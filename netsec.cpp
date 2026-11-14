#include "netsec.hpp"

extern "C" {

NETSEC_EXPORT void netsec_compiler_barrier(void) noexcept {
    netsec::compiler_barrier();
}

NETSEC_EXPORT void netsec_speculation_barrier(void) noexcept {
    netsec::speculation_barrier();
}

NETSEC_EXPORT void netsec_memory_barrier(void) noexcept {
    netsec::memory_barrier();
}

NETSEC_EXPORT void netsec_full_barrier(void) noexcept {
    netsec::full_barrier();
}

NETSEC_EXPORT std::size_t netsec_mask_index(
    std::size_t index, std::size_t length) noexcept {
    return netsec::mask_index(index, length);
}

NETSEC_EXPORT void* netsec_mask_pointer(
    void* pointer, int valid, void* fallback) noexcept {
    return netsec::mask_pointer(pointer, valid != 0, fallback);
}

NETSEC_EXPORT std::uint8_t netsec_speculation_safe_load_u8(
    const std::uint8_t* base, std::size_t length, std::size_t index) noexcept {
    return netsec::speculation_safe_load(base, length, index);
}

NETSEC_EXPORT std::uint64_t netsec_speculation_safe_load_u64(
    const std::uint64_t* base, std::size_t length, std::size_t index) noexcept {
    return netsec::speculation_safe_load(base, length, index);
}

NETSEC_EXPORT void netsec_trust_boundary_enter(void) noexcept {
    netsec::enter_trust_boundary();
}

NETSEC_EXPORT void netsec_trust_boundary_leave(void) noexcept {
    netsec::leave_trust_boundary();
}

NETSEC_EXPORT std::uint32_t netsec_abi_version(void) noexcept {
    return 1;
}

}  // extern "C"
