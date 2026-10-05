#pragma once

// Core bit-level primitives for binrw.
//
// Bit numbering convention (host-endianness independent):
//   Absolute bit position p refers to byte p/8, bit p%8, where bit 0 is the
//   LEAST significant bit of that byte. All multi-byte quantities are
//   assembled from individual bytes with explicit shifts, never by
//   memcpy/reinterpret_cast into native integers, so results are identical
//   on little- and big-endian hosts and no unaligned access ever occurs.
//
// Field semantics for a field of `width` bits starting at `bit_offset`:
//   - Little: bit i of the value (i = 0 is LSB) lives at absolute position
//     bit_offset + i.
//   - Big: the value's MSB lives at absolute position bit_offset; bit
//     significance decreases as the absolute position increases.

#include <cstddef>
#include <cstdint>
#include <stdexcept>
#include <string>

namespace binrw {

class Error : public std::runtime_error {
public:
    explicit Error(const std::string& msg) : std::runtime_error(msg) {}
};

enum class ByteOrder { Little, Big };
enum class Signedness { Unsigned, Signed };

// ---------------------------------------------------------------------------
// Checked arithmetic: every length computation that can overflow goes
// through these helpers and raises binrw::Error instead of wrapping.
// ---------------------------------------------------------------------------

inline uint64_t checked_add(uint64_t a, uint64_t b, const char* what) {
    if (b > UINT64_MAX - a) {
        throw Error(std::string("overflow in ") + what);
    }
    return a + b;
}

inline uint64_t checked_mul(uint64_t a, uint64_t b, const char* what) {
    if (a != 0 && b > UINT64_MAX / a) {
        throw Error(std::string("overflow in ") + what);
    }
    return a * b;
}

// Number of bytes needed to hold `bits` bits, with overflow check.
inline uint64_t bits_to_bytes(uint64_t bits) {
    if (bits > UINT64_MAX - 7) {
        throw Error("overflow in bits_to_bytes");
    }
    return (bits + 7) / 8;
}

// ---------------------------------------------------------------------------
// Sign extension: widen the low `width` bits of `raw` (1..64) to int64_t.
// ---------------------------------------------------------------------------

inline int64_t sign_extend(uint64_t raw, unsigned width) {
    if (width == 0 || width > 64) {
        throw Error("sign_extend: width out of range");
    }
    if (width == 64) {
        return static_cast<int64_t>(raw);
    }
    const uint64_t sign_bit = 1ULL << (width - 1);
    if ((raw & sign_bit) == 0) {
        return static_cast<int64_t>(raw);
    }
    const uint64_t extended = raw | (~0ULL << width);
    return static_cast<int64_t>(extended);
}

// Truncate `value` to `width` bits (used when writing signed values).
inline uint64_t truncate_bits(uint64_t value, unsigned width) {
    if (width >= 64) {
        return value;
    }
    return value & ((1ULL << width) - 1);
}

// ---------------------------------------------------------------------------
// Bit-level access on a byte buffer.
// ---------------------------------------------------------------------------

inline uint8_t get_bit(const uint8_t* data, uint64_t pos) {
    return static_cast<uint8_t>((data[pos >> 3] >> (pos & 7u)) & 1u);
}

inline void set_bit(uint8_t* data, uint64_t pos, unsigned bit) {
    const uint64_t byte_index = pos >> 3;
    const unsigned shift = static_cast<unsigned>(pos & 7u);
    const uint8_t mask = static_cast<uint8_t>(1u << shift);
    data[byte_index] = static_cast<uint8_t>((data[byte_index] & ~mask) |
                                            ((bit & 1u) ? mask : 0));
}

// Reads `width` bits (1..64) starting at absolute bit offset `bit_offset`.
// The caller must guarantee the range lies inside the buffer.
inline uint64_t read_bits(const uint8_t* data, uint64_t bit_offset,
                          unsigned width, ByteOrder order) {
    uint64_t value = 0;
    if (order == ByteOrder::Little) {
        for (unsigned i = 0; i < width; ++i) {
            value |= static_cast<uint64_t>(get_bit(data, bit_offset + i)) << i;
        }
    } else {
        for (unsigned i = 0; i < width; ++i) {
            value = (value << 1) | get_bit(data, bit_offset + i);
        }
    }
    return value;
}

// Writes the low `width` bits of `value` starting at `bit_offset`.
// Bits of `value` beyond `width` are ignored.
inline void write_bits(uint8_t* data, uint64_t bit_offset, unsigned width,
                       ByteOrder order, uint64_t value) {
    if (order == ByteOrder::Little) {
        for (unsigned i = 0; i < width; ++i) {
            set_bit(data, bit_offset + i,
                    static_cast<unsigned>((value >> i) & 1u));
        }
    } else {
        for (unsigned i = 0; i < width; ++i) {
            set_bit(data, bit_offset + i,
                    static_cast<unsigned>((value >> (width - 1 - i)) & 1u));
        }
    }
}

}  // namespace binrw
