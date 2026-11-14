#pragma once

// netsec - importable speculation-barrier primitives for C++20 runtimes.
//
// The component separates compiler ordering fences from architecture
// speculation barriers.  A compiler fence can only stop the compiler from
// moving work across the fence; it cannot stop a CPU from executing a
// mispredicted load.  An architecture barrier is required in front of a load
// whose address is derived from an attacker-controlled value.

#include <array>
#include <atomic>
#include <concepts>
#include <cstddef>
#include <cstdint>
#include <functional>
#include <optional>
#include <span>
#include <type_traits>
#include <utility>

#if defined(_MSC_VER)
#  include <intrin.h>
#endif

#if defined(__GNUC__) || defined(__clang__)
#  define NETSEC_ALWAYS_INLINE inline __attribute__((always_inline))
#  define NETSEC_NOINLINE __attribute__((noinline))
#  define NETSEC_EXPORT __attribute__((visibility("default")))
#else
#  define NETSEC_ALWAYS_INLINE inline
#  define NETSEC_NOINLINE
#  define NETSEC_EXPORT
#endif

#define NETSEC_COMPILER_BARRIER() ::netsec::compiler_barrier()
#define NETSEC_SPECULATION_BARRIER() ::netsec::speculation_barrier()
#define NETSEC_ARCH_BARRIER() ::netsec::speculation_barrier()
#define NETSEC_TRUST_BOUNDARY_ENTER() ::netsec::enter_trust_boundary()
#define NETSEC_TRUST_BOUNDARY_EXIT() ::netsec::leave_trust_boundary()
#define NETSEC_SAFE_LOAD(base, length, index) \
    ::netsec::speculation_safe_load((base), (length), (index))
#define NETSEC_TRUST_BOUNDARY NETSEC_NOINLINE

namespace netsec {

// -------------------------------------------------------------------------
// Compiler barriers
// -------------------------------------------------------------------------

// Prevent the compiler from moving memory operations across this point.  This
// intentionally emits no machine instruction and does not block CPU
// speculation.
NETSEC_ALWAYS_INLINE void compiler_barrier() noexcept {
#if defined(__GNUC__) || defined(__clang__)
    __asm__ __volatile__("" ::: "memory");
#else
    std::atomic_signal_fence(std::memory_order_seq_cst);
#endif
}

NETSEC_ALWAYS_INLINE void compiler_fence() noexcept {
    compiler_barrier();
}

NETSEC_ALWAYS_INLINE void compiler_only_barrier() noexcept {
    compiler_barrier();
}

// -------------------------------------------------------------------------
// Architecture speculation barriers
// -------------------------------------------------------------------------

// Stop speculative execution from crossing this point.  The exact instruction
// is target dependent:
//   x86/x86-64 : LFENCE
//   AArch64    : CSDB
//   ARMv8-A32  : CSDB (ISB on older ARM cores)
//   RISC-V     : FENCE + FENCE.I
//   POWER      : ISYNC
//   s390x      : BCR 15,0
//
// This is a speculation barrier, not a general-purpose memory-ordering fence.
NETSEC_ALWAYS_INLINE void speculation_barrier() noexcept {
#if defined(__x86_64__) || defined(__i386__) || defined(_M_X64) || defined(_M_IX86)
#  if defined(_MSC_VER)
    _mm_lfence();
#  else
    __asm__ __volatile__("lfence" ::: "memory");
#  endif
#elif defined(__aarch64__) || defined(_M_ARM64)
#  if defined(_MSC_VER)
    __isb(0xF);
#  else
    __asm__ __volatile__("csdb" ::: "memory");
#  endif
#elif defined(__arm__) || defined(_M_ARM)
#  if defined(__ARM_ARCH) && (__ARM_ARCH >= 8)
    __asm__ __volatile__("csdb" ::: "memory");
#  else
    __asm__ __volatile__("isb" ::: "memory");
#  endif
#elif defined(__riscv)
    __asm__ __volatile__("fence iorw, iorw\n\tfence.i" ::: "memory");
#elif defined(__powerpc__) || defined(__powerpc64__) || defined(__PPC__)
    __asm__ __volatile__("isync" ::: "memory");
#elif defined(__s390x__) || defined(__s390__)
    __asm__ __volatile__("bcr 15,0" ::: "memory");
#elif defined(__mips__)
    __asm__ __volatile__("sync" ::: "memory");
#else
    // Portable fallback: correct compiler ordering, no architecture guarantee.
    std::atomic_signal_fence(std::memory_order_seq_cst);
#endif
}

NETSEC_ALWAYS_INLINE void architectural_barrier() noexcept {
    speculation_barrier();
}

NETSEC_ALWAYS_INLINE void arch_barrier() noexcept {
    speculation_barrier();
}

NETSEC_ALWAYS_INLINE void speculation_fence() noexcept {
    speculation_barrier();
}

NETSEC_ALWAYS_INLINE void architectural_speculation_barrier() noexcept {
    speculation_barrier();
}

// General memory-ordering fence.  Unlike speculation_barrier(), this orders
// memory operations and is intended for publisher/consumer hand-off.
NETSEC_ALWAYS_INLINE void memory_barrier() noexcept {
    compiler_barrier();
#if defined(__x86_64__) || defined(__i386__) || defined(_M_X64) || defined(_M_IX86)
#  if defined(_MSC_VER)
    _mm_mfence();
#  else
    __asm__ __volatile__("mfence" ::: "memory");
#  endif
#elif defined(__aarch64__) || defined(_M_ARM64)
#  if defined(_MSC_VER)
    __dmb(0xF);
#  else
    __asm__ __volatile__("dmb ish" ::: "memory");
#  endif
#elif defined(__arm__) || defined(_M_ARM)
    __asm__ __volatile__("dmb ish" ::: "memory");
#elif defined(__riscv)
    __asm__ __volatile__("fence iorw, iorw" ::: "memory");
#elif defined(__powerpc__) || defined(__powerpc64__) || defined(__PPC__)
    __asm__ __volatile__("sync" ::: "memory");
#elif defined(__s390x__) || defined(__s390__)
    __asm__ __volatile__("bcr 15,0" ::: "memory");
#elif defined(__mips__)
    __asm__ __volatile__("sync" ::: "memory");
#else
    std::atomic_thread_fence(std::memory_order_seq_cst);
#endif
    compiler_barrier();
}

NETSEC_ALWAYS_INLINE void full_barrier() noexcept {
    compiler_barrier();
    speculation_barrier();
    memory_barrier();
}

// -------------------------------------------------------------------------
// Branchless masking helpers
// -------------------------------------------------------------------------

// Return a mask filled with ones when condition is true and zeros otherwise.
// The result is computed arithmetically so that it does not introduce a
// secret-dependent branch.
template <std::integral T>
    requires(!std::same_as<std::remove_cv_t<T>, bool>)
[[nodiscard]] constexpr T mask_from_bool(bool condition) noexcept {
    using U = std::make_unsigned_t<T>;
    return static_cast<T>(static_cast<U>(0) - static_cast<U>(condition ? 1u : 0u));
}

// Mask an attacker-controlled index.  In-range indices are preserved; an
// out-of-range index becomes zero.  The conditional move is written as
// arithmetic (not as an if statement) so a mispredicted branch cannot make the
// masked value attacker controlled.
template <std::integral T>
[[nodiscard]] constexpr std::size_t mask_index(T index, std::size_t length) noexcept {
    using U = std::make_unsigned_t<T>;
    const U widened_index = static_cast<U>(index);
    const U widened_length = static_cast<U>(length);
    const U in_bounds = (widened_index < widened_length) ? U{1} : U{0};
    const U mask = static_cast<U>(0) - in_bounds;
    return static_cast<std::size_t>(widened_index & mask);
}

template <std::integral T>
[[nodiscard]] constexpr std::size_t masked_index(T index, std::size_t length) noexcept {
    return mask_index(index, length);
}

// Mask a pointer to a safe fallback when its validity predicate is false.
// Both the pointer and the fallback are combined with the same mask, so the
// select has no data-dependent branch.
template <class T>
[[nodiscard]] NETSEC_ALWAYS_INLINE T* mask_pointer(
    T* pointer, bool valid, T* fallback = nullptr) noexcept {
    const std::uintptr_t pointer_bits = reinterpret_cast<std::uintptr_t>(pointer);
    const std::uintptr_t fallback_bits = reinterpret_cast<std::uintptr_t>(fallback);
    const std::uintptr_t mask =
        static_cast<std::uintptr_t>(0) - static_cast<std::uintptr_t>(valid ? 1u : 0u);
    return reinterpret_cast<T*>((pointer_bits & mask) | (fallback_bits & ~mask));
}

template <class T>
[[nodiscard]] NETSEC_ALWAYS_INLINE T* mask_pointer(
    T* pointer, std::size_t index, std::size_t length, T* fallback = nullptr) noexcept {
    return mask_pointer(pointer, index < length, fallback);
}

template <class T>
[[nodiscard]] NETSEC_ALWAYS_INLINE T* masked_pointer(
    T* pointer, bool valid, T* fallback = nullptr) noexcept {
    return mask_pointer(pointer, valid, fallback);
}

template <class T>
[[nodiscard]] NETSEC_ALWAYS_INLINE T* mask_ptr(
    T* pointer, bool valid, T* fallback = nullptr) noexcept {
    return mask_pointer(pointer, valid, fallback);
}

// Keep only the selected address bits.  This is useful when a runtime wants to
// force a pointer into a permitted address window before dereferencing it.
template <class T>
[[nodiscard]] NETSEC_ALWAYS_INLINE T* mask_pointer_bits(
    T* pointer, std::uintptr_t keep_mask) noexcept {
    return reinterpret_cast<T*>(reinterpret_cast<std::uintptr_t>(pointer) & keep_mask);
}

// -------------------------------------------------------------------------
// Bounds-checked loads
// -------------------------------------------------------------------------

namespace detail {

// Opaque values prevent the optimizer from proving that the masked index is
// equivalent to the original index and then re-materializing a conditional
// load.  This is important: a mitigation that the compiler folds back into
// `if (in_bounds) base[index]` is not a mitigation.
[[nodiscard]] NETSEC_ALWAYS_INLINE std::size_t opaque_size(std::size_t value) noexcept {
#if defined(__GNUC__) || defined(__clang__)
    __asm__ __volatile__("" : "+r"(value));
    return value;
#else
    volatile std::size_t copy = value;
    return copy;
#endif
}

template <class T>
[[nodiscard]] NETSEC_ALWAYS_INLINE T* opaque_pointer(T* value) noexcept {
    std::uintptr_t bits = reinterpret_cast<std::uintptr_t>(value);
#if defined(__GNUC__) || defined(__clang__)
    __asm__ __volatile__("" : "+r"(bits));
#else
    volatile std::uintptr_t copy = bits;
    bits = copy;
#endif
    return reinterpret_cast<T*>(bits);
}

}  // namespace detail

// Bounds-checked load with a masked index and a masked base pointer.
//
// Contract:
//   * index < length  -> returns base[index]
//   * index >= length -> returns T{} without exposing out-of-bounds bytes
//   * null base or zero length also returns T{}
//
// The load address is computed from both masks and is opaque to the optimizer.
// The architecture speculation barrier sits between the mask computation and
// the load, matching the "bounds check, barrier, load" gadget shape.
template <class T>
[[nodiscard]] T speculation_safe_load(
    const T* base, std::size_t length, std::size_t index) noexcept {
    static_assert(
        std::is_trivially_copyable_v<T>,
        "speculation_safe_load requires a trivially copyable element type");

    static const T fallback{};
    const bool in_bounds = index < length;
    const bool usable = in_bounds && (base != nullptr);
    const T* safe_base = mask_pointer(base, usable, &fallback);
    const std::size_t safe_index = mask_index(index, length);

    // Keep the masked operands opaque so the compiler cannot recover an
    // unmasked conditional load.  Then stop speculation before the load.
    safe_base = detail::opaque_pointer(safe_base);
    const std::size_t load_index = detail::opaque_size(safe_index);
    speculation_barrier();

    const T value = safe_base[load_index];
    compiler_barrier();

    return in_bounds ? value : T{};
}

template <class T, std::size_t N>
[[nodiscard]] T speculation_safe_load(
    const T (&array)[N], std::size_t index) noexcept {
    return speculation_safe_load(static_cast<const T*>(array), N, index);
}

template <class T, std::size_t N>
[[nodiscard]] T speculation_safe_load(
    const std::array<T, N>& array, std::size_t index) noexcept {
    return speculation_safe_load(array.data(), N, index);
}

template <class T>
[[nodiscard]] T speculation_safe_load(
    std::span<const T> values, std::size_t index) noexcept {
    return speculation_safe_load(values.data(), values.size(), index);
}

template <class T, class Fallback>
    requires std::convertible_to<Fallback, T>
[[nodiscard]] T speculation_safe_load(
    const T* base,
    std::size_t length,
    std::size_t index,
    Fallback fallback_value) noexcept {
    static_assert(
        std::is_trivially_copyable_v<T>,
        "speculation_safe_load requires a trivially copyable element type");

    static const T fallback{};
    const bool in_bounds = index < length;
    const bool usable = in_bounds && (base != nullptr);
    const T* safe_base = detail::opaque_pointer(mask_pointer(base, usable, &fallback));
    const std::size_t load_index =
        detail::opaque_size(mask_index(index, length));
    speculation_barrier();

    const T value = safe_base[load_index];
    compiler_barrier();
    return in_bounds ? value : static_cast<T>(fallback_value);
}

// Optional-returning variant.  The branch may be mispredicted, but the fallback
// load still uses the masked address and cannot expose the out-of-range element.
template <class T>
[[nodiscard]] std::optional<T> speculation_safe_load_optional(
    const T* base, std::size_t length, std::size_t index) noexcept {
    if (base == nullptr || index >= length) {
        // Touch the safe load so the same masked gadget is present on this
        // path as well; it will not access the out-of-range element.
        (void)speculation_safe_load(base, length, index);
        return std::nullopt;
    }
    return speculation_safe_load(base, length, index);
}

template <class T, std::size_t N>
[[nodiscard]] std::optional<T> speculation_safe_load_optional(
    const std::array<T, N>& array, std::size_t index) noexcept {
    return speculation_safe_load_optional(array.data(), N, index);
}

// Common aliases used by runtimes and security wrappers.
template <class T>
[[nodiscard]] T safe_load(
    const T* base, std::size_t length, std::size_t index) noexcept {
    return speculation_safe_load(base, length, index);
}

template <class T>
[[nodiscard]] T secure_load(
    const T* base, std::size_t length, std::size_t index) noexcept {
    return speculation_safe_load(base, length, index);
}

template <class T>
[[nodiscard]] T mitigated_load(
    const T* base, std::size_t length, std::size_t index) noexcept {
    return speculation_safe_load(base, length, index);
}

template <class T>
[[nodiscard]] T safe_array_load(
    const T* base, std::size_t length, std::size_t index) noexcept {
    return speculation_safe_load(base, length, index);
}

// -------------------------------------------------------------------------
// Trust-boundary barriers
// -------------------------------------------------------------------------

// Place on functions that receive untrusted state or return across a trust
// boundary.  The entry/exit helpers, or the RAII guard below, should be used
// inside the function body as well.
NETSEC_ALWAYS_INLINE void enter_trust_boundary() noexcept {
    compiler_barrier();
    speculation_barrier();
    compiler_barrier();
}

NETSEC_ALWAYS_INLINE void leave_trust_boundary() noexcept {
    compiler_barrier();
    speculation_barrier();
    memory_barrier();
    compiler_barrier();
}

NETSEC_ALWAYS_INLINE void trust_boundary_enter() noexcept {
    enter_trust_boundary();
}

NETSEC_ALWAYS_INLINE void trust_boundary_exit() noexcept {
    leave_trust_boundary();
}

class TrustBoundary final {
public:
    TrustBoundary() noexcept { enter_trust_boundary(); }
    ~TrustBoundary() noexcept { leave_trust_boundary(); }

    TrustBoundary(const TrustBoundary&) = delete;
    TrustBoundary& operator=(const TrustBoundary&) = delete;
    TrustBoundary(TrustBoundary&&) = delete;
    TrustBoundary& operator=(TrustBoundary&&) = delete;
};

using trust_boundary_guard = TrustBoundary;
using speculation_barrier_guard = TrustBoundary;

// Enter the boundary, call the supplied callable, and leave the boundary even
// when the callable exits through an exception.
template <class Callable, class... Args>
decltype(auto) call_across_trust_boundary(Callable&& callable, Args&&... args) noexcept(
    noexcept(std::invoke(std::forward<Callable>(callable), std::forward<Args>(args)...))) {
    TrustBoundary guard;
    return std::invoke(
        std::forward<Callable>(callable), std::forward<Args>(args)...);
}

template <class Callable, class... Args>
decltype(auto) across_trust_boundary(Callable&& callable, Args&&... args) noexcept(
    noexcept(std::invoke(std::forward<Callable>(callable), std::forward<Args>(args)...))) {
    return call_across_trust_boundary(
        std::forward<Callable>(callable), std::forward<Args>(args)...);
}

template <class Callable, class... Args>
decltype(auto) trust_boundary(Callable&& callable, Args&&... args) noexcept(
    noexcept(std::invoke(std::forward<Callable>(callable), std::forward<Args>(args)...))) {
    return call_across_trust_boundary(
        std::forward<Callable>(callable), std::forward<Args>(args)...);
}

}  // namespace netsec

// -------------------------------------------------------------------------
// Importable C ABI
// -------------------------------------------------------------------------
//
// A C++20 runtime can either include this header and use the inline API, or
// link the netsec library and import the stable C symbols below.

extern "C" {

NETSEC_EXPORT void netsec_compiler_barrier(void) noexcept;
NETSEC_EXPORT void netsec_speculation_barrier(void) noexcept;
NETSEC_EXPORT void netsec_memory_barrier(void) noexcept;
NETSEC_EXPORT void netsec_full_barrier(void) noexcept;

NETSEC_EXPORT std::size_t netsec_mask_index(
    std::size_t index, std::size_t length) noexcept;
NETSEC_EXPORT void* netsec_mask_pointer(
    void* pointer, int valid, void* fallback) noexcept;

NETSEC_EXPORT std::uint8_t netsec_speculation_safe_load_u8(
    const std::uint8_t* base, std::size_t length, std::size_t index) noexcept;
NETSEC_EXPORT std::uint64_t netsec_speculation_safe_load_u64(
    const std::uint64_t* base, std::size_t length, std::size_t index) noexcept;

NETSEC_EXPORT void netsec_trust_boundary_enter(void) noexcept;
NETSEC_EXPORT void netsec_trust_boundary_leave(void) noexcept;

NETSEC_EXPORT std::uint32_t netsec_abi_version(void) noexcept;

}  // extern "C"
