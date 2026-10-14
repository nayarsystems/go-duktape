package duktape

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"unsafe"

	"github.com/stretchr/testify/require"
)

// churn is a script that allocates and frees a lot, at sizes inside and
// outside the cached classes, and checks its own results: a block handed out
// twice, or a realloc that kept too small a block, shows up as a wrong
// result rather than only as a crash.
const churn = `
function churn(rounds) {
    var sum = 0;
    for (var r = 0; r < rounds; r++) {
        var o = {};
        var a = [];
        var s = "";
        for (var i = 0; i < 200; i++) {
            o["k" + i] = { n: i, s: "v" + i };
            a.push(i * r);
            s += String.fromCharCode(65 + (i % 26));
        }
        var big = new Array(2000).join("x" + r);
        for (var i = 0; i < 200; i++) {
            if (o["k" + i].n !== i || o["k" + i].s !== "v" + i) { throw new Error("object corrupted"); }
            sum += a[i];
        }
        if (s.length !== 200 || s.charAt(27) !== "B") { throw new Error("string corrupted"); }
        if (big.length !== 1999 * (1 + String(r).length)) { throw new Error("big string corrupted"); }
        var buf = new Uint8Array(3000);
        for (var i = 0; i < buf.length; i++) { buf[i] = i & 255; }
        for (var i = 0; i < buf.length; i++) { if (buf[i] !== (i & 255)) { throw new Error("buffer corrupted"); } }
    }
    return sum;
}
`

func runChurn(t *testing.T, ctx *Context, rounds int) float64 {
	t.Helper()
	require.NoError(t, ctx.PevalString(churn))
	ctx.Pop()
	require.NoError(t, ctx.PevalString(fmt.Sprintf("churn(%d)", rounds)))
	defer ctx.Pop()
	return ctx.GetNumber(-1)
}

func churnWant(rounds int) float64 {
	sum := 0
	for r := 0; r < rounds; r++ {
		for i := 0; i < 200; i++ {
			sum += i * r
		}
	}
	return float64(sum)
}

// Scripts that allocate, grow and free a lot get the same results with the
// cache as without it.
func TestHeapCache_KeepsResultsRight(t *testing.T) {
	ctx := New()
	defer ctx.DestroyHeap()
	require.Equal(t, churnWant(50), runChurn(t, ctx, 50))
}

// After a call, the blocks it freed are kept for the next one, within the
// bound.
func TestHeapCache_KeepsFreedBlocks(t *testing.T) {
	ctx := New()
	defer ctx.DestroyHeap()
	runChurn(t, ctx, 5)
	ctx.Gc(0)
	cached := ctx.heapCacheBytes()
	require.Greater(t, cached, 0, "freed small blocks stay in the cache")
	require.LessOrEqual(t, cached, DefaultHeapCacheMaxBytes, "within the bound")
}

// Freeing far more small blocks than the bound keeps only up to the bound:
// the rest goes back to malloc.
func TestHeapCache_IsBounded(t *testing.T) {
	ctx := New()
	defer ctx.DestroyHeap()
	freeLots(t, ctx)
	require.Greater(t, ctx.heapCacheBytes(), DefaultHeapCacheMaxBytes/2, "the script freed well past the bound")
	require.LessOrEqual(t, ctx.heapCacheBytes(), DefaultHeapCacheMaxBytes)
}

// freeLots frees far more small blocks than any bound used in these tests.
func freeLots(t *testing.T, ctx *Context) {
	t.Helper()
	require.NoError(t, ctx.PevalString(`
        var keep = [];
        for (var i = 0; i < 50000; i++) { keep.push({ n: i, s: "v" + i }); }
        keep = null;
    `))
	ctx.Pop()
	ctx.Gc(0)
}

// Flags.HeapCacheMaxBytes sets the bound.
func TestHeapCache_CustomBound(t *testing.T) {
	ctx := NewWithFlags(&Flags{HeapCacheMaxBytes: 32 * 1024})
	defer ctx.DestroyHeap()
	freeLots(t, ctx)
	require.Greater(t, ctx.heapCacheBytes(), 16*1024)
	require.LessOrEqual(t, ctx.heapCacheBytes(), 32*1024)
}

// A negative Flags.HeapCacheMaxBytes disables the cache: nothing is kept,
// with or without tracking hooks, and the heap works as before.
func TestHeapCache_Disabled(t *testing.T) {
	ctx := NewWithFlags(&Flags{HeapCacheMaxBytes: -1})
	require.Equal(t, churnWant(5), runChurn(t, ctx, 5))
	freeLots(t, ctx)
	require.Equal(t, 0, ctx.heapCacheBytes())
	ctx.DestroyHeap()

	var mu sync.Mutex
	live := map[unsafe.Pointer]bool{}
	ctx = NewWithFlags(&Flags{
		HeapCacheMaxBytes: -1,
		MallocHook:        func(p unsafe.Pointer, _ uint64) { mu.Lock(); live[p] = true; mu.Unlock() },
		FreeHook:          func(p unsafe.Pointer) { mu.Lock(); delete(live, p); mu.Unlock() },
		ReallocHook: func(p, np unsafe.Pointer, _ uint64) {
			mu.Lock()
			delete(live, p)
			live[np] = true
			mu.Unlock()
		},
	})
	require.Equal(t, churnWant(5), runChurn(t, ctx, 5))
	freeLots(t, ctx)
	require.Equal(t, 0, ctx.heapCacheBytes())
	ctx.DestroyHeap()
	mu.Lock()
	defer mu.Unlock()
	delete(live, nil)
	require.Empty(t, live)
}

// NewWithFlags without hooks creates the same cached heap New does.
func TestHeapCache_NewWithFlagsWithoutHooks(t *testing.T) {
	ctx := NewWithFlags(&Flags{})
	defer ctx.DestroyHeap()
	require.Equal(t, churnWant(5), runChurn(t, ctx, 5))
	ctx.Gc(0)
	require.Greater(t, ctx.heapCacheBytes(), 0)
}

// With tracking hooks the heap still sits on the cache, and the hooks see
// every allocation duktape makes and frees: by the time the heap is gone,
// what was allocated has all been freed.
func TestHeapCache_UnderTrackingHooks(t *testing.T) {
	var mu sync.Mutex
	live := map[unsafe.Pointer]uint64{}
	ctx := NewWithFlags(&Flags{
		MallocHook: func(p unsafe.Pointer, size uint64) {
			mu.Lock()
			defer mu.Unlock()
			if p != nil {
				live[p] = size
			}
		},
		FreeHook: func(p unsafe.Pointer) {
			mu.Lock()
			defer mu.Unlock()
			delete(live, p)
		},
		ReallocHook: func(p, np unsafe.Pointer, size uint64) {
			mu.Lock()
			defer mu.Unlock()
			delete(live, p)
			if np != nil {
				live[np] = size
			}
		},
	})
	require.Equal(t, churnWant(5), runChurn(t, ctx, 5))
	ctx.Gc(0)
	require.Greater(t, ctx.heapCacheBytes(), 0, "the cache is in use under the hooks")

	mu.Lock()
	inUse := len(live)
	mu.Unlock()
	require.Greater(t, inUse, 0, "the hooks see what the heap holds")

	ctx.DestroyHeap()
	mu.Lock()
	defer mu.Unlock()
	require.Empty(t, live, "everything allocated was freed when the heap went away")
}

// The heap's udata now carries the cache as well as the context id, which the
// exec timeout check reads to find its Go context: it still does, whichever
// way the heap was created.
func TestHeapCache_TimeoutFindsItsContext(t *testing.T) {
	hook := func(unsafe.Pointer, uint64) {}
	cases := map[string]*Flags{
		"cache":           {},
		"no cache":        {HeapCacheMaxBytes: -1},
		"hooks":           {MallocHook: hook},
		"hooks, no cache": {MallocHook: hook, HeapCacheMaxBytes: -1},
	}
	for name, flags := range cases {
		t.Run(name, func(t *testing.T) {
			ctx := NewWithFlags(flags)
			defer ctx.DestroyHeap()
			calls := 0
			ctx.SetExecTimeoutCheckHandler(func(*Context) bool {
				calls++
				return calls > 3
			})
			err := ctx.PevalString(`while (true) {}`)
			require.ErrorContains(t, err, "execution timeout")
		})
	}
}

// Heaps are created and destroyed over and over, each with its own cache.
func TestHeapCache_ManyHeaps(t *testing.T) {
	for i := 0; i < 200; i++ {
		ctx := New()
		require.Equal(t, churnWant(2), runChurn(t, ctx, 2))
		ctx.DestroyHeap()
	}
}

// A string that grows past the biggest class and back keeps its content.
func TestHeapCache_GrowingString(t *testing.T) {
	ctx := New()
	defer ctx.DestroyHeap()
	require.NoError(t, ctx.PevalString(`
        var s = "";
        for (var i = 0; i < 5000; i++) { s += "ab"; }
        s.slice(0, 10) + "|" + s.length;
    `))
	defer ctx.Pop()
	require.Equal(t, strings.Repeat("ab", 5)+"|10000", ctx.GetString(-1))
}
