// debugloc/value.hpp
//
// BitValue: a fixed-width (<= 64 bit) integer value where each bit is either
// known or unknown. Unknown bits model data that could not be recovered
// (e.g. missing memory, unavailable registers) -- they are *never*
// silently materialised as zero.
#pragma once

#include <cstdint>
#include <string>

namespace debugloc {

// Maximum bit width of a single value.
inline constexpr uint32_t kMaxWidth = 64;

struct BitValue {
  uint64_t bits = 0;   // values of known bits; unknown bit positions read as 0
  uint64_t known = 0;  // bitmask: 1 = bit is known, 0 = bit is unknown
  uint32_t width = 0;  // number of significant bits (1..kMaxWidth)

  static uint64_t maskFor(uint32_t width) {
    return width >= 64 ? ~uint64_t{0} : ((uint64_t{1} << width) - 1);
  }

  // Fully known value.
  static BitValue constant(uint64_t v, uint32_t width) {
    BitValue r;
    r.width = width;
    r.known = maskFor(width);
    r.bits = v & r.known;
    return r;
  }

  // Fully unknown value of the given width.
  static BitValue unknown(uint32_t width) {
    BitValue r;
    r.width = width;
    r.known = 0;
    r.bits = 0;
    return r;
  }

  bool isFullyKnown() const { return known == maskFor(width); }
  bool isFullyUnknown() const { return known == 0; }

  // Extract bits [offset, offset+count) (offset counted from the LSB).
  BitValue slice(uint32_t offset, uint32_t count) const {
    BitValue r;
    r.width = count;
    if (count == 0) return r;
    if (offset >= 64) return r;  // entirely out of range: stays unknown
    uint64_t m = maskFor(count);
    r.bits = (bits >> offset) & m;
    r.known = (known >> offset) & m;
    return r;
  }

  // Concatenate: *this becomes the low bits, hi becomes the high bits.
  // Result width must fit in kMaxWidth; caller validates.
  static BitValue concat(const BitValue& hi, const BitValue& lo) {
    BitValue r;
    r.width = hi.width + lo.width;
    r.bits = (hi.bits << lo.width) | lo.bits;
    r.known = (hi.known << lo.width) | lo.known;
    return r;
  }

  // Render as a string of 0/1/x, MSB first, e.g. "01xx10".
  std::string toString() const {
    std::string s;
    s.reserve(width);
    for (int i = static_cast<int>(width) - 1; i >= 0; --i) {
      uint64_t b = uint64_t{1} << i;
      if (!(known & b)) s += 'x';
      else s += (bits & b) ? '1' : '0';
    }
    return s;
  }

  bool operator==(const BitValue& o) const {
    return width == o.width && bits == o.bits && known == o.known;
  }
  bool operator!=(const BitValue& o) const { return !(*this == o); }
};

}  // namespace debugloc
