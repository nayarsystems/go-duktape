package duktape

/*
#cgo !windows CFLAGS: -std=c99 -O3 -Wall -fomit-frame-pointer -fstrict-aliasing
#cgo windows CFLAGS: -O3 -Wall -fomit-frame-pointer -fstrict-aliasing
#cgo linux LDFLAGS: -lm
#cgo freebsd LDFLAGS: -lm
#cgo openbsd LDFLAGS: -lm
#cgo dragonfly LDFLAGS: -lm

#include "duktape.h"
#include "duk_logging.h"
#include "duk_print_alert.h"
#include "duk_module_duktape.h"
#include "duk_console.h"
#include "go_wrappers.h"

extern duk_ret_t goFunctionCall(duk_context *ctx);
extern void goFinalizeCall(duk_context *ctx);
*/
import "C"
import (
	"errors"
	"fmt"
	"regexp"
	"sync"
)

var reFuncName = regexp.MustCompile("^[a-z_][a-z0-9_]*([A-Z_][a-z0-9_]*)*$")

const (
	goFunctionPtrProp = "\xff" + "goFunctionPtrProp"
	goContextPtrProp  = "\xff" + "goContextPtrProp"
)

type IdKey = uint32

type Context struct {
	*context
}

// this is a pojo containing only the values of the Context
type context struct {
	sync.Mutex
	duk_context             *C.duk_context
	dukCtxId                IdKey
	fnIndex                 *functionIndex
	timerIndex              *timerIndex
	fatalErrorHandler       func(dctx *Context, emsg string)
	execTimeoutCheckHandler func(dctx *Context) bool
	data                    any
}

func createDukContext(d *Context) {
	// Create a unique identifier for this context.
	if d.dukCtxId != 0 {
		panic(fmt.Sprintf("context (%p) already exists", d))
	}

	d.dukCtxId = contexts.add(d)
	d.duk_context = C.goWrapperDukCreateHeap(C.uint32_t(d.dukCtxId))
}

// New returns plain initialized duktape context object
// See: http://duktape.org/api.html#duk_create_heap_default
func New() *Context {
	d := &Context{
		&context{
			fnIndex:    newFunctionIndex(),
			timerIndex: &timerIndex{},
		},
	}
	createDukContext(d)
	C.duk_logging_init(d.duk_context, 0)
	C.duk_print_alert_init(d.duk_context, 0)
	C.duk_module_duktape_init(d.duk_context)
	C.duk_console_init(d.duk_context, 0)

	return d
}

// Flags is a set of flags for controlling the behaviour of duktape.
type Flags struct {
	Logging    uint
	PrintAlert uint
	Console    uint
}

// FlagConsoleProxyWrapper is a Console flag.
// Use a proxy wrapper to make undefined methods (console.foo()) no-ops.
const FlagConsoleProxyWrapper = 1 << 0

// FlagConsoleFlush is a Console flag.
// Flush output after every call.
const FlagConsoleFlush = 1 << 1

// NewWithFlags returns plain initialized duktape context object
// You can control the behaviour of duktape by setting flags.
// See: http://duktape.org/api.html#duk_create_heap_default
func NewWithFlags(flags *Flags) *Context {
	d := &Context{
		&context{
			fnIndex:    newFunctionIndex(),
			timerIndex: &timerIndex{},
		},
	}
	createDukContext(d)
	C.duk_logging_init(d.duk_context, C.duk_uint_t(flags.Logging))
	C.duk_print_alert_init(d.duk_context, C.duk_uint_t(flags.PrintAlert))
	C.duk_module_duktape_init(d.duk_context)
	C.duk_console_init(d.duk_context, C.duk_uint_t(flags.Console))

	return d
}

func contextFromPointer(ctx *C.duk_context) *Context {
	return &Context{&context{duk_context: ctx}}
}

func (d *Context) SetExecTimeoutCheckHandler(fn func(dctx *Context) bool) {
	d.execTimeoutCheckHandler = fn
}

func (d *Context) SetFatalErrorHandler(fn func(dctx *Context, emsg string)) {
	d.fatalErrorHandler = fn
}

func (d *Context) SetContextData(data any) {
	d.data = data
}

func (d *Context) GetContextData() any {
	return d.data
}

// PushGlobalGoFunction push the given function into duktape global object
// Returns non-negative index (relative to stack bottom) of the pushed function
// also returns error if the function name is invalid
func (d *Context) PushGlobalGoFunction(name string, fn func(*Context) int) (int, error) {
	if !reFuncName.MatchString(name) {
		return -1, errors.New("Malformed function name '" + name + "'")
	}

	d.PushGlobalObject()
	idx := d.PushGoFunction(fn)
	d.PutPropString(-2, name)
	d.Pop()

	return idx, nil
}

// PushGoFunction push the given function into duktape stack, returns non-negative
// index (relative to stack bottom) of the pushed function
func (d *Context) PushGoFunction(fn func(*Context) int) int {
	funcId := d.fnIndex.add(fn)

	idx := d.PushCFunction((*[0]byte)(C.goFunctionCall), C.DUK_VARARGS)
	d.PushCFunction((*[0]byte)(C.goFinalizeCall), 1)
	d.PushNumber(float64(funcId))
	d.PutPropString(-2, goFunctionPtrProp)
	d.PushNumber(float64(d.dukCtxId))
	d.PutPropString(-2, goContextPtrProp)
	d.SetFinalizer(-2)

	d.PushNumber(float64(funcId))
	d.PutPropString(-2, goFunctionPtrProp)
	d.PushNumber(float64(d.dukCtxId))
	d.PutPropString(-2, goContextPtrProp)

	return idx
}

//export goFunctionCall
func goFunctionCall(cCtx *C.duk_context) C.duk_ret_t {
	d := contextFromPointer(cCtx)

	funPtr, d := d.getFunctionPtrs()

	result := d.fnIndex.get(funPtr)(d)

	return C.duk_ret_t(result)
}

//export goFinalizeCall
func goFinalizeCall(cCtx *C.duk_context) {
	d := contextFromPointer(cCtx)

	funPtr, d := d.getFunctionPtrs()

	d.fnIndex.delete(funPtr)
}

//export goExecTimeoutCheck
func goExecTimeoutCheck(id C.uint32_t) C.duk_bool_t {
	d := contexts.get(uint32(id))
	var res C.duk_bool_t
	if d != nil && d.execTimeoutCheckHandler != nil && d.execTimeoutCheckHandler(d) {
		res = 1
	} else {
		res = 0
	}
	return res
}

//export goFatalErrorHandler
func goFatalErrorHandler(id C.uint32_t, msg *C.char) {
	d := contexts.get(uint32(id))
	if d != nil && d.fatalErrorHandler != nil {
		d.fatalErrorHandler(d, C.GoString(msg))
		return
	}
	panic(fmt.Sprintf("duktape fatal error: %s", C.GoString(msg)))
}

func (d *Context) getFunctionPtrs() (IdKey, *Context) {
	d.PushCurrentFunction()
	d.GetPropString(-1, goFunctionPtrProp)
	funPtr := IdKey(d.GetNumber(-1))

	d.Pop()

	d.GetPropString(-1, goContextPtrProp)
	ctx := contexts.get(IdKey(d.GetNumber(-1)))
	d.Pop2()
	return funPtr, ctx
}

type Error struct {
	Type       string
	Message    string
	FileName   string
	LineNumber int
	Stack      string
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s: %s", e.Type, e.Message)
}

type Type uint

func (t Type) IsNone() bool      { return t == TypeNone }
func (t Type) IsUndefined() bool { return t == TypeUndefined }
func (t Type) IsNull() bool      { return t == TypeNull }
func (t Type) IsBool() bool      { return t == TypeBoolean }
func (t Type) IsNumber() bool    { return t == TypeNumber }
func (t Type) IsString() bool    { return t == TypeString }
func (t Type) IsObject() bool    { return t == TypeObject }
func (t Type) IsBuffer() bool    { return t == TypeBuffer }
func (t Type) IsPointer() bool   { return t == TypePointer }
func (t Type) IsLightFunc() bool { return t == TypeLightFunc }

func (t Type) String() string {
	switch t {
	case TypeNone:
		return "None"
	case TypeUndefined:
		return "Undefined"
	case TypeNull:
		return "Null"
	case TypeBoolean:
		return "Boolean"
	case TypeNumber:
		return "Number"
	case TypeString:
		return "String"
	case TypeObject:
		return "Object"
	case TypeBuffer:
		return "Buffer"
	case TypePointer:
		return "Pointer"
	case TypeLightFunc:
		return "LightFunc"
	default:
		return "Unknown"
	}
}

type functionIndex struct {
	currentIndex IdKey
	functions    map[IdKey]func(*Context) int
	sync.RWMutex
}

type timerIndex struct {
	c float64
	sync.Mutex
}

func (t *timerIndex) get() float64 {
	t.Lock()
	defer t.Unlock()
	t.c++
	return t.c
}

func newFunctionIndex() *functionIndex {
	return &functionIndex{
		functions: make(map[IdKey]func(*Context) int, 0),
	}
}

func (i *functionIndex) add(fn func(*Context) int) IdKey {
	i.Lock()
	ptr := i.currentIndex
	i.currentIndex++
	i.functions[ptr] = fn
	i.Unlock()

	return ptr
}

func (i *functionIndex) get(ptr IdKey) func(*Context) int {
	i.RLock()
	fn := i.functions[ptr]
	i.RUnlock()

	return fn
}

func (i *functionIndex) delete(ptr IdKey) {
	i.Lock()
	delete(i.functions, ptr)
	i.Unlock()
}

func (i *functionIndex) destroy() {
	i.Lock()

	for ptr := range i.functions {
		delete(i.functions, ptr)
	}
	i.Unlock()
}

type ctxIndex struct {
	sync.RWMutex
	currentIndex IdKey
	ctxs         map[IdKey]*Context
}

func (ci *ctxIndex) add(d *Context) IdKey {
	ci.Lock()
	defer ci.Unlock()
	ci.currentIndex++
	ci.ctxs[ci.currentIndex] = d
	return ci.currentIndex
}

func (ci *ctxIndex) get(ptr IdKey) *Context {
	ci.RLock()
	defer ci.RUnlock()
	ctx := ci.ctxs[ptr]
	return ctx
}

func (ci *ctxIndex) delete(ctx *Context) {
	ci.Lock()
	defer ci.Unlock()
	if _, ok := ci.ctxs[ctx.dukCtxId]; ok {
		delete(ci.ctxs, ctx.dukCtxId)
		return
	}
	panic(fmt.Sprintf("context (%p) doesn't exist", ctx))
}

var contexts *ctxIndex

func init() {
	contexts = &ctxIndex{
		ctxs: make(map[IdKey]*Context),
	}
}
