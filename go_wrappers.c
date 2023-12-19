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