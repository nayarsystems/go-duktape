#ifndef HEAP_CACHE_H_
#define HEAP_CACHE_H_

#include <stdint.h>
#include "duktape.h"

// The udata every heap created by this package carries: the Go context id
// the exec timeout and fatal handlers look up, and the heap's block cache.
typedef struct goHeapUdata goHeapUdata;

goHeapUdata *goHeapUdataNew(uint32_t id);
uint32_t goHeapUdataId(void *udata);
void goHeapUdataFree(goHeapUdata *u);

// The memory functions of a cached heap (see heap_cache.c).
void *goHeapCacheAlloc(void *udata, duk_size_t size);
void *goHeapCacheRealloc(void *udata, void *ptr, duk_size_t size);
void goHeapCacheFree(void *udata, void *ptr);

// Bytes currently kept in the heap's cache, for tests.
size_t goHeapCacheCachedBytes(void *udata);

#endif
