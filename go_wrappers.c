#include "go_wrappers.h"
#include "duktape.h"

duk_bool_t goWrapperExecTimeoutCheck(void *userData) {
  return goExecTimeoutCheck((uint32_t)(uintptr_t)userData);
}

void goWrapperFatalErrorHandler(void *userData, const char *msg) {
  return goFatalErrorHandler((uint32_t)(uintptr_t)userData, (char *)msg);
}

duk_context *goWrapperDukCreateHeap(uint32_t id) {
  return duk_create_heap(NULL, NULL, NULL, (void *)(uintptr_t)id, NULL);
}

#pragma GCC diagnostic push
#pragma GCC diagnostic ignored "-Wuse-after-free"

static void *trackedMalloc(void *userData, size_t size) {
  void *ptr = malloc(size);
  goTrackMalloc((uint32_t)(uintptr_t)userData, ptr, size);
  return ptr;
}

static void *trackedRealloc(void *userData, void *ptr, size_t size) {
  void *nptr = realloc(ptr, size);
  goTrackRealloc((uint32_t)(uintptr_t)userData, ptr, nptr, size);
  return nptr;
}

static void trackedFree(void *userData, void *ptr) {
  free(ptr);
  goTrackFree((uint32_t)(uintptr_t)userData, ptr);
}

#pragma GCC diagnostic pop

duk_context *goWrapperDukCreateHeapWithHooks(uint32_t id) {
  void *udata = (void *)(uintptr_t)id;
  return duk_create_heap(trackedMalloc, trackedRealloc, trackedFree, udata,
                         NULL);
}