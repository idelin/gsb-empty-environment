#ifndef NETSEC_H
#define NETSEC_H

/*
 * netsec: software object-tag propagation runtime.
 *
 * This is the stable, importable C ABI used by instrumented C++20 runtimes.
 * A pointer is paired with the tag and bounds of the object it was derived
 * from.  The tag must survive pointer arithmetic and conversions, and every
 * access is revalidated against the object registry.
 */

#include <stddef.h>
#include <stdint.h>

#if defined(_WIN32)
#  if defined(NETSEC_BUILDING)
#    define NETSEC_API __declspec(dllexport)
#  else
#    define NETSEC_API __declspec(dllimport)
#  endif
#elif defined(__GNUC__) || defined(__clang__)
#  define NETSEC_API __attribute__((visibility("default")))
#else
#  define NETSEC_API
#endif

#ifdef __cplusplus
extern "C" {
#endif

enum netsec_access_kind {
    NETSEC_ACCESS_READ = 0,
    NETSEC_ACCESS_WRITE = 1,
    NETSEC_ACCESS_READ_WRITE = 2,
    NETSEC_ACCESS_SYSCALL_READ = 3,
    NETSEC_ACCESS_SYSCALL_WRITE = 4
};

enum netsec_trap_reason {
    NETSEC_TRAP_NONE = 0,
    NETSEC_TRAP_UNTAGGED = 1,
    NETSEC_TRAP_STALE_OBJECT = 2,
    NETSEC_TRAP_OUT_OF_BOUNDS = 3,
    NETSEC_TRAP_TAG_MISMATCH = 4,
    NETSEC_TRAP_UNREGISTERED = 5,
    NETSEC_TRAP_ARITHMETIC_OVERFLOW = 6,
    NETSEC_TRAP_PROVENANCE_MISMATCH = 7,
    NETSEC_TRAP_INVALID_ARGUMENT = 8
};

typedef struct netsec_object {
    uint64_t tag;
    void *base;
    size_t size;
    uint64_t generation;
} netsec_object;

/*
 * Self-describing pointer descriptor.  address may move, but tag,
 * object_base, object_size and generation remain bound to the object.
 */
typedef struct netsec_ptr {
    uintptr_t address;
    uintptr_t object_base;
    size_t object_size;
    uint64_t tag;
    uint64_t generation;
    uint32_t tagged;
    uint32_t reserved;
} netsec_ptr;

typedef void (*netsec_trap_handler)(int reason,
                                    uintptr_t address,
                                    size_t bytes,
                                    uint64_t expected_tag,
                                    uint64_t actual_tag,
                                    const char *operation);

/* Registration and lifetime. */
NETSEC_API netsec_object netsec_register_object(void *base, size_t size);
NETSEC_API int netsec_unregister_object(uint64_t tag);
NETSEC_API int netsec_object_alive(uint64_t tag);
NETSEC_API netsec_object netsec_object_for_tag(uint64_t tag);

/* Descriptor construction and propagation. */
NETSEC_API netsec_ptr netsec_ptr_from_object(netsec_object object);
NETSEC_API netsec_ptr netsec_ptr_from_object_at(netsec_object object,
                                                size_t byte_offset);
NETSEC_API netsec_ptr netsec_ptr_from_address(const void *address,
                                              size_t bytes);
NETSEC_API netsec_ptr netsec_ptr_untagged(void);
NETSEC_API netsec_ptr netsec_ptr_add(netsec_ptr pointer, ptrdiff_t byte_offset);
NETSEC_API netsec_ptr netsec_ptr_sub(netsec_ptr pointer, ptrdiff_t byte_offset);
NETSEC_API netsec_ptr netsec_ptr_add_checked(netsec_ptr pointer,
                                             ptrdiff_t byte_offset);
NETSEC_API netsec_ptr netsec_ptr_sub_checked(netsec_ptr pointer,
                                             ptrdiff_t byte_offset);
NETSEC_API ptrdiff_t netsec_ptr_distance(netsec_ptr left, netsec_ptr right);
NETSEC_API netsec_ptr netsec_ptr_convert(netsec_ptr pointer,
                                         size_t old_element_size,
                                         size_t new_element_size);
NETSEC_API netsec_ptr netsec_ptr_strip(netsec_ptr pointer);
NETSEC_API netsec_ptr netsec_ptr_reassociate(netsec_ptr raw,
                                             uint64_t tag,
                                             size_t bytes);

/* Non-trapping validation and trapping enforcement. */
NETSEC_API int netsec_ptr_check(netsec_ptr pointer, size_t bytes);
NETSEC_API void netsec_ptr_require(netsec_ptr pointer,
                                   size_t bytes,
                                   int access);
NETSEC_API int netsec_bounds_check(netsec_ptr pointer, size_t bytes);
NETSEC_API void netsec_bounds_require(netsec_ptr pointer, size_t bytes);
NETSEC_API uint64_t netsec_tag_at(const void *address, size_t bytes);

/* Syscall boundary: user descriptors are checked again at entry. */
NETSEC_API void netsec_syscall_revalidate(netsec_ptr pointer,
                                          size_t bytes,
                                          int kernel_writes);
NETSEC_API void *netsec_syscall_map(netsec_ptr pointer,
                                    size_t bytes,
                                    int kernel_writes);
NETSEC_API void netsec_copy_from_user(void *kernel_destination,
                                      netsec_ptr user_source,
                                      size_t bytes);
NETSEC_API void netsec_copy_to_user(netsec_ptr user_destination,
                                    const void *kernel_source,
                                    size_t bytes);
NETSEC_API uint64_t netsec_syscall_validation_count(void);

/* Raise a trap from compiler-generated instrumentation. */
NETSEC_API void netsec_raise_trap(int reason,
                                  uintptr_t address,
                                  size_t bytes,
                                  uint64_t expected_tag,
                                  uint64_t actual_tag,
                                  const char *operation);

/* Trap policy.  The handler runs before the runtime raises its trap. */
NETSEC_API void netsec_set_trap_handler(netsec_trap_handler handler);
NETSEC_API netsec_trap_handler netsec_get_trap_handler(void);
NETSEC_API const char *netsec_reason_name(int reason);

#ifdef __cplusplus
}
#endif

#endif /* NETSEC_H */
