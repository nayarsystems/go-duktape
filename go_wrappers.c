#include "go_wrappers.h"
#include "duktape.h"
#include "heap_cache.h"

duk_bool_t goWrapperExecTimeoutCheck(void *userData) {
  return goExecTimeoutCheck(goHeapUdataId(userData));
}

void goWrapperFatalErrorHandler(void *userData, const char *msg) {
  return goFatalErrorHandler(goHeapUdataId(userData), (char *)msg);
}

// See heap_cache.c for why the heap keeps its own cache of freed blocks.
// A heap without the cache uses duktape's default memory functions (plain
// malloc), exactly as before the cache existed; it still carries the udata,
// which the handlers need for the context id.
duk_context *goWrapperDukCreateHeap(uint32_t id, long cacheMaxBytes) {
  goHeapUdata *u = goHeapUdataNew(id, cacheMaxBytes);
  if (u == NULL) {
    return NULL;
  }
  duk_context *ctx;
  if (goHeapUdataCached(u)) {
    ctx = duk_create_heap(goHeapCacheAlloc, goHeapCacheRealloc, goHeapCacheFree,
                          u, NULL);
  } else {
    ctx = duk_create_heap(NULL, NULL, NULL, u, NULL);
  }
  if (ctx == NULL) {
    goHeapUdataFree(u);
  }
  return ctx;
}

#pragma GCC diagnostic push
#pragma GCC diagnostic ignored "-Wuse-after-free"

// The tracking hooks sit on top of the block cache, so a tracked heap
// allocates exactly as an untracked one does, and the hooks see what duktape
// allocates and frees (blocks kept in the cache count as freed).
static void *trackedMalloc(void *userData, size_t size) {
  void *ptr = goHeapCacheAlloc(userData, size);
  goTrackMalloc(goHeapUdataId(userData), ptr, size);
  return ptr;
}

static void *trackedRealloc(void *userData, void *ptr, size_t size) {
  void *nptr = goHeapCacheRealloc(userData, ptr, size);
  goTrackRealloc(goHeapUdataId(userData), ptr, nptr, size);
  return nptr;
}

static void trackedFree(void *userData, void *ptr) {
  goHeapCacheFree(userData, ptr);
  goTrackFree(goHeapUdataId(userData), ptr);
}

#pragma GCC diagnostic pop

duk_context *goWrapperDukCreateHeapWithHooks(uint32_t id, long cacheMaxBytes) {
  goHeapUdata *u = goHeapUdataNew(id, cacheMaxBytes);
  if (u == NULL) {
    return NULL;
  }
  duk_context *ctx =
      duk_create_heap(trackedMalloc, trackedRealloc, trackedFree, u, NULL);
  if (ctx == NULL) {
    goHeapUdataFree(u);
  }
  return ctx;
}

// The udata (and with it the cache) outlives duk_destroy_heap, which frees
// the heap's memory through it, so it is released afterwards.
void goWrapperDukDestroyHeap(duk_context *ctx) {
  duk_memory_functions funcs;
  duk_get_memory_functions(ctx, &funcs);
  duk_destroy_heap(ctx);
  goHeapUdataFree(funcs.udata);
}

size_t goWrapperDukHeapCacheBytes(duk_context *ctx) {
  duk_memory_functions funcs;
  duk_get_memory_functions(ctx, &funcs);
  return goHeapCacheCachedBytes(funcs.udata);
}
