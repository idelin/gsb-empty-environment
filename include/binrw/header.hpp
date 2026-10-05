#pragma once

// Fixed-size message header. All multi-byte fields are little-endian on the
// wire, assembled byte-by-byte so parsing is identical on any host.
//
// Layout (16 bytes):
//   offset 0  uint32  magic
//   offset 4  uint16  version
//   offset 6  uint16  schema_id
//   offset 8  uint64  payload_bits   (payload length measured in BITS)

#include <cstddef>
#include <cstdint>

#include "binrw/bitstream.hpp"

namespace binrw {

struct Header {
    static constexpr size_t kSize = 16;
    static constexpr uint32_t kMagic = 0x31575242;  // "BWR1" little-endian

    uint32_t magic = kMagic;
    uint16_t version = 1;
    uint16_t schema_id = 0;
    uint64_t payload_bits = 0;

    static Header parse(const uint8_t* data, size_t size) {
        if (size < kSize) {
            throw Error("truncated header");
        }
        Header h;
        h.magic = static_cast<uint32_t>(data[0]) |
                  (static_cast<uint32_t>(data[1]) << 8) |
                  (static_cast<uint32_t>(data[2]) << 16) |
                  (static_cast<uint32_t>(data[3]) << 24);
        h.version = static_cast<uint16_t>(data[4]) |
                    static_cast<uint16_t>(static_cast<uint16_t>(data[5]) << 8);
        h.schema_id = static_cast<uint16_t>(data[6]) |
                      static_cast<uint16_t>(static_cast<uint16_t>(data[7]) << 8);
        uint64_t bits = 0;
        for (unsigned i = 0; i < 8; ++i) {
            bits |= static_cast<uint64_t>(data[8 + i]) << (8 * i);
        }
        h.payload_bits = bits;
        if (h.magic != kMagic) {
            throw Error("bad magic");
        }
        return h;
    }

    void serialize(uint8_t* out) const {
        out[0] = static_cast<uint8_t>(magic & 0xFF);
        out[1] = static_cast<uint8_t>((magic >> 8) & 0xFF);
        out[2] = static_cast<uint8_t>((magic >> 16) & 0xFF);
        out[3] = static_cast<uint8_t>((magic >> 24) & 0xFF);
        out[4] = static_cast<uint8_t>(version & 0xFF);
        out[5] = static_cast<uint8_t>((version >> 8) & 0xFF);
        out[6] = static_cast<uint8_t>(schema_id & 0xFF);
        out[7] = static_cast<uint8_t>((schema_id >> 8) & 0xFF);
        for (unsigned i = 0; i < 8; ++i) {
            out[8 + i] = static_cast<uint8_t>((payload_bits >> (8 * i)) & 0xFF);
        }
    }
};

}  // namespace binrw
