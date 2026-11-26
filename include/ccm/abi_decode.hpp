// Alignment-free little-endian scalar codecs.  Attacker-controlled bytes are
// decoded with explicit shifts so there is never an unaligned access, trap, or
// reliance on host endianness.  All buffers live in enclave-private memory.
#ifndef CCM_ABI_DECODE_HPP
#define CCM_ABI_DECODE_HPP

#include "ccm/schema.hpp"
#include "ccm/types.hpp"

namespace ccm {

inline u16_t read_le16(const std::uint8_t* p) {
    return static_cast<u16_t>(
        static_cast<u16_t>(p[0]) |
        static_cast<u16_t>(static_cast<u16_t>(p[1]) << 8));
}

inline u32_t read_le32(const std::uint8_t* p) {
    return static_cast<u32_t>(p[0]) |
           static_cast<u32_t>(static_cast<u32_t>(p[1]) << 8) |
           static_cast<u32_t>(static_cast<u32_t>(p[2]) << 16) |
           static_cast<u32_t>(static_cast<u32_t>(p[3]) << 24);
}

inline u64_t read_le64(const std::uint8_t* p) {
    u64_t value = 0;
    for (unsigned shift = 0; shift < 64; shift += 8) {
        len_t index = static_cast<len_t>(shift / 8);
        value |= static_cast<u64_t>(p[index]) << shift;
    }
    return value;
}

inline void write_le16(std::uint8_t* p, u16_t value) {
    p[0] = static_cast<std::uint8_t>(value);
    p[1] = static_cast<std::uint8_t>(value >> 8);
}

inline void write_le32(std::uint8_t* p, u32_t value) {
    for (unsigned shift = 0; shift < 32; shift += 8) {
        p[static_cast<len_t>(shift / 8)] =
            static_cast<std::uint8_t>(value >> shift);
    }
}

inline void write_le64(std::uint8_t* p, u64_t value) {
    for (unsigned shift = 0; shift < 64; shift += 8) {
        p[static_cast<len_t>(shift / 8)] =
            static_cast<std::uint8_t>(value >> shift);
    }
}

inline u64_t decode_scalar(ScalarWidth width, const std::uint8_t* p) {
    switch (width) {
        case ScalarWidth::U8:
            return static_cast<u64_t>(p[0]);
        case ScalarWidth::U16:
            return static_cast<u64_t>(read_le16(p));
        case ScalarWidth::U32:
            return static_cast<u64_t>(read_le32(p));
        case ScalarWidth::U64:
            return read_le64(p);
    }
    return 0;
}

inline void encode_pointer(std::uint8_t* p, addr_t address) {
    write_le64(p, static_cast<u64_t>(address));
}

}  // namespace ccm

#endif  // CCM_ABI_DECODE_HPP
