#pragma once

// Message: a parsed header + payload. Known fields (per schema) are decoded
// into logical values; bits not covered by the schema (unknown fields,
// padding, trailing data) are preserved verbatim in the original payload
// buffer, so re-encoding without modifications reproduces the input
// byte-for-byte.

#include <cstddef>
#include <cstdint>
#include <vector>

#include "binrw/header.hpp"
#include "binrw/schema.hpp"

namespace binrw {

struct FieldValue {
    FieldDesc desc;
    uint64_t raw = 0;  // raw bits exactly as stored on the wire

    // Logical value with sign extension applied per the schema.
    int64_t as_signed() const { return sign_extend(raw, desc.bit_width); }
    uint64_t as_unsigned() const { return raw; }
};

class Message {
public:
    // Parses `size` bytes. `schema` may be nullptr, in which case no fields
    // are decoded and the whole payload is preserved as unknown data.
    // Throws binrw::Error on truncation, bad magic, or length overflow.
    static Message parse(const uint8_t* data, size_t size,
                         const Schema* schema) {
        Message msg;
        msg.header_ = Header::parse(data, size);

        const uint64_t payload_bytes = bits_to_bytes(msg.header_.payload_bits);
        const uint64_t total =
            checked_add(static_cast<uint64_t>(Header::kSize), payload_bytes,
                        "message length");
        if (total > size) {
            throw Error("truncated payload");
        }
        msg.payload_.assign(data + Header::kSize, data + total);

        if (schema != nullptr) {
            schema->validate();
            msg.fields_.reserve(schema->fields.size());
            for (const FieldDesc& f : schema->fields) {
                const uint64_t end =
                    checked_add(f.bit_offset, f.bit_width, "field range");
                if (end > msg.header_.payload_bits) {
                    throw Error("field '" + f.name + "' exceeds payload");
                }
                FieldValue v;
                v.desc = f;
                v.raw = read_bits(msg.payload_.data(), f.bit_offset,
                                  f.bit_width, f.order);
                msg.fields_.push_back(v);
            }
        }
        return msg;
    }

    // Creates a fresh message (payload zero-filled) for encoding.
    static Message create(Header header, const Schema* schema) {
        Message msg;
        msg.header_ = header;
        msg.payload_.assign(
            static_cast<size_t>(bits_to_bytes(header.payload_bits)), 0);
        if (schema != nullptr) {
            schema->validate();
            for (const FieldDesc& f : schema->fields) {
                const uint64_t end =
                    checked_add(f.bit_offset, f.bit_width, "field range");
                if (end > header.payload_bits) {
                    throw Error("field '" + f.name + "' exceeds payload");
                }
                FieldValue v;
                v.desc = f;
                msg.fields_.push_back(v);
            }
        }
        return msg;
    }

    const Header& header() const { return header_; }

    const FieldValue* find(const std::string& name) const {
        for (const FieldValue& v : fields_) {
            if (v.desc.name == name) {
                return &v;
            }
        }
        return nullptr;
    }

    // Sets a known field's raw bits (extra high bits are truncated).
    bool set(const std::string& name, uint64_t raw) {
        for (FieldValue& v : fields_) {
            if (v.desc.name == name) {
                v.raw = truncate_bits(raw, v.desc.bit_width);
                return true;
            }
        }
        return false;
    }

    // Sets a known signed field from its logical (signed) value.
    bool set_signed(const std::string& name, int64_t value) {
        for (FieldValue& v : fields_) {
            if (v.desc.name == name) {
                v.raw = truncate_bits(static_cast<uint64_t>(value),
                                      v.desc.bit_width);
                return true;
            }
        }
        return false;
    }

    // Re-encodes: known fields are written over a copy of the original
    // payload, so unknown bits survive untouched.
    std::vector<uint8_t> encode() const {
        std::vector<uint8_t> out(Header::kSize + payload_.size());
        header_.serialize(out.data());
        std::copy(payload_.begin(), payload_.end(), out.begin() + Header::kSize);
        uint8_t* payload = out.data() + Header::kSize;
        for (const FieldValue& v : fields_) {
            write_bits(payload, v.desc.bit_offset, v.desc.bit_width,
                       v.desc.order, v.raw);
        }
        return out;
    }

private:
    Header header_;
    std::vector<uint8_t> payload_;  // original bits; unknown fields preserved
    std::vector<FieldValue> fields_;
};

}  // namespace binrw
