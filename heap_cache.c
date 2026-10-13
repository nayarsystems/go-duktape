// A per-heap cache of freed small blocks, under every heap this package
// creates (New, and NewWithFlags with or without tracking hooks).
//
// Why: a statically linked musl (the firmware's libc) uses mallocng, which
// hands a group of blocks back to the system as soon as the whole group is
// free. When the blocks a script allocates and frees in one call happen to
// fill and empty a group, every call pays an mmap, an munmap and the page
// faults in between — measured at +60% per call on a bevents snippet, with
// the same instructions executed. Whether it happens depends on where the
// heap's allocations fall, so any change to any script can turn it on or off.
//
// The cache keeps freed small blocks on per-size free lists instead of
// returning them to malloc, so a call reuses what the previous one freed and
// the groups never empty. It is bounded: past HEAP_CACHE_MAX_BYTES, freed
// blocks go back to malloc as before.
//
// Each block carries a header with its size class, since duktape's free does
// not pass the size. Blocks larger than the biggest class are plain malloc
// blocks with a zero class. A heap is only ever used by one thread at a time
// (go-duktape callers serialize on the context's lock), so the cache has no
// lock of its own.

#include <stdlib.h>
#include <string.h>
#include "heap_cache.h"

#define HEAP_CACHE_STEP 16
#define HEAP_CACHE_CLASSES 64 /* classes of 16, 32, ... 1024 bytes */
#define HEAP_CACHE_MAX_SIZE (HEAP_CACHE_STEP * HEAP_CACHE_CLASSES)
#define HEAP_CACHE_MAX_BYTES (256 * 1024)

// The header keeps the payload aligned as malloc's own blocks are.
// (max_align_t is C11; the package builds as C99, so the union spells out
// the types it stands for.)
typedef union {
	uint32_t cls; /* 1..HEAP_CACHE_CLASSES, or 0 for a plain malloc block */
	long double ld;
	long long ll;
	void *p;
} heapCacheHeader;

#define HDR sizeof(heapCacheHeader)

typedef struct heapCacheFree {
	struct heapCacheFree *next;
} heapCacheFree;

struct goHeapUdata {
	uint32_t id;
	size_t cachedBytes;
	heapCacheFree *lists[HEAP_CACHE_CLASSES];
};

goHeapUdata *goHeapUdataNew(uint32_t id) {
	goHeapUdata *u = calloc(1, sizeof(goHeapUdata));
	if (u != NULL) {
		u->id = id;
	}
	return u;
}

uint32_t goHeapUdataId(void *udata) {
	return ((goHeapUdata *)udata)->id;
}

void goHeapUdataFree(goHeapUdata *u) {
	if (u == NULL) {
		return;
	}
	for (int c = 0; c < HEAP_CACHE_CLASSES; c++) {
		heapCacheFree *f = u->lists[c];
		while (f != NULL) {
			heapCacheFree *next = f->next;
			free((char *)f - HDR);
			f = next;
		}
	}
	free(u);
}

size_t goHeapCacheCachedBytes(void *udata) {
	return ((goHeapUdata *)udata)->cachedBytes;
}

static size_t classSize(uint32_t cls) {
	return (size_t)cls * HEAP_CACHE_STEP;
}

void *goHeapCacheAlloc(void *udata, duk_size_t size) {
	goHeapUdata *u = udata;
	if (size > HEAP_CACHE_MAX_SIZE) {
		heapCacheHeader *h = malloc(HDR + size);
		if (h == NULL) {
			return NULL;
		}
		h->cls = 0;
		return (char *)h + HDR;
	}
	uint32_t cls = size == 0 ? 1 : (uint32_t)((size + HEAP_CACHE_STEP - 1) / HEAP_CACHE_STEP);
	heapCacheFree *f = u->lists[cls - 1];
	if (f != NULL) {
		u->lists[cls - 1] = f->next;
		u->cachedBytes -= classSize(cls);
		return f;
	}
	heapCacheHeader *h = malloc(HDR + classSize(cls));
	if (h == NULL) {
		return NULL;
	}
	h->cls = cls;
	return (char *)h + HDR;
}

void goHeapCacheFree(void *udata, void *ptr) {
	goHeapUdata *u = udata;
	if (ptr == NULL) {
		return;
	}
	heapCacheHeader *h = (heapCacheHeader *)((char *)ptr - HDR);
	uint32_t cls = h->cls;
	if (cls == 0 || u->cachedBytes + classSize(cls) > HEAP_CACHE_MAX_BYTES) {
		free(h);
		return;
	}
	heapCacheFree *f = ptr;
	f->next = u->lists[cls - 1];
	u->lists[cls - 1] = f;
	u->cachedBytes += classSize(cls);
}

void *goHeapCacheRealloc(void *udata, void *ptr, duk_size_t size) {
	if (ptr == NULL) {
		return goHeapCacheAlloc(udata, size);
	}
	if (size == 0) {
		goHeapCacheFree(udata, ptr);
		return NULL;
	}
	heapCacheHeader *h = (heapCacheHeader *)((char *)ptr - HDR);
	if (h->cls == 0) {
		// A plain block stays plain, whatever its new size.
		heapCacheHeader *nh = realloc(h, HDR + size);
		if (nh == NULL) {
			return NULL;
		}
		return (char *)nh + HDR;
	}
	size_t have = classSize(h->cls);
	if (size <= have) {
		return ptr;
	}
	void *nptr = goHeapCacheAlloc(udata, size);
	if (nptr == NULL) {
		return NULL;
	}
	memcpy(nptr, ptr, have);
	goHeapCacheFree(udata, ptr);
	return nptr;
}
