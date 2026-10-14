package duktape

// What the heap's block cache saves (see HEAP_CACHE.md): a call that builds
// garbage and drops it, with the cache off and on. Besides the time, it
// reports the page faults per call, which is how the allocator going back to
// the kernel on every call shows.
//
// The effect is musl's: build and run it statically linked against musl, in
// the flake's musl shell. Against glibc the cache saves far less.
//
//   rm -f heapcache.test &&
//   nix develop .#musl --command go test -c -o heapcache.test \
//       -tags netgo -ldflags '-s -w -extldflags=-static' . &&
//   ! strings heapcache.test | grep -q GLIBC_ &&   # musl, not a static glibc
//   ./heapcache.test -test.run=XXX -test.bench=HeapCache -test.benchtime=20000x -test.count=10 > bench.txt &&
//   benchstat -col /cache bench.txt

import (
	"fmt"
	"syscall"
	"testing"
)

// The two kinds of garbage a call may build: loose objects, all of them
// small blocks; and an array that grows past 1 KB, whose element storage is a
// large block reallocated as it grows.
const benchWork = `
function objects(n) {
    var head = null;
    for (var i = 0; i < n; i++) { head = { n: i, s: "v" + i, next: head }; }
    return n;
}
function array(n) {
    var a = [];
    for (var i = 0; i < n; i++) { a.push({ n: i, s: "v" + i }); }
    return a.length;
}
`

// minorFaults is the process's count of page faults that needed no I/O,
// which is what mapping fresh pages costs.
func minorFaults(b *testing.B) int64 {
	var ru syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru); err != nil {
		b.Fatal(err)
	}
	return int64(ru.Minflt)
}

func BenchmarkHeapCache(b *testing.B) {
	caches := []struct {
		name     string
		maxBytes int
	}{
		{"off", -1},
		{"on", 0},
	}
	for _, work := range []string{"objects", "array"} {
		for _, n := range []int{32, 128, 512} {
			for _, c := range caches {
				b.Run(fmt.Sprintf("work=%s/n=%d/cache=%s", work, n, c.name), func(b *testing.B) {
					ctx := NewWithFlags(&Flags{HeapCacheMaxBytes: c.maxBytes})
					defer ctx.DestroyHeap()
					if err := ctx.PevalString(benchWork); err != nil {
						b.Fatal(err)
					}
					ctx.Pop()

					faults := minorFaults(b)
					b.ResetTimer()
					for i := 0; i < b.N; i++ {
						ctx.GetGlobalString(work)
						ctx.PushInt(n)
						if ctx.Pcall(1) != ExecSuccess {
							b.Fatal(ctx.SafeToString(-1))
						}
						ctx.Pop()
					}
					b.StopTimer()
					b.ReportMetric(float64(minorFaults(b)-faults)/float64(b.N), "faults/op")
				})
			}
		}
	}
}
