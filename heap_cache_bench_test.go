package duktape

// What the heap's block cache saves (see HEAP_CACHE.md): a call that builds
// garbage and drops it, with the cache off and on. Besides the time, it
// reports the page faults per thousand calls (faults/kop), which is how the
// allocator going back to the kernel on every call shows. Per thousand, not
// per call, so that the near-zero side does not come out in benchstat as µ
// and the other as m.
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
// small blocks; and an array whose element storage (16 bytes per entry on
// x86_64) is a block reallocated as it grows: past 1 KB at 64 entries, and
// past the largest cached class (16 KB) at 1024, which n=2048 is there to
// show. On armv7 entries take 8 bytes, so each threshold comes at twice n.
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
	works := []struct {
		name string
		ns   []int
	}{
		{"objects", []int{32, 128, 512}},
		{"array", []int{32, 128, 512, 2048}},
	}
	for _, w := range works {
		work := w.name
		for _, n := range w.ns {
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
					b.ReportMetric(float64(minorFaults(b)-faults)*1000/float64(b.N), "faults/kop")
				})
			}
		}
	}
}
