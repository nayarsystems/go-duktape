// A per-heap cache of freed small blocks, under every heap this package
// creates (New, and NewWithFlags with or without tracking hooks).
//
// Why: a statically linked musl (the firmware's libc) uses mallocng, which
// hands a group of blocks back to the system as soon as the whole group is
// free. When the blocks a script allocates and frees in one call happen to
// fill and empty a group, every call pays an mmap, an munmap and the page
// faults in between — measured at about +55% per call on a bevents snippet, with
// the same instructions executed. Whether it happens depends on where the
// heap's allocations fall, so any change to any script can turn it on or off.
//
// The cache keeps freed small blocks on per-size free lists instead of
// returning them to malloc, so a call reuses what the previous one freed and
// the groups never empty. It is bounded: past its maximum (by default
// HEAP_CACHE_DEFAULT_MAX_BYTES, see Flags.HeapCacheMaxBytes), freed blocks go
// back to malloc as before. A heap can also be created without it, which
// makes these functions plain malloc, realloc and free.
//
// Each block carries a header with its size class, since duktape's free does
// not pass the size. Blocks larger than the biggest class (16 KB) are plain
// malloc blocks with a zero class. A heap is only ever used by one thread at a time
// (go-duktape callers serialize on the context's lock), so the cache has no
// lock of its own.

#include <stdint.h>
#include <stdlib.h>
#include <string.h>
#include "heap_cache.h"

// Size classes: multiples of 16 up to 1 KB, where most of a script's blocks
// fall, then powers of two up to 16 KB, for the arrays and strings a call
// may build and drop (a growing array, at 16 bytes per entry on 64-bit, 8 on
// armv7, passes 1 KB at 64 entries, or 128). Past 16 KB a block is not cached.
#define HEAP_CACHE_STEP 16
#define HEAP_CACHE_LINEAR_CLASSES 64 /* 16, 32, ... 1024 bytes */
#define HEAP_CACHE_LINEAR_MAX (HEAP_CACHE_STEP * HEAP_CACHE_LINEAR_CLASSES)
#define HEAP_CACHE_POW2_CLASSES 4 /* 2, 4, 8, 16 KB */
#define HEAP_CACHE_CLASSES (HEAP_CACHE_LINEAR_CLASSES + HEAP_CACHE_POW2_CLASSES)
#define HEAP_CACHE_MAX_SIZE (HEAP_CACHE_LINEAR_MAX << HEAP_CACHE_POW2_CLASSES)
// The bound counts the blocks' usable bytes (their size class), not their
// headers nor malloc's own bookkeeping: the memory actually held is somewhat
// more.
#define HEAP_CACHE_DEFAULT_MAX_BYTES (256 * 1024)

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
	int cached;
	size_t maxBytes;
	size_t cachedBytes;
	heapCacheFree *lists[HEAP_CACHE_CLASSES];
};

goHeapUdata *goHeapUdataNew(uint32_t id, long maxBytes) {
	goHeapUdata *u = calloc(1, sizeof(goHeapUdata));
	if (u != NULL) {
		u->id = id;
		u->cached = maxBytes >= 0;
		u->maxBytes = maxBytes > 0 ? (size_t)maxBytes : HEAP_CACHE_DEFAULT_MAX_BYTES;
	}
	return u;
}

int goHeapUdataCached(void *udata) {
	return ((goHeapUdata *)udata)->cached;
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
	if (cls <= HEAP_CACHE_LINEAR_CLASSES) {
		return (size_t)cls * HEAP_CACHE_STEP;
	}
	return (size_t)HEAP_CACHE_LINEAR_MAX << (cls - HEAP_CACHE_LINEAR_CLASSES);
}

// sizeClass is the smallest class a block of size bytes fits in, for
// 0 < size <= HEAP_CACHE_MAX_SIZE (a 0-byte request gets the smallest).
static uint32_t sizeClass(duk_size_t size) {
	if (size <= HEAP_CACHE_LINEAR_MAX) {
		return size == 0 ? 1 : (uint32_t)((size + HEAP_CACHE_STEP - 1) / HEAP_CACHE_STEP);
	}
	uint32_t cls = HEAP_CACHE_LINEAR_CLASSES + 1;
	while (classSize(cls) < size) {
		cls++;
	}
	return cls;
}

void *goHeapCacheAlloc(void *udata, duk_size_t size) {
	goHeapUdata *u = udata;
	if (!u->cached) {
		return malloc(size);
	}
	if (size > HEAP_CACHE_MAX_SIZE) {
		if (size > SIZE_MAX - HDR) {
			return NULL;
		}
		heapCacheHeader *h = malloc(HDR + size);
		if (h == NULL) {
			return NULL;
		}
		h->cls = 0;
		return (char *)h + HDR;
	}
	uint32_t cls = sizeClass(size);
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
	if (!u->cached) {
		free(ptr);
		return;
	}
	if (ptr == NULL) {
		return;
	}
	heapCacheHeader *h = (heapCacheHeader *)((char *)ptr - HDR);
	uint32_t cls = h->cls;
	if (cls == 0 || u->cachedBytes + classSize(cls) > u->maxBytes) {
		free(h);
		return;
	}
	heapCacheFree *f = ptr;
	f->next = u->lists[cls - 1];
	u->lists[cls - 1] = f;
	u->cachedBytes += classSize(cls);
}

void *goHeapCacheRealloc(void *udata, void *ptr, duk_size_t size) {
	if (!((goHeapUdata *)udata)->cached) {
		return realloc(ptr, size);
	}
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
		if (size > SIZE_MAX - HDR) {
			return NULL;
		}
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
