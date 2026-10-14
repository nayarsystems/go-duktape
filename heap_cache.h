#ifndef HEAP_CACHE_H_
#define HEAP_CACHE_H_

#include <stdint.h>
#include "duktape.h"

// The udata every heap created by this package carries: the Go context id
// the exec timeout and fatal handlers look up, and the heap's block cache.
typedef struct goHeapUdata goHeapUdata;

// maxBytes bounds the cache: 0 for the default (HEAP_CACHE_DEFAULT_MAX_BYTES),
// a negative value for no cache at all, the memory functions then being plain
// malloc, realloc and free.
goHeapUdata *goHeapUdataNew(uint32_t id, long maxBytes);
int goHeapUdataCached(void *udata);
uint32_t goHeapUdataId(void *udata);
void goHeapUdataFree(goHeapUdata *u);

// The heap's memory functions (see heap_cache.c). With the cache disabled
// they are plain malloc, realloc and free (the tracked heaps still go through
// them).
void *goHeapCacheAlloc(void *udata, duk_size_t size);
void *goHeapCacheRealloc(void *udata, void *ptr, duk_size_t size);
void goHeapCacheFree(void *udata, void *ptr);

// Bytes currently kept in the heap's cache, for tests.
size_t goHeapCacheCachedBytes(void *udata);

#endif
