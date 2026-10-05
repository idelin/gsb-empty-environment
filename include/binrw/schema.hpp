#pragma once

// Schema description: a named list of bit fields within the payload.
// Fields may start at any bit offset and cross byte boundaries.

#include <cstdint>
#include <string>
#include <vector>

#include "binrw/bitstream.hpp"

namespace binrw {

struct FieldDesc {
    std::string name;
    uint64_t bit_offset = 0;   // absolute bit offset within the payload
    unsigned bit_width = 0;    // 1..64
    ByteOrder order = ByteOrder::Little;
    Signedness sign = Signedness::Unsigned;
};

struct Schema {
    uint16_t schema_id = 0;
    std::vector<FieldDesc> fields;

    // Validates widths and offset arithmetic. Throws binrw::Error on
    // overflow or out-of-range widths.
    void validate() const {
        for (const FieldDesc& f : fields) {
            if (f.bit_width == 0 || f.bit_width > 64) {
                throw Error("field '" + f.name + "': width out of range");
            }
            checked_add(f.bit_offset, f.bit_width, "field range");
        }
    }

    const FieldDesc* find(const std::string& name) const {
        for (const FieldDesc& f : fields) {
            if (f.name == name) {
                return &f;
            }
        }
        return nullptr;
    }
};

}  // namespace binrw
