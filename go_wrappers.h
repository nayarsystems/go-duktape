
#ifndef GO_WRAPPERS_H_
#define GO_WRAPPERS_H_

#include "duktape.h"

extern duk_bool_t goExecTimeoutCheck(uint32_t ctxId);
extern void goFatalErrorHandler(uint32_t ctxId, char *msg);

duk_bool_t goWrapperExecTimeoutCheck(void *userData);
void goWrapperFatalErrorHandler(void *userData, const char *msg);
duk_context *goWrapperDukCreateHeap(uint32_t id);

#endif