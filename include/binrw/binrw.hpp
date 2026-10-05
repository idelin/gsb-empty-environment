// binrw: schema-driven binary reader/writer.
//
// Layout:
//   [ fixed 12-byte little-endian header ][ payload bytes ]
//
// Payload fields are described by a Schema: each field has an absolute bit
// offset, a bit width (1..64), a byte/bit order, and signedness. Fields may
// start at any bit and cross byte boundaries.
//
// Portability / safety guarantees:
//   - No unaligned memory access: every multi-byte value is assembled from
//     single bytes with shifts, so behavior is identical on any host
//     endianness and there is no UB from misaligned loads.
//   - All length arithmetic (bit offsets, payload_len * 8, offset + width)
//     is checked for overflow before use.
//   - Signed values are sign-extended from their declared bit width.
//   - Bits not covered by any schema field ("unknown" bits) are preserved
//     verbatim: parse() keeps the original payload bytes and encode() only
//     rewrites the bit ranges owned by known fields.

#pragma once

#include <cstddef>
#include <cstdint>
#include <stdexcept>
#include <string>
#include <utility>
#include <vector>

namespace binrw {

class Error : public std::runtime_error {
public:
  explicit Error(const std::string& msg) : std::runtime_error(msg) {}
};

enum class ByteOrder { Little, Big };

// ---------------------------------------------------------------------------
// Checked arithmetic
// ---------------------------------------------------------------------------

inline bool checked_add_u64(uint64_t a, uint64_t b, uint64_t& out) {
  out = a + b;
  return out >= a;  // false on wraparound
}

inline bool checked_mul_u64(uint64_t a, uint64_t b, uint64_t& out) {
  if (a != 0 && b > UINT64_MAX / a) return false;
  out = a * b;
  return true;
}

// ---------------------------------------------------------------------------
// Bit-level helpers (byte-wise only, host-endianness independent)
// ---------------------------------------------------------------------------

// Mask of the low `width` bits. width must be in [1, 64].
inline uint64_t mask_for(uint32_t width) {
  return width >= 64 ? ~uint64_t{0} : ((uint64_t{1} << width) - 1);
}

// Sign-extend the low `width` bits of `raw` into a full int64_t.
inline int64_t sign_extend(uint64_t raw, uint32_t width) {
  if (width == 0 || width >= 64) return static_cast<int64_t>(raw);
  const uint64_t sign_bit = uint64_t{1} << (width - 1);
  // If the sign bit is set, subtract 2^width; branch-free and UB-free.
  return static_cast<int64_t>((raw ^ sign_bit) - sign_bit);
}

// Read `width` bits starting at absolute bit offset `bit_pos`.
// Little: bit i of the result is stream bit (bit_pos + i), where stream bits
//         are numbered LSB-first within each byte.
// Big:    bits are consumed MSB-first within each byte; the first bit read
//         is the most significant bit of the result.
// The caller must guarantee bit_pos + width <= 8 * byte_size (checked).
inline uint64_t read_bits(const uint8_t* data, uint64_t bit_pos,
                          uint32_t width, ByteOrder order) {
  uint64_t value = 0;
  if (order == ByteOrder::Little) {
    for (uint32_t i = 0; i < width; ++i) {
      const uint64_t p = bit_pos + i;
      const uint64_t bit = (data[p >> 3] >> (p & 7)) & 1u;
      value |= bit << i;
    }
  } else {
    for (uint32_t i = 0; i < width; ++i) {
      const uint64_t p = bit_pos + i;
      const uint64_t bit = (data[p >> 3] >> (7 - (p & 7))) & 1u;
      value = (value << 1) | bit;
    }
  }
  return value;
}

// Write the low `width` bits of `value` at absolute bit offset `bit_pos`,
// leaving all other bits in the buffer untouched.
inline void write_bits(uint8_t* data, uint64_t bit_pos, uint32_t width,
                       uint64_t value, ByteOrder order) {
  if (order == ByteOrder::Little) {
    for (uint32_t i = 0; i < width; ++i) {
      const uint64_t p = bit_pos + i;
      const uint8_t bit = static_cast<uint8_t>((value >> i) & 1u);
      uint8_t& byte = data[p >> 3];
      const uint8_t mask = static_cast<uint8_t>(1u << (p & 7));
      byte = static_cast<uint8_t>((byte & ~mask) | (bit << (p & 7)));
    }
  } else {
    for (uint32_t i = 0; i < width; ++i) {
      const uint64_t p = bit_pos + i;
      const uint8_t bit =
          static_cast<uint8_t>((value >> (width - 1 - i)) & 1u);
      uint8_t& byte = data[p >> 3];
      const uint8_t mask = static_cast<uint8_t>(1u << (7 - (p & 7)));
      byte = static_cast<uint8_t>((byte & ~mask) | (bit << (7 - (p & 7))));
    }
  }
}

// ---------------------------------------------------------------------------
// Schema
// ---------------------------------------------------------------------------

struct FieldDesc {
  std::string name;
  uint64_t bit_offset = 0;
  uint32_t bit_width = 0;  // 1..64
  ByteOrder order = ByteOrder::Little;
  bool is_signed = false;
};

// Physical-bit coverage of a field: which (byte, bit-in-byte) cells it owns.
// LE fields number bits LSB-first within a byte, BE fields MSB-first, so two
// fields with disjoint stream-bit ranges can still alias physically; overlap
// checks must be done in physical space. A field of width <= 64 spans at
// most 9 bytes.
struct BitCoverage {
  static constexpr uint32_t kMaxBytes = 9;
  uint64_t first_byte = 0;
  uint32_t num_bytes = 0;
  uint8_t masks[kMaxBytes] = {};
};

inline BitCoverage coverage_of(uint64_t bit_offset, uint32_t width,
                               ByteOrder order) {
  BitCoverage cov;
  cov.first_byte = bit_offset >> 3;
  for (uint32_t i = 0; i < width; ++i) {
    const uint64_t p = bit_offset + i;
    const uint32_t idx = static_cast<uint32_t>((p >> 3) - cov.first_byte);
    const uint8_t mask =
        static_cast<uint8_t>(1u << (order == ByteOrder::Little
                                        ? (p & 7)
                                        : (7 - (p & 7))));
    cov.masks[idx] = static_cast<uint8_t>(cov.masks[idx] | mask);
    if (idx + 1 > cov.num_bytes) cov.num_bytes = idx + 1;
  }
  return cov;
}

inline bool coverages_overlap(const BitCoverage& a, const BitCoverage& b) {
  const uint64_t lo = a.first_byte > b.first_byte ? a.first_byte : b.first_byte;
  const uint64_t a_end = a.first_byte + a.num_bytes;
  const uint64_t b_end = b.first_byte + b.num_bytes;
  const uint64_t hi = a_end < b_end ? a_end : b_end;
  for (uint64_t byte = lo; byte < hi; ++byte) {
    if (a.masks[byte - a.first_byte] & b.masks[byte - b.first_byte]) {
      return true;
    }
  }
  return false;
}

class Schema {
public:
  Schema& add(FieldDesc field) {
    if (field.bit_width == 0 || field.bit_width > 64) {
      throw Error("field '" + field.name + "': bit_width must be in [1, 64]");
    }
    uint64_t end;
    if (!checked_add_u64(field.bit_offset, field.bit_width, end)) {
      throw Error("field '" + field.name + "': bit range overflows u64");
    }
    const BitCoverage cov = coverage_of(field.bit_offset, field.bit_width,
                                        field.order);
    for (size_t i = 0; i < fields_.size(); ++i) {
      if (fields_[i].name == field.name) {
        throw Error("duplicate field name '" + field.name + "'");
      }
      if (coverages_overlap(cov, coverages_[i])) {
        throw Error("field '" + field.name + "' overlaps field '" +
                    fields_[i].name + "'");
      }
    }
    if (end > payload_bits_) payload_bits_ = end;
    fields_.push_back(std::move(field));
    coverages_.push_back(cov);
    return *this;
  }

  // Append a field immediately after the current end of the schema.
  Schema& add_next(std::string name, uint32_t bit_width,
                   ByteOrder order = ByteOrder::Little, bool is_signed = false) {
    return add(FieldDesc{std::move(name), payload_bits_, bit_width, order,
                         is_signed});
  }

  const std::vector<FieldDesc>& fields() const { return fields_; }
  uint64_t payload_bits() const { return payload_bits_; }

private:
  std::vector<FieldDesc> fields_;
  std::vector<BitCoverage> coverages_;
  uint64_t payload_bits_ = 0;
};

// ---------------------------------------------------------------------------
// Header (fixed layout, always little-endian on the wire)
// ---------------------------------------------------------------------------

struct Header {
  static constexpr uint32_t kMagic = 0x31525742;  // "BWR1"
  static constexpr size_t kSize = 12;

  uint16_t version = 1;
  uint16_t schema_id = 0;
  uint32_t payload_len = 0;  // payload size in bytes

  static Header decode(const uint8_t* p, size_t available) {
    if (available < kSize) throw Error("truncated header");
    const uint32_t magic = static_cast<uint32_t>(p[0]) |
                           (static_cast<uint32_t>(p[1]) << 8) |
                           (static_cast<uint32_t>(p[2]) << 16) |
                           (static_cast<uint32_t>(p[3]) << 24);
    if (magic != kMagic) throw Error("bad magic");
    Header h;
    h.version = static_cast<uint16_t>(p[4] | (p[5] << 8));
    h.schema_id = static_cast<uint16_t>(p[6] | (p[7] << 8));
    h.payload_len = static_cast<uint32_t>(p[8]) |
                    (static_cast<uint32_t>(p[9]) << 8) |
                    (static_cast<uint32_t>(p[10]) << 16) |
                    (static_cast<uint32_t>(p[11]) << 24);
    return h;
  }

  void encode(uint8_t* p) const {
    p[0] = static_cast<uint8_t>(kMagic);
    p[1] = static_cast<uint8_t>(kMagic >> 8);
    p[2] = static_cast<uint8_t>(kMagic >> 16);
    p[3] = static_cast<uint8_t>(kMagic >> 24);
    p[4] = static_cast<uint8_t>(version);
    p[5] = static_cast<uint8_t>(version >> 8);
    p[6] = static_cast<uint8_t>(schema_id);
    p[7] = static_cast<uint8_t>(schema_id >> 8);
    p[8] = static_cast<uint8_t>(payload_len);
    p[9] = static_cast<uint8_t>(payload_len >> 8);
    p[10] = static_cast<uint8_t>(payload_len >> 16);
    p[11] = static_cast<uint8_t>(payload_len >> 24);
  }
};

// ---------------------------------------------------------------------------
// Message
// ---------------------------------------------------------------------------

class Message {
public:
  // Parse `size` bytes according to `schema`. Throws binrw::Error on any
  // structural problem (truncation, bad magic, overflowing bit ranges).
  static Message parse(const uint8_t* data, size_t size, const Schema& schema) {
    Message msg;
    msg.header_ = Header::decode(data, size);

    // Length multiplication, checked: payload_len (bytes) -> bits.
    uint64_t payload_bits;
    if (!checked_mul_u64(msg.header_.payload_len, 8, payload_bits)) {
      throw Error("payload length overflows when converted to bits");
    }
    if (msg.header_.payload_len > size - Header::kSize) {
      throw Error("payload truncated: header declares " +
                  std::to_string(msg.header_.payload_len) + " bytes, only " +
                  std::to_string(size - Header::kSize) + " available");
    }

    const uint8_t* payload = data + Header::kSize;
    msg.payload_.assign(payload, payload + msg.header_.payload_len);
    msg.values_.reserve(schema.fields().size());

    for (const auto& f : schema.fields()) {
      uint64_t end;
      if (!checked_add_u64(f.bit_offset, f.bit_width, end)) {
        throw Error("field '" + f.name + "': bit range overflows u64");
      }
      if (end > payload_bits) {
        throw Error("field '" + f.name + "' (" + std::to_string(f.bit_offset) +
                    ".." + std::to_string(end) + ") exceeds payload of " +
                    std::to_string(payload_bits) + " bits");
      }
      msg.values_.push_back(read_bits(payload, f.bit_offset, f.bit_width, f.order));
    }
    msg.schema_ = &schema;
    return msg;
  }

  // Serialize back to bytes. Unknown bits (gaps between schema fields, and
  // any payload bytes beyond the schema) are preserved verbatim because we
  // start from the original payload and only rewrite known field ranges.
  std::vector<uint8_t> encode() const {
    std::vector<uint8_t> out(Header::kSize + payload_.size());
    header_.encode(out.data());
    uint8_t* payload = out.data() + Header::kSize;
    if (!payload_.empty()) {
      std::copy(payload_.begin(), payload_.end(), payload);
    }
    const auto& fields = schema_->fields();
    for (size_t i = 0; i < fields.size(); ++i) {
      write_bits(payload, fields[i].bit_offset, fields[i].bit_width,
                 values_[i], fields[i].order);
    }
    return out;
  }

  const Header& header() const { return header_; }

  // Raw (unsigned) bits of a field, zero-extended to 64 bits.
  uint64_t u64(const std::string& name) const {
    const size_t i = index_of(name);
    return values_[i];
  }

  // Sign-extended value of a signed field (or reinterpretation of an
  // unsigned field's low bits).
  int64_t i64(const std::string& name) const {
    const size_t i = index_of(name);
    return sign_extend(values_[i], schema_->fields()[i].bit_width);
  }

  // Replace a field's raw bits; only the low bit_width bits are kept.
  void set(const std::string& name, uint64_t raw) {
    const size_t i = index_of(name);
    values_[i] = raw & mask_for(schema_->fields()[i].bit_width);
  }

  // Replace a field with a signed value, truncated to its bit width.
  void set_signed(const std::string& name, int64_t value) {
    const size_t i = index_of(name);
    values_[i] = static_cast<uint64_t>(value) &
                 mask_for(schema_->fields()[i].bit_width);
  }

private:
  size_t index_of(const std::string& name) const {
    const auto& fields = schema_->fields();
    for (size_t i = 0; i < fields.size(); ++i) {
      if (fields[i].name == name) return i;
    }
    throw Error("unknown field '" + name + "'");
  }

  Header header_;
  std::vector<uint8_t> payload_;   // original payload bytes (unknown bits kept)
  std::vector<uint64_t> values_;   // raw bits per schema field
  const Schema* schema_ = nullptr; // must outlive the Message
};

}  // namespace binrw
