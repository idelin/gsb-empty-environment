// CCM - Confidential Computing Marshaling library.
// Common fixed-width integer and address type aliases.
#ifndef CCM_TYPES_HPP
#define CCM_TYPES_HPP

#include <cstddef>
#include <cstdint>

namespace ccm {

// Numeric virtual address.  All pointer arithmetic in the library is performed
// on this unsigned integer type so that -Wsign-conversion/-Wconversion stay
// silent and pointer wrapping is defined behaviour.
using addr_t = std::uintptr_t;

// Byte length / offset type used across the ABI description and audit layer.
using len_t = std::size_t;

// ABI scalar width used by the modeled 64-bit little-endian interface.
using u64_t = std::uint64_t;
using u32_t = std::uint32_t;
using u16_t = std::uint16_t;
using u8_t = std::uint8_t;

}  // namespace ccm

#endif  // CCM_TYPES_HPP
