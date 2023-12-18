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

extern duk_ret_t goFunctionCall(duk_context *ctx);
extern void goFinalizeCall(duk_context *ctx);

*/
import "C"
import (
	goContext "context"
	"errors"
	"fmt"
	"regexp"
	"sync"
	"unsafe"
)

var reFuncName = regexp.MustCompile("^[a-z_][a-z0-9_]*([A-Z_][a-z0-9_]*)*$")

const (
	goFunctionPtrProp = "\xff" + "goFunctionPtrProp"
	goContextPtrProp  = "\xff" + "goContextPtrProp"
)

type funcIdKey uint64
type contextCPtr = unsafe.Pointer

type Context struct {
	*context
}

// this is a pojo containing only the values of the Context
type context struct {
	sync.Mutex
	duk_context             *C.duk_context
	dukCtxCPtr              contextCPtr
	fnIndex                 *functionIndex
	timerIndex              *timerIndex
	fatalErrorHandler       func(dctx *Context, emsg string)
	execTimeoutCheckHandler func(dctx *Context) bool
	goCtx                   goContext.Context
}

func createDukContext(d *Context) {
	// Create a unique identifier for this context.
	//
	// This identifier is used to identify the context in the C callbacks so that
	// we can retrieve the Context object from the contexts map.
	//
	// Some C callbacks are called passing the context pointer as a void *
	// which the Go runtime will convert to an unsafe.Pointer.
	//
	// Since the unsafe.Pointer of *Context can be updated by de GC we can't use it
	// as the void * pointer. Instead, we need to create unsafe.Pointer that is not managed by the GC.
	// We can achieve that by allocating a single byte of heap in C and storing the pointer to that byte
	// in go as a unsafe.Pointer.
	//
	// This means that unsafe.Pointer will not point to the Context object, but will
	// serve as a unique identifier for the context.
	//
	// We will store this C pointer (unsafe.Pointer in go) in the Context object.
	if d.dukCtxCPtr != nil {
		panic(fmt.Sprintf("context (%p) already exists", d))
	}

	d.dukCtxCPtr = contexts.add(d)
	d.duk_context = C.duk_create_heap(nil, nil, nil, d.dukCtxCPtr, nil)
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

func (d *Context) SetGoContext(goCtx goContext.Context) {
	d.goCtx = goCtx
}

func (d *Context) GetGoContext() goContext.Context {
	return d.goCtx
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
	d.PushPointer(d.dukCtxCPtr)
	d.PutPropString(-2, goContextPtrProp)
	d.SetFinalizer(-2)

	d.PushNumber(float64(funcId))
	d.PutPropString(-2, goFunctionPtrProp)
	d.PushPointer(d.dukCtxCPtr)
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
func goExecTimeoutCheck(userData unsafe.Pointer) C.duk_bool_t {
	if userData == nil {
		return 0
	}
	d := contexts.get(userData)
	var res C.duk_bool_t
	if d != nil && d.execTimeoutCheckHandler != nil && d.execTimeoutCheckHandler(d) {
		res = 1
	} else {
		res = 0
	}
	return res
}

//export goFatalErrorHandler
func goFatalErrorHandler(userData unsafe.Pointer, msg *C.char) {
	var d *Context
	if userData == nil {
		goto defaultPanicHandler
	}
	d = contexts.get(userData)
	if d != nil && d.fatalErrorHandler != nil {
		d.fatalErrorHandler(d, C.GoString(msg))
		return
	}
defaultPanicHandler:
	panic(fmt.Sprintf("duktape fatal error: %s", C.GoString(msg)))

}

func (d *Context) getFunctionPtrs() (funcIdKey, *Context) {
	d.PushCurrentFunction()
	d.GetPropString(-1, goFunctionPtrProp)
	funPtr := funcIdKey(d.GetNumber(-1))

	d.Pop()

	d.GetPropString(-1, goContextPtrProp)
	ctx := contexts.get(d.GetPointer(-1))
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
	currentIndex funcIdKey
	functions    map[funcIdKey]func(*Context) int
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
		functions: make(map[funcIdKey]func(*Context) int, 0),
	}
}

func (i *functionIndex) add(fn func(*Context) int) funcIdKey {
	i.Lock()
	ptr := i.currentIndex
	i.currentIndex++
	i.functions[ptr] = fn
	i.Unlock()

	return ptr
}

func (i *functionIndex) get(ptr funcIdKey) func(*Context) int {
	i.RLock()
	fn := i.functions[ptr]
	i.RUnlock()

	return fn
}

func (i *functionIndex) delete(ptr funcIdKey) {
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
	ctxs map[contextCPtr]*Context
}

func (ci *ctxIndex) add(d *Context) contextCPtr {
	ci.Lock()
	defer ci.Unlock()
	ptr := C.malloc(1)
	ci.ctxs[ptr] = d
	return ptr
}

func (ci *ctxIndex) get(ptr contextCPtr) *Context {
	ci.RLock()
	defer ci.RUnlock()
	ctx := ci.ctxs[ptr]
	return ctx
}

func (ci *ctxIndex) delete(ctx *Context) {
	ci.Lock()
	defer ci.Unlock()
	if _, ok := ci.ctxs[ctx.dukCtxCPtr]; ok {
		delete(ci.ctxs, ctx.dukCtxCPtr)
		C.free(ctx.dukCtxCPtr)
		return
	}
	panic(fmt.Sprintf("context (%p) doesn't exist", ctx))
}

var contexts *ctxIndex

func init() {
	contexts = &ctxIndex{
		ctxs: make(map[contextCPtr]*Context),
	}
}
