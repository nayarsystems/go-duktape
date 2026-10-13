# The heap's block cache

Every duktape heap this package creates (`New`, and `NewWithFlags` with or
without tracking hooks) allocates its memory through a small per-heap cache of
freed blocks (`heap_cache.c`). This page explains why it exists, how it
works, and how to check that it does its job.

## Why

duktape allocates and frees constantly: every object, string, buffer and
property table of a script, and much of it only lives for one call. By
default it does that through the C library's `malloc` and `free`.

A program linked statically against **musl** (as the idefix firmware is, on
every architecture) gets musl's allocator, mallocng. mallocng groups blocks of
the same size class and hands a group back to the kernel (`munmap`) **as soon
as every block in it is free**. That is a good policy for memory footprint,
and a bad one for a pattern like this:

1. a call allocates a few blocks of some size class, which fills a group;
2. the call ends and frees them: the group is empty, so it is unmapped;
3. the next call allocates the same blocks again: a group is mapped again, and
   its pages are faulted in one by one.

When that happens, **every call** pays an `mmap`, an `munmap` and a page
fault per page touched. It does not show in the instructions executed; it
shows as time spent in the kernel.

Whether it happens depends on exactly where a heap's allocations fall relative
to the group boundaries, which depends on everything the script does, and on
how its source happens to be laid out. In idefix it appeared and disappeared
with changes that did nothing to the code: importing a module without using
it, or removing semicolons that automatic semicolon insertion made redundant.
Measured on the gateway's bevents snippet (`BenchmarkCore_Tick` in
`targets/gsr2`), with the same bundle:

| | Time per call | Page faults (20k calls) | mmap+munmap (5k calls) | Instructions |
|---|---|---|---|---|
| musl, no cache | 129 µs | 161,511 | 40,332 | 11.93 G |
| musl, with the cache | 83 µs | 1,312 | 209 | 11.38 G |
| glibc, no cache | 78 µs | — | — | — |

glibc keeps freed memory around (below its trim threshold), which is why the
effect does not show on a desktop build.

## How it works

### Size classes and free lists

Requests up to **1024 bytes** are rounded up to a multiple of **16**: 64 size
classes (16, 32, 48, … 1024). Each class has a singly linked list of free
blocks; the link lives in the first bytes of the free block itself, so a list
costs no memory of its own.

- **alloc(size)**: take a block from the class's list if there is one;
  otherwise `malloc` a new one of the class's size.
- **free(ptr)**: push the block onto its class's list, unless the cache is
  already at its bound (see below), in which case it goes back to `free`.
- **realloc(ptr, size)**: if the new size still fits in the block's class,
  return the same block (duktape grows strings and buffers by small steps, so
  this is common); otherwise allocate a block of the new size, copy, and free
  the old one through the cache.

Requests larger than 1024 bytes are not cached: they go straight to
`malloc`/`realloc`/`free`, marked as such. Large blocks are rare and reusing
them exactly is unlikely, and they are what mallocng handles well anyway.

### The header

duktape's `free` and `realloc` do not say how big a block is, so each block
carries a small header in front of it with its class (0 for a large, uncached
block). The header is sized and aligned like the largest scalar type, so the
pointer handed to duktape is as aligned as one from `malloc`:

| Architecture | Header |
|---|---|
| x86_64, aarch64 | 16 bytes |
| armv7 | 8 bytes |

On top of the header, a block also spends up to 15 bytes on rounding to its
class. That is the cache's cost in footprint. It has not been measured; as an
estimate, for duktape's typical allocations of tens to a few hundred bytes it
adds some 10–25% to the heap's live size (16 bytes of header and 7.5 of
rounding on average, on a 100-byte block, is 23%).

### The bound

Freed blocks are kept only while the cache holds at most **256 KB** per
heap. Past that, freed blocks go back to `free` as without the cache. So a
heap never sits on more than 256 KB of memory it is not using, and a script
that frees a large amount at once (dropping a big object graph) still returns
most of it.

The cache is emptied, and all its blocks freed, when the heap is destroyed
(`DestroyHeap`).

### Where it lives

duktape lets the embedder supply the heap's memory functions and an opaque
`udata` pointer that it hands back to them (`duk_create_heap`). The package
already used `udata` to carry the Go context id, which the exec timeout check
and the fatal error handler use to find their Go context. Now `udata` points
to a small struct (`goHeapUdata`) holding that id and the cache, and those
handlers read the id from it.

`DestroyHeap` reads the `udata` back with `duk_get_memory_functions` before
destroying the heap, and frees the struct (and the cache) after
`duk_destroy_heap`, which still frees the heap's memory through the cache.

### Threads

The cache has no lock. A duktape heap must only be used by one thread at a
time, and go-duktape's users serialize on the context's lock, so the memory
functions of one heap are never called concurrently. Different heaps have
different caches.

### Tracking hooks

With `NewWithFlags` and any of `MallocHook`, `ReallocHook` or `FreeHook`, the
tracking functions sit **on top of** the cache: they allocate through it and
then tell the hooks. A tracked heap therefore allocates exactly as an
untracked one does, and the hooks see what duktape allocates and frees. A
block kept in the cache counts as freed, so a heap tracker measures what the
heap is using, not what the process holds on its behalf.

`NewWithFlags` without any hook creates the same heap as `New`, without
calling into Go on each allocation.

## What it does not cover

- **cgo's own allocations.** Strings passed from Go to duktape go through
  `C.CString`, which uses the C library's `malloc` directly, not the heap's
  memory functions. If an allocation pattern there ever triggers the same
  effect, the cache does not help.
- **Memory given back early.** Up to 256 KB of freed blocks per heap stay
  allocated until the heap is destroyed. A process with N heaps may hold up to
  N × 256 KB more than without the cache.

## Checking it

Tests: `heap_cache_test.go` runs scripts that allocate, grow and free a lot
and check their own results, and checks the cache's bound, its use under
tracking hooks and the hooks' bookkeeping.

To see the effect itself, run a benchmark of a script under a static musl
build and look at page faults and memory syscalls:

```bash
perf stat -e task-clock,instructions,page-faults ./bench.test -test.bench=X -test.benchtime=20000x
strace -f -c -e trace=mmap,munmap ./bench.test -test.bench=X -test.benchtime=5000x
```

A per-call `mmap`/`munmap` pair, or page faults growing with the number of
calls, means the allocator is going back to the kernel on every call.

## Tuning

The class step (16 bytes), the largest cached size (1024 bytes) and the bound
(256 KB) are compile-time constants at the top of `heap_cache.c`
(`HEAP_CACHE_STEP`, `HEAP_CACHE_CLASSES`, `HEAP_CACHE_MAX_BYTES`).
