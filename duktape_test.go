package duktape

import (
	goContext "context"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	. "gopkg.in/check.v1"
)

// Hook up gocheck into the "go test" runner.
func Test(t *testing.T) { TestingT(t) }

var _ = Suite(&DuktapeSuite{})

type DuktapeSuite struct {
	ctx *Context
}

func (s *DuktapeSuite) SetUpTest(c *C) {
	s.ctx = New()
}

func (s *DuktapeSuite) TestPushGlobalGoFunction_Call(c *C) {
	var check bool
	idx, err := s.ctx.PushGlobalGoFunction("test", func(c *Context) int {
		check = !check
		return 0
	})

	c.Assert(err, IsNil)
	c.Assert(idx, Not(Equals), -1)

	c.Assert(s.ctx.fnIndex.functions, HasLen, 1)

	err = s.ctx.PevalString("test();")
	c.Assert(err, IsNil)
	c.Assert(check, Equals, true)

	err = s.ctx.PevalString("test();")
	c.Assert(err, IsNil)
	c.Assert(check, Equals, false)
}

func (s *DuktapeSuite) TestPushGlobalGoFunction_Malformed(c *C) {
	idx, err := s.ctx.PushGlobalGoFunction(".", func(c *Context) int {
		return 0
	})

	c.Assert(err, ErrorMatches, "Malformed function name '.'")
	c.Assert(idx, Equals, -1)
}

func (s *DuktapeSuite) TestPushGlobalGoFunction_Finalize(c *C) {
	s.ctx.PushGlobalGoFunction("test", func(c *Context) int {
		return 0
	})

	c.Assert(s.ctx.fnIndex.functions, HasLen, 1)

	err := s.ctx.PevalString("test = undefined")
	c.Assert(err, IsNil)

	s.ctx.Gc(0)
	c.Assert(s.ctx.fnIndex.functions, HasLen, 0)
}

func (s *DuktapeSuite) TestPushGoFunction_Call(c *C) {
	var check bool
	s.ctx.PushGlobalObject()
	s.ctx.PushGoFunction(func(c *Context) int {
		check = !check
		return 0
	})

	s.ctx.PutPropString(-2, "test")
	s.ctx.Pop()

	c.Assert(s.ctx.fnIndex.functions, HasLen, 1)

	err := s.ctx.PevalString("test();")
	c.Assert(err, IsNil)
	c.Assert(check, Equals, true)

	err = s.ctx.PevalString("test();")
	c.Assert(err, IsNil)
	c.Assert(check, Equals, false)
}

func goTestfunc(ctx *Context) int {
	top := ctx.GetTop()
	a := ctx.GetNumber(top - 2)
	b := ctx.GetNumber(top - 1)
	ctx.PushNumber(a + b)
	return 1
}

func (s *DuktapeSuite) TestMyAddTwo(c *C) {
	s.ctx.PushGlobalGoFunction("adder", goTestfunc)
	err := s.ctx.PevalString(`print("2 + 3 =", adder(2,3))`)
	c.Assert(err, IsNil)

	s.ctx.Pop()

	err = s.ctx.PevalString(`adder(2,3)`)
	c.Assert(err, IsNil)

	c.Assert(s.ctx.GetNumber(-1), Equals, 5.0)
}

func (s *DuktapeSuite) TestPevalTimeout(c *C) {
	ctx, ctxCancel := goContext.WithCancel(goContext.Background())
	s.ctx.SetGoContext(ctx)
	s.ctx.SetExecTimeoutCheckHandler(func(dctx *Context) bool {
		return dctx.GetGoContext().Err() != nil
	})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		err := s.ctx.PevalString(`while(true){}`)
		require.ErrorContains(c, err, "execution timeout")
	}()
	ctxCancel()
	wg.Wait()
}

func (s *DuktapeSuite) TestPcallTimeout(c *C) {
	ctx, ctxCancel := goContext.WithCancel(goContext.Background())
	s.ctx.SetGoContext(ctx)
	s.ctx.SetExecTimeoutCheckHandler(func(dctx *Context) bool {
		return dctx.GetGoContext().Err() != nil
	})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		err := s.ctx.PcompileString(CompileFunction, `function(){ while(true){} }`)
		require.NoError(c, err)
		res := s.ctx.Pcall(0)
		require.Equal(c, ExecError, res)
		errMsg := s.ctx.SafeToString(-1)
		require.Contains(c, errMsg, "execution timeout")
	}()
	ctxCancel()
	wg.Wait()
}

func (s *DuktapeSuite) TestCreateContexts(c *C) {
	dukCtx := New()
	dukCtxFromMap := contexts.get(dukCtx.dukCtxCPtr)
	require.Equal(c, dukCtx, dukCtxFromMap)
	require.Len(c, contexts.ctxs, 2)
	dukCtx.DestroyHeap()
}

func (s *DuktapeSuite) TearDownTest(c *C) {
	dukCtx := contexts.get(s.ctx.dukCtxCPtr)
	require.Equal(c, s.ctx, dukCtx)
	require.Len(c, contexts.ctxs, 1)
	s.ctx.DestroyHeap()
	require.Len(c, contexts.ctxs, 0)
}
