#include "netsec.hpp"

#include <algorithm>
#include <array>
#include <cctype>
#include <cmath>
#include <cstdint>
#include <cstring>
#include <limits>
#include <map>
#include <memory>
#include <optional>
#include <set>
#include <sstream>
#include <string>
#include <tuple>
#include <utility>

#if defined(__APPLE__)
#include <CoreFoundation/CoreFoundation.h>
#include <Security/Security.h>
#endif

namespace netsec {
namespace {

thread_local std::string g_last_parse_error;
thread_local CrlError g_last_parse_error_kind = CrlError::MalformedCrl;

constexpr std::uint8_t kUniversal = 0;
constexpr std::uint8_t kContext = 2;

constexpr std::uint32_t kBoolean = 1;
constexpr std::uint32_t kInteger = 2;
constexpr std::uint32_t kBitString = 3;
constexpr std::uint32_t kOctetString = 4;
constexpr std::uint32_t kObjectIdentifier = 6;
constexpr std::uint32_t kEnumerated = 10;
constexpr std::uint32_t kUtf8String = 12;
constexpr std::uint32_t kSequence = 16;
constexpr std::uint32_t kSet = 17;
constexpr std::uint32_t kNumericString = 18;
constexpr std::uint32_t kPrintableString = 19;
constexpr std::uint32_t kTeletexString = 20;
constexpr std::uint32_t kIa5String = 22;
constexpr std::uint32_t kUtcTime = 23;
constexpr std::uint32_t kGeneralizedTime = 24;
constexpr std::uint32_t kVisibleString = 26;
constexpr std::uint32_t kUniversalString = 28;
constexpr std::uint32_t kBmpString = 30;

struct DerNode {
  std::uint8_t tag_class = 0;
  bool constructed = false;
  std::uint32_t tag = 0;
  const std::uint8_t *begin = nullptr;
  const std::uint8_t *content = nullptr;
  std::size_t content_length = 0;
  std::size_t total_length = 0;

  [[nodiscard]] Bytes content_bytes() const {
    return Bytes(content, content + content_length);
  }

  [[nodiscard]] Bytes full_bytes() const {
    return Bytes(begin, begin + total_length);
  }
};

class DerReader {
public:
  DerReader() = default;
  DerReader(const std::uint8_t *begin, std::size_t length)
      : cursor_(begin), end_(begin + length) {}

  explicit DerReader(const DerNode &node)
      : cursor_(node.content), end_(node.content + node.content_length) {}

  [[nodiscard]] std::size_t remaining() const noexcept {
    return static_cast<std::size_t>(end_ - cursor_);
  }

  [[nodiscard]] bool at_end() const noexcept { return cursor_ == end_; }

  bool read(DerNode &out) {
    if (cursor_ >= end_) {
      return false;
    }

    const std::uint8_t *start = cursor_;
    std::uint8_t first = *cursor_++;
    out.tag_class = static_cast<std::uint8_t>(first >> 6);
    out.constructed = (first & 0x20U) != 0;
    out.tag = static_cast<std::uint32_t>(first & 0x1FU);

    if (out.tag == 0x1FU) {
      out.tag = 0;
      bool saw = false;
      bool continued = false;
      while (cursor_ < end_) {
        std::uint8_t b = *cursor_++;
        if (!saw && b == 0x80U) {
          return false;
        }
        saw = true;
        if (out.tag > ((std::numeric_limits<std::uint32_t>::max)() >> 7)) {
          return false;
        }
        out.tag = (out.tag << 7U) | static_cast<std::uint32_t>(b & 0x7FU);
        continued = (b & 0x80U) != 0;
        if (!continued) {
          break;
        }
      }
      if (!saw || continued) {
        return false;
      }
    }

    if (cursor_ >= end_) {
      return false;
    }

    std::uint8_t length_byte = *cursor_++;
    std::size_t length = 0;
    if ((length_byte & 0x80U) == 0) {
      length = length_byte;
    } else {
      std::size_t octets = length_byte & 0x7FU;
      if (octets == 0 || octets > 8 ||
          static_cast<std::size_t>(end_ - cursor_) < octets) {
        return false;
      }
      if (octets > 1 && cursor_[0] == 0x00U) {
        return false;
      }
      std::uint64_t value = 0;
      for (std::size_t i = 0; i < octets; ++i) {
        value = (value << 8U) | cursor_[i];
      }
      if (value < 0x80U ||
          value > static_cast<std::uint64_t>(
                      (std::numeric_limits<std::size_t>::max)())) {
        return false;
      }
      length = static_cast<std::size_t>(value);
      cursor_ += octets;
    }

    if (length > static_cast<std::size_t>(end_ - cursor_)) {
      return false;
    }

    out.begin = start;
    out.content = cursor_;
    out.content_length = length;
    out.total_length = static_cast<std::size_t>(cursor_ - start) + length;
    cursor_ += length;
    return true;
  }

private:
  const std::uint8_t *cursor_ = nullptr;
  const std::uint8_t *end_ = nullptr;
};

bool is_universal(const DerNode &node, std::uint32_t tag, bool constructed) {
  return node.tag_class == kUniversal && node.tag == tag &&
         node.constructed == constructed;
}

std::vector<DerNode> children(const DerNode &node) {
  std::vector<DerNode> result;
  DerReader reader(node);
  DerNode child;
  while (reader.read(child)) {
    result.push_back(child);
  }
  return result;
}

std::optional<DerNode> parse_one(std::span<const std::uint8_t> der,
                                 bool require_end = true) {
  if (der.empty()) {
    g_last_parse_error = "empty DER input";
    return std::nullopt;
  }
  DerReader reader(der.data(), der.size());
  DerNode node;
  if (!reader.read(node)) {
    g_last_parse_error = "malformed DER";
    return std::nullopt;
  }
  if (require_end && !reader.at_end()) {
    g_last_parse_error = "trailing data after DER object";
    return std::nullopt;
  }
  return node;
}

std::string hex_encode(std::span<const std::uint8_t> data) {
  static constexpr char digits[] = "0123456789ABCDEF";
  std::string out;
  out.reserve(data.size() * 2);
  for (std::uint8_t b : data) {
    out.push_back(digits[b >> 4U]);
    out.push_back(digits[b & 0x0FU]);
  }
  return out;
}

std::string trim_ascii(std::string value) {
  auto not_space = [](unsigned char c) { return std::isspace(c) == 0; };
  value.erase(value.begin(),
              std::find_if(value.begin(), value.end(), not_space));
  value.erase(std::find_if(value.rbegin(), value.rend(), not_space).base(),
              value.end());
  return value;
}

std::string collapse_spaces(std::string value) {
  std::string out;
  out.reserve(value.size());
  bool pending_space = false;
  for (unsigned char c : value) {
    if (std::isspace(c) != 0) {
      pending_space = !out.empty();
      continue;
    }
    if (pending_space) {
      out.push_back(' ');
      pending_space = false;
    }
    out.push_back(static_cast<char>(std::tolower(c)));
  }
  return out;
}

std::string bytes_as_text(std::span<const std::uint8_t> data) {
  return std::string(reinterpret_cast<const char *>(data.data()), data.size());
}

bool decode_utf16be(std::span<const std::uint8_t> data, std::string &out) {
  if ((data.size() % 2U) != 0) {
    return false;
  }
  for (std::size_t i = 0; i < data.size(); i += 2) {
    std::uint32_t cp = (static_cast<std::uint32_t>(data[i]) << 8U) |
                       static_cast<std::uint32_t>(data[i + 1]);
    if (cp >= 0xD800U && cp <= 0xDBFFU) {
      if (i + 3 >= data.size()) {
        return false;
      }
      std::uint32_t low = (static_cast<std::uint32_t>(data[i + 2]) << 8U) |
                          static_cast<std::uint32_t>(data[i + 3]);
      if (low < 0xDC00U || low > 0xDFFFU) {
        return false;
      }
      cp = 0x10000U + ((cp - 0xD800U) << 10U) + (low - 0xDC00U);
      i += 2;
    } else if (cp >= 0xDC00U && cp <= 0xDFFFU) {
      return false;
    }
    if (cp <= 0x7FU) {
      out.push_back(static_cast<char>(cp));
    } else if (cp <= 0x7FFU) {
      out.push_back(static_cast<char>(0xC0U | (cp >> 6U)));
      out.push_back(static_cast<char>(0x80U | (cp & 0x3FU)));
    } else if (cp <= 0xFFFFU) {
      out.push_back(static_cast<char>(0xE0U | (cp >> 12U)));
      out.push_back(static_cast<char>(0x80U | ((cp >> 6U) & 0x3FU)));
      out.push_back(static_cast<char>(0x80U | (cp & 0x3FU)));
    } else {
      out.push_back(static_cast<char>(0xF0U | (cp >> 18U)));
      out.push_back(static_cast<char>(0x80U | ((cp >> 12U) & 0x3FU)));
      out.push_back(static_cast<char>(0x80U | ((cp >> 6U) & 0x3FU)));
      out.push_back(static_cast<char>(0x80U | (cp & 0x3FU)));
    }
  }
  return true;
}

bool decode_utf32be(std::span<const std::uint8_t> data, std::string &out) {
  if ((data.size() % 4U) != 0) {
    return false;
  }
  for (std::size_t i = 0; i < data.size(); i += 4) {
    std::uint32_t cp = (static_cast<std::uint32_t>(data[i]) << 24U) |
                       (static_cast<std::uint32_t>(data[i + 1]) << 16U) |
                       (static_cast<std::uint32_t>(data[i + 2]) << 8U) |
                       static_cast<std::uint32_t>(data[i + 3]);
    if (cp > 0x10FFFFU || (cp >= 0xD800U && cp <= 0xDFFFU)) {
      return false;
    }
    if (cp <= 0x7FU) {
      out.push_back(static_cast<char>(cp));
    } else if (cp <= 0x7FFU) {
      out.push_back(static_cast<char>(0xC0U | (cp >> 6U)));
      out.push_back(static_cast<char>(0x80U | (cp & 0x3FU)));
    } else if (cp <= 0xFFFFU) {
      out.push_back(static_cast<char>(0xE0U | (cp >> 12U)));
      out.push_back(static_cast<char>(0x80U | ((cp >> 6U) & 0x3FU)));
      out.push_back(static_cast<char>(0x80U | (cp & 0x3FU)));
    } else {
      out.push_back(static_cast<char>(0xF0U | (cp >> 18U)));
      out.push_back(static_cast<char>(0x80U | ((cp >> 12U) & 0x3FU)));
      out.push_back(static_cast<char>(0x80U | ((cp >> 6U) & 0x3FU)));
      out.push_back(static_cast<char>(0x80U | (cp & 0x3FU)));
    }
  }
  return true;
}

bool decode_directory_string(const DerNode &node, std::string &out) {
  if (node.tag_class != kUniversal) {
    return false;
  }
  switch (node.tag) {
  case kUtf8String:
  case kPrintableString:
  case kNumericString:
  case kTeletexString:
  case kIa5String:
  case kVisibleString:
    out = bytes_as_text(
        std::span<const std::uint8_t>(node.content, node.content_length));
    return true;
  case kBmpString:
    return decode_utf16be(
        std::span<const std::uint8_t>(node.content, node.content_length), out);
  case kUniversalString:
    return decode_utf32be(
        std::span<const std::uint8_t>(node.content, node.content_length), out);
  default:
    return false;
  }
}

bool decode_oid(const DerNode &node, std::string &out) {
  if (!is_universal(node, kObjectIdentifier, false) ||
      node.content_length == 0) {
    return false;
  }

  std::vector<std::uint64_t> arcs;
  std::uint64_t value = 0;
  bool pending = false;
  for (std::size_t i = 0; i < node.content_length; ++i) {
    std::uint8_t b = node.content[i];
    if (!pending && b == 0x80U) {
      return false;
    }
    pending = true;
    if (value > ((std::numeric_limits<std::uint64_t>::max)() >> 7)) {
      return false;
    }
    value = (value << 7U) | static_cast<std::uint64_t>(b & 0x7FU);
    if ((b & 0x80U) == 0) {
      arcs.push_back(value);
      value = 0;
      pending = false;
    }
  }
  if (pending || arcs.empty()) {
    return false;
  }

  std::uint64_t first = arcs.front();
  std::uint64_t arc0 = 0;
  std::uint64_t arc1 = 0;
  if (first < 40) {
    arc0 = 0;
    arc1 = first;
  } else if (first < 80) {
    arc0 = 1;
    arc1 = first - 40;
  } else {
    arc0 = 2;
    arc1 = first - 80;
  }

  std::ostringstream stream;
  stream << arc0 << '.' << arc1;
  for (std::size_t i = 1; i < arcs.size(); ++i) {
    stream << '.' << arcs[i];
  }
  out = stream.str();
  return true;
}

bool decode_positive_integer(const DerNode &node, Bytes &magnitude) {
  if (!is_universal(node, kInteger, false) &&
      !is_universal(node, kEnumerated, false)) {
    return false;
  }
  if (node.content_length == 0) {
    return false;
  }
  if ((node.content[0] & 0x80U) != 0) {
    return false;
  }
  std::size_t first = 0;
  while (first < node.content_length && node.content[first] == 0) {
    ++first;
  }
  magnitude.assign(node.content + first, node.content + node.content_length);
  if (magnitude.empty()) {
    magnitude.push_back(0);
  }
  return true;
}

bool decode_uint64(const DerNode &node, std::uint64_t &out) {
  Bytes magnitude;
  if (!decode_positive_integer(node, magnitude) || magnitude.size() > 8) {
    return false;
  }
  std::uint64_t value = 0;
  for (std::uint8_t b : magnitude) {
    value = (value << 8U) | static_cast<std::uint64_t>(b);
  }
  out = value;
  return true;
}

bool decode_boolean(const DerNode &node, bool &out) {
  if (!is_universal(node, kBoolean, false) || node.content_length != 1) {
    return false;
  }
  if (node.content[0] != 0x00U && node.content[0] != 0xFFU) {
    return false;
  }
  out = node.content[0] != 0x00U;
  return true;
}

bool decode_implicit_boolean(const DerNode &node, bool &out) {
  if (node.tag_class != kContext || node.constructed ||
      node.content_length != 1) {
    return false;
  }
  if (node.content[0] != 0x00U && node.content[0] != 0xFFU) {
    return false;
  }
  out = node.content[0] != 0x00U;
  return true;
}

std::int64_t days_from_civil(int year, unsigned month, unsigned day) noexcept {
  year -= month <= 2;
  const std::int64_t era = (year >= 0 ? year : year - 399) / 400;
  const unsigned yoe = static_cast<unsigned>(year - era * 400);
  const unsigned adjusted_month = month > 2 ? month - 3 : month + 9;
  const unsigned doy = (153U * adjusted_month + 2U) / 5U + day - 1U;
  const unsigned doe = yoe * 365U + yoe / 4U - yoe / 100U + doy;
  return era * 146097 + static_cast<std::int64_t>(doe) - 719468;
}

bool valid_date(int year, int month, int day) {
  if (month < 1 || month > 12 || day < 1) {
    return false;
  }
  static constexpr std::array<int, 12> lengths{31, 28, 31, 30, 31, 30,
                                               31, 31, 30, 31, 30, 31};
  int max_day = lengths[static_cast<std::size_t>(month - 1)];
  bool leap = (year % 4 == 0 && year % 100 != 0) || (year % 400 == 0);
  if (month == 2 && leap) {
    max_day = 29;
  }
  return day <= max_day;
}

bool make_time(int year, int month, int day, int hour, int minute, int second,
               int offset_seconds, TimePoint &out) {
  if (!valid_date(year, month, day) || hour < 0 || hour > 23 || minute < 0 ||
      minute > 59 || second < 0 || second > 60) {
    return false;
  }
  if (second == 60) {
    second = 59;
  }
  std::int64_t days = days_from_civil(year, static_cast<unsigned>(month),
                                      static_cast<unsigned>(day));
  std::int64_t seconds = days * 86400 + static_cast<std::int64_t>(hour) * 3600 +
                         static_cast<std::int64_t>(minute) * 60 + second -
                         offset_seconds;
  out = TimePoint(std::chrono::duration_cast<Clock::duration>(
      std::chrono::seconds(seconds)));
  return true;
}

bool parse_decimal2(const char *text, int &value) {
  if (!std::isdigit(static_cast<unsigned char>(text[0])) ||
      !std::isdigit(static_cast<unsigned char>(text[1]))) {
    return false;
  }
  value = (text[0] - '0') * 10 + (text[1] - '0');
  return true;
}

bool parse_time(const DerNode &node, TimePoint &out) {
  if (node.tag_class != kUniversal ||
      (node.tag != kUtcTime && node.tag != kGeneralizedTime)) {
    return false;
  }

  std::string text = bytes_as_text(
      std::span<const std::uint8_t>(node.content, node.content_length));
  std::size_t pos = 0;
  int year = 0;
  if (node.tag == kUtcTime) {
    if (text.size() < 10) {
      return false;
    }
    int yy = 0;
    if (!parse_decimal2(text.data(), yy)) {
      return false;
    }
    year = yy >= 50 ? 1900 + yy : 2000 + yy;
    pos = 2;
  } else {
    if (text.size() < 14) {
      return false;
    }
    if (!std::isdigit(static_cast<unsigned char>(text[0])) ||
        !std::isdigit(static_cast<unsigned char>(text[1])) ||
        !std::isdigit(static_cast<unsigned char>(text[2])) ||
        !std::isdigit(static_cast<unsigned char>(text[3]))) {
      return false;
    }
    year = (text[0] - '0') * 1000 + (text[1] - '0') * 100 +
           (text[2] - '0') * 10 + (text[3] - '0');
    pos = 4;
  }

  int month = 0;
  int day = 0;
  int hour = 0;
  int minute = 0;
  int second = 0;
  if (!parse_decimal2(text.data() + pos, month) ||
      !parse_decimal2(text.data() + pos + 2, day) ||
      !parse_decimal2(text.data() + pos + 4, hour) ||
      !parse_decimal2(text.data() + pos + 6, minute)) {
    return false;
  }
  pos += 8;

  bool have_seconds = false;
  if (pos + 2 <= text.size() &&
      std::isdigit(static_cast<unsigned char>(text[pos])) &&
      std::isdigit(static_cast<unsigned char>(text[pos + 1]))) {
    if (!parse_decimal2(text.data() + pos, second)) {
      return false;
    }
    pos += 2;
    have_seconds = true;
  }

  if (node.tag == kGeneralizedTime && pos < text.size() &&
      (text[pos] == '.' || text[pos] == ',')) {
    ++pos;
    if (pos >= text.size() ||
        !std::isdigit(static_cast<unsigned char>(text[pos]))) {
      return false;
    }
    while (pos < text.size() &&
           std::isdigit(static_cast<unsigned char>(text[pos]))) {
      ++pos;
    }
  }

  int offset_seconds = 0;
  if (pos < text.size() && text[pos] == 'Z') {
    ++pos;
  } else if (pos < text.size() && (text[pos] == '+' || text[pos] == '-')) {
    int sign = text[pos] == '+' ? 1 : -1;
    ++pos;
    int oh = 0;
    int om = 0;
    if (pos + 4 > text.size() || !parse_decimal2(text.data() + pos, oh) ||
        !parse_decimal2(text.data() + pos + 2, om) || oh > 23 || om > 59) {
      return false;
    }
    offset_seconds = sign * (oh * 3600 + om * 60);
    pos += 4;
  } else if (node.tag == kUtcTime && !have_seconds && pos == text.size()) {
    // Tolerate a legacy missing-Z UTCTime.  RFC 5280 encoded values
    // still have to end in Z.
  } else {
    return false;
  }

  if (pos != text.size()) {
    return false;
  }
  return make_time(year, month, day, hour, minute, second, offset_seconds, out);
}

std::string normalize_dn_value(const DerNode &node) {
  std::string value;
  if (decode_directory_string(node, value)) {
    return collapse_spaces(std::move(value));
  }
  if (node.tag_class == kUniversal && node.tag == kObjectIdentifier) {
    std::string oid_text;
    if (decode_oid(node, oid_text)) {
      return oid_text;
    }
  }
  return "#" + hex_encode(std::span<const std::uint8_t>(node.content,
                                                        node.content_length));
}

bool parse_rdn_content(std::span<const std::uint8_t> content,
                       std::string &out) {
  DerReader reader(content.data(), content.size());
  std::vector<std::string> attributes;
  DerNode atv;
  while (reader.read(atv)) {
    if (!is_universal(atv, kSequence, true)) {
      return false;
    }
    auto parts = children(atv);
    if (parts.size() != 2 ||
        !is_universal(parts[0], kObjectIdentifier, false)) {
      return false;
    }
    std::string oid_text;
    if (!decode_oid(parts[0], oid_text)) {
      return false;
    }
    attributes.push_back(oid_text + "=" + normalize_dn_value(parts[1]));
  }
  if (attributes.empty()) {
    return false;
  }
  std::sort(attributes.begin(), attributes.end());
  out.clear();
  for (std::size_t i = 0; i < attributes.size(); ++i) {
    if (i != 0) {
      out.push_back('+');
    }
    out += attributes[i];
  }
  return true;
}

bool parse_name(const DerNode &node, DistinguishedName &out) {
  if (!is_universal(node, kSequence, true)) {
    return false;
  }
  std::vector<std::string> rdns;
  DerReader reader(node);
  DerNode rdn;
  while (reader.read(rdn)) {
    if (!is_universal(rdn, kSet, true)) {
      return false;
    }
    std::string canonical_rdn;
    if (!parse_rdn_content(
            std::span<const std::uint8_t>(rdn.content, rdn.content_length),
            canonical_rdn)) {
      return false;
    }
    rdns.push_back(std::move(canonical_rdn));
  }
  out.der = node.full_bytes();
  out.canonical.clear();
  for (std::size_t i = 0; i < rdns.size(); ++i) {
    if (i != 0) {
      out.canonical.push_back('/');
    }
    out.canonical += rdns[i];
  }
  std::sort(rdns.begin(), rdns.end());
  out.canonical_sorted.clear();
  for (std::size_t i = 0; i < rdns.size(); ++i) {
    if (i != 0) {
      out.canonical_sorted.push_back('/');
    }
    out.canonical_sorted += rdns[i];
  }
  return true;
}

std::string normalize_uri(std::string value) {
  value = trim_ascii(std::move(value));
  auto scheme_end = value.find(':');
  if (scheme_end != std::string::npos) {
    for (std::size_t i = 0; i < scheme_end; ++i) {
      value[i] =
          static_cast<char>(std::tolower(static_cast<unsigned char>(value[i])));
    }
    if (scheme_end + 2 < value.size() && value[scheme_end + 1] == '/' &&
        value[scheme_end + 2] == '/') {
      std::size_t host_begin = scheme_end + 3;
      std::size_t host_end = value.find_first_of("/?#", host_begin);
      if (host_end == std::string::npos) {
        host_end = value.size();
      }
      for (std::size_t i = host_begin; i < host_end; ++i) {
        value[i] = static_cast<char>(
            std::tolower(static_cast<unsigned char>(value[i])));
      }
    }
  }
  return value;
}

bool parse_general_name(const DerNode &node, GeneralName &out) {
  if (node.tag_class != kContext) {
    return false;
  }
  out.context_tag = node.tag;
  out.der = node.full_bytes();
  out.value = node.content_bytes();
  out.text.clear();
  out.canonical.clear();

  switch (node.tag) {
  case 0:
    out.type = GeneralNameType::OtherName;
    out.canonical = "other:" + hex_encode(out.value);
    break;
  case 1:
    out.type = GeneralNameType::Rfc822Name;
    out.text = bytes_as_text(out.value);
    out.canonical = "email:" + collapse_spaces(out.text);
    break;
  case 2:
    out.type = GeneralNameType::DnsName;
    out.text = bytes_as_text(out.value);
    out.canonical = "dns:" + collapse_spaces(out.text);
    break;
  case 3:
    out.type = GeneralNameType::X400Address;
    out.canonical = "x400:" + hex_encode(out.value);
    break;
  case 4: {
    out.type = GeneralNameType::DirectoryName;
    DerReader reader(node);
    DerNode name_node;
    if (!reader.read(name_node) || !reader.at_end()) {
      return false;
    }
    DistinguishedName name;
    if (!parse_name(name_node, name)) {
      return false;
    }
    out.text = name.canonical;
    out.canonical = "dn:" + name.canonical;
    break;
  }
  case 5:
    out.type = GeneralNameType::EdiPartyName;
    out.canonical = "edi:" + hex_encode(out.value);
    break;
  case 6:
    out.type = GeneralNameType::UniformResourceIdentifier;
    out.text = bytes_as_text(out.value);
    out.canonical = "uri:" + normalize_uri(out.text);
    break;
  case 7:
    out.type = GeneralNameType::IpAddress;
    out.canonical = "ip:" + hex_encode(out.value);
    break;
  case 8: {
    out.type = GeneralNameType::RegisteredId;
    DerNode oid_node;
    oid_node.tag_class = kUniversal;
    oid_node.constructed = false;
    oid_node.tag = kObjectIdentifier;
    oid_node.content = node.content;
    oid_node.content_length = node.content_length;
    std::string oid_text;
    if (!decode_oid(oid_node, oid_text)) {
      return false;
    }
    out.text = oid_text;
    out.canonical = "rid:" + oid_text;
    break;
  }
  default:
    out.type = GeneralNameType::Unknown;
    out.canonical =
        "gn" + std::to_string(node.tag) + ":" + hex_encode(out.value);
    break;
  }
  return !out.canonical.empty();
}

bool parse_general_names_implicit(std::span<const std::uint8_t> content,
                                  std::vector<GeneralName> &out) {
  DerReader reader(content.data(), content.size());
  DerNode node;
  while (reader.read(node)) {
    GeneralName name;
    if (!parse_general_name(node, name)) {
      return false;
    }
    out.push_back(std::move(name));
  }
  return !out.empty();
}

bool parse_general_names(const DerNode &node, std::vector<GeneralName> &out) {
  out.clear();
  if (is_universal(node, kSequence, true)) {
    return parse_general_names_implicit(
        std::span<const std::uint8_t>(node.content, node.content_length), out);
  }
  if (node.tag_class == kContext) {
    return parse_general_names_implicit(
        std::span<const std::uint8_t>(node.content, node.content_length), out);
  }
  return false;
}

bool parse_distribution_point_name(const DerNode &field,
                                   DistributionPointName &out) {
  if (field.tag_class != kContext || (field.tag != 0 && field.tag != 1)) {
    return false;
  }

  DerReader reader(field);
  DerNode inner;
  if (reader.read(inner) && reader.at_end()) {
    if (inner.tag_class == kContext && inner.tag == 0) {
      out.type = DistributionPointNameType::FullName;
      return parse_general_names_implicit(
          std::span<const std::uint8_t>(inner.content, inner.content_length),
          out.full_names);
    }
    if (inner.tag_class == kContext && inner.tag == 1) {
      out.type = DistributionPointNameType::NameRelativeToCrlIssuer;
      out.relative_name_der = inner.full_bytes();
      return parse_rdn_content(
          std::span<const std::uint8_t>(inner.content, inner.content_length),
          out.relative_name_canonical);
    }
  }

  if (field.tag == 0) {
    out.type = DistributionPointNameType::FullName;
    std::vector<GeneralName> names;
    if (parse_general_names_implicit(
            std::span<const std::uint8_t>(field.content, field.content_length),
            names)) {
      out.full_names = std::move(names);
      return true;
    }
  }
  return false;
}

bool parse_reason_flags(const DerNode &node, ReasonFlags &out) {
  if (!is_universal(node, kBitString, false) || node.content_length == 0) {
    return false;
  }
  std::uint8_t unused = node.content[0];
  if (unused > 7 || (node.content_length == 1 && unused != 0)) {
    return false;
  }
  std::size_t bits = (node.content_length - 1) * 8;
  if (unused > bits) {
    return false;
  }
  bits -= unused;

  out.present = true;
  out.reason_mask = 0;
  for (std::size_t bit = 0; bit < bits; ++bit) {
    std::size_t byte_index = 1 + bit / 8;
    std::uint8_t mask = static_cast<std::uint8_t>(0x80U >> (bit % 8));
    if ((node.content[byte_index] & mask) == 0) {
      continue;
    }
    int reason = 0;
    switch (bit) {
    case 1:
      reason = 1;
      break;
    case 2:
      reason = 2;
      break;
    case 3:
      reason = 3;
      break;
    case 4:
      reason = 4;
      break;
    case 5:
      reason = 5;
      break;
    case 6:
      reason = 6;
      break;
    case 7:
      reason = 9;
      break;
    case 8:
      reason = 10;
      break;
    default:
      break;
    }
    if (reason != 0) {
      out.reason_mask |= static_cast<std::uint16_t>(1U << reason);
    }
  }
  return true;
}

struct ParsedExtension {
  std::string oid;
  bool critical = false;
  Bytes value;
};

bool parse_extensions(const DerNode &sequence,
                      std::vector<ParsedExtension> &out) {
  if (!is_universal(sequence, kSequence, true)) {
    return false;
  }
  out.clear();
  std::set<std::string> seen;
  DerReader reader(sequence);
  DerNode extension;
  while (reader.read(extension)) {
    if (!is_universal(extension, kSequence, true)) {
      return false;
    }
    auto parts = children(extension);
    if (parts.size() < 2 || parts.size() > 3 ||
        !is_universal(parts[0], kObjectIdentifier, false)) {
      return false;
    }
    ParsedExtension parsed;
    if (!decode_oid(parts[0], parsed.oid)) {
      return false;
    }
    std::size_t value_index = 1;
    if (parts.size() == 3) {
      if (!decode_boolean(parts[1], parsed.critical)) {
        return false;
      }
      value_index = 2;
    }
    if (!is_universal(parts[value_index], kOctetString, false)) {
      return false;
    }
    parsed.value = parts[value_index].content_bytes();
    if (!seen.insert(parsed.oid).second) {
      return false;
    }
    out.push_back(std::move(parsed));
  }
  return true;
}

} // namespace

bool DistributionPointName::empty() const noexcept {
  if (type == DistributionPointNameType::FullName) {
    return full_names.empty();
  }
  return relative_name_der.empty();
}

bool DistributionPointName::intersects(
    const DistributionPointName &other) const noexcept {
  if (empty() || other.empty() || type != other.type) {
    return false;
  }
  if (type == DistributionPointNameType::NameRelativeToCrlIssuer) {
    return relative_name_canonical == other.relative_name_canonical;
  }
  for (const GeneralName &left : full_names) {
    for (const GeneralName &right : other.full_names) {
      if (!left.canonical.empty() && left.canonical == right.canonical) {
        return true;
      }
    }
  }
  return false;
}

bool ReasonFlags::includes(int reason_code) const noexcept {
  if (!present) {
    return true;
  }
  if (reason_code <= 0 || reason_code > 15) {
    return covers_all_reasons();
  }
  return (reason_mask & static_cast<std::uint16_t>(1U << reason_code)) != 0;
}

bool ReasonFlags::covers_all_reasons() const noexcept {
  if (!present) {
    return true;
  }
  constexpr std::uint16_t all = static_cast<std::uint16_t>(
      (1U << 1) | (1U << 2) | (1U << 3) | (1U << 4) | (1U << 5) | (1U << 6) |
      (1U << 9) | (1U << 10));
  return (reason_mask & all) == all;
}

bool DistinguishedName::equivalent(
    const DistinguishedName &other) const noexcept {
  if (der.empty() || other.der.empty()) {
    return false;
  }
  if (der == other.der) {
    return true;
  }
  return canonical == other.canonical ||
         canonical_sorted == other.canonical_sorted;
}

bool CrlScope::restricts_certificate_type() const noexcept {
  return only_contains_user_certs || only_contains_ca_certs ||
         only_contains_attribute_certs;
}

bool CertificateInfo::can_sign_crls() const noexcept {
  return !key_usage_present ||
         (key_usage_bits & static_cast<std::uint16_t>(1U << 6)) != 0;
}

bool CertificateInfo::valid_at(TimePoint at) const noexcept {
  return at >= not_before && at <= not_after;
}

std::string_view to_string(CrlError error) noexcept {
  switch (error) {
  case CrlError::None:
    return "none";
  case CrlError::EmptyInput:
    return "empty input";
  case CrlError::MalformedDer:
    return "malformed DER";
  case CrlError::MalformedCertificate:
    return "malformed certificate";
  case CrlError::MalformedCrl:
    return "malformed CRL";
  case CrlError::UnsupportedVersion:
    return "unsupported version";
  case CrlError::UnsupportedAlgorithm:
    return "unsupported signature algorithm";
  case CrlError::InvalidSignature:
    return "invalid signature";
  case CrlError::Expired:
    return "CRL is expired";
  case CrlError::NotYetValid:
    return "CRL is not yet valid";
  case CrlError::MissingNextUpdate:
    return "CRL is missing nextUpdate";
  case CrlError::UnknownCriticalExtension:
    return "unknown critical extension";
  case CrlError::IssuerMismatch:
    return "CRL issuer does not match the certificate issuer";
  case CrlError::UnauthorizedCrlIssuer:
    return "CRL issuer is not authorized";
  case CrlError::CrlSignerNotAuthorized:
    return "CRL signer is not authorized to sign CRLs";
  case CrlError::ScopeMismatch:
    return "CRL scope does not cover the certificate";
  case CrlError::NotApplicable:
    return "CRL is not applicable";
  case CrlError::CrlNumberMissing:
    return "CRL number is missing";
  case CrlError::DeltaWithoutBase:
    return "delta CRL without a matching base CRL";
  case CrlError::DeltaGap:
    return "delta CRL chain has a gap";
  case CrlError::DeltaScopeMismatch:
    return "delta CRL scope differs from base CRL scope";
  case CrlError::BaseCrlNumberMismatch:
    return "delta CRL base number does not match the base CRL";
  case CrlError::CertificateIssuerMismatch:
    return "CRL entry certificate issuer does not match the certificate issuer";
  }
  return "unknown error";
}

std::string_view to_string(RevocationStatus status) noexcept {
  switch (status) {
  case RevocationStatus::Good:
    return "good";
  case RevocationStatus::Revoked:
    return "revoked";
  case RevocationStatus::Unknown:
    return "unknown";
  }
  return "unknown";
}

namespace {

bool parse_single_der_value(const Bytes &value, DerNode &out) {
  auto node =
      parse_one(std::span<const std::uint8_t>(value.data(), value.size()));
  if (!node.has_value()) {
    return false;
  }
  out = *node;
  return true;
}

bool parse_algorithm_oid(const DerNode &node, std::string &oid) {
  if (!is_universal(node, kSequence, true)) {
    return false;
  }
  auto parts = children(node);
  if (parts.size() != 1 && parts.size() != 2) {
    return false;
  }
  return decode_oid(parts[0], oid);
}

bool parse_key_usage(const Bytes &value, bool &present, std::uint16_t &bits) {
  DerNode node;
  if (!parse_single_der_value(value, node) ||
      !is_universal(node, kBitString, false) || node.content_length == 0) {
    return false;
  }
  std::uint8_t unused = node.content[0];
  if (unused > 7) {
    return false;
  }
  std::size_t bit_count = (node.content_length - 1) * 8;
  if (unused > bit_count) {
    return false;
  }
  bit_count -= unused;
  present = true;
  bits = 0;
  for (std::size_t bit = 0; bit < bit_count && bit < 16; ++bit) {
    std::size_t byte_index = 1 + bit / 8;
    std::uint8_t mask = static_cast<std::uint8_t>(0x80U >> (bit % 8));
    if ((node.content[byte_index] & mask) != 0) {
      bits |= static_cast<std::uint16_t>(1U << bit);
    }
  }
  return true;
}

bool parse_octet_string_value(const Bytes &value, Bytes &out) {
  DerNode node;
  if (!parse_single_der_value(value, node) ||
      !is_universal(node, kOctetString, false)) {
    return false;
  }
  out = node.content_bytes();
  return true;
}

bool parse_basic_constraints(const Bytes &value, bool &present, bool &is_ca,
                             std::optional<std::uint32_t> &path_length) {
  DerNode node;
  if (!parse_single_der_value(value, node) ||
      !is_universal(node, kSequence, true)) {
    return false;
  }
  present = true;
  is_ca = false;
  path_length.reset();
  auto parts = children(node);
  std::size_t index = 0;
  if (index < parts.size() && is_universal(parts[index], kBoolean, false)) {
    if (!decode_boolean(parts[index], is_ca)) {
      return false;
    }
    ++index;
  }
  if (index < parts.size() &&
      (is_universal(parts[index], kInteger, false) ||
       is_universal(parts[index], kEnumerated, false))) {
    Bytes magnitude;
    if (!decode_positive_integer(parts[index], magnitude) ||
        magnitude.size() > 4) {
      return false;
    }
    std::uint32_t length = 0;
    for (std::uint8_t b : magnitude) {
      length = (length << 8U) | b;
    }
    path_length = length;
    ++index;
  }
  return index == parts.size();
}

bool parse_authority_key_identifier(const Bytes &value, Bytes &key_identifier,
                                    DistinguishedName *issuer_name,
                                    Bytes *serial) {
  DerNode node;
  if (!parse_single_der_value(value, node) ||
      !is_universal(node, kSequence, true)) {
    return false;
  }
  auto parts = children(node);
  for (const DerNode &part : parts) {
    if (part.tag_class != kContext) {
      return false;
    }
    if (part.tag == 0) {
      if (!part.constructed) {
        key_identifier.assign(part.content, part.content + part.content_length);
      } else {
        DerNode inner;
        if (!parse_single_der_value(part.content_bytes(), inner) ||
            !is_universal(inner, kOctetString, false)) {
          return false;
        }
        key_identifier = inner.content_bytes();
      }
    } else if (part.tag == 1 && issuer_name != nullptr) {
      std::vector<GeneralName> names;
      if (!parse_general_names_implicit(
              std::span<const std::uint8_t>(part.content, part.content_length),
              names)) {
        return false;
      }
      for (const GeneralName &name : names) {
        if (name.type == GeneralNameType::DirectoryName) {
          // Re-parse the directoryName so issuer_name has the
          // canonical representation used elsewhere.
          auto name_node = parse_one(
              std::span<const std::uint8_t>(name.der.data(), name.der.size()));
          if (name_node.has_value()) {
            DerReader reader(*name_node);
            DerNode dirname;
            if (reader.read(dirname)) {
              parse_name(dirname, *issuer_name);
            }
          }
          break;
        }
      }
    } else if (part.tag == 2 && serial != nullptr) {
      if (!part.constructed) {
        serial->assign(part.content, part.content + part.content_length);
      }
    }
  }
  return true;
}

bool parse_crl_distribution_points(const Bytes &value,
                                   std::vector<CrlDistributionPoint> &out) {
  DerNode node;
  if (!parse_single_der_value(value, node) ||
      !is_universal(node, kSequence, true)) {
    return false;
  }

  out.clear();
  DerReader reader(node);
  DerNode dp_node;
  while (reader.read(dp_node)) {
    if (!is_universal(dp_node, kSequence, true)) {
      return false;
    }
    CrlDistributionPoint dp;
    auto parts = children(dp_node);
    for (const DerNode &part : parts) {
      if (part.tag_class != kContext) {
        return false;
      }
      if (part.tag == 0) {
        DistributionPointName name;
        if (!parse_distribution_point_name(part, name)) {
          return false;
        }
        dp.distribution_point = std::move(name);
      } else if (part.tag == 1) {
        DerNode bits;
        bits.tag_class = kUniversal;
        bits.constructed = false;
        bits.tag = kBitString;
        bits.content = part.content;
        bits.content_length = part.content_length;
        if (!parse_reason_flags(bits, dp.reasons)) {
          return false;
        }
      } else if (part.tag == 2) {
        if (!parse_general_names_implicit(
                std::span<const std::uint8_t>(part.content,
                                              part.content_length),
                dp.crl_issuers)) {
          return false;
        }
      } else {
        return false;
      }
    }
    out.push_back(std::move(dp));
  }
  return true;
}

bool is_known_critical_certificate_extension(std::string_view oid) {
  static const std::set<std::string_view> known{
      "2.5.29.9",           // subjectDirectoryAttributes
      "2.5.29.14",          // subjectKeyIdentifier
      "2.5.29.15",          // keyUsage
      "2.5.29.17",          // subjectAltName
      "2.5.29.18",          // issuerAltName
      "2.5.29.19",          // basicConstraints
      "2.5.29.30",          // nameConstraints
      "2.5.29.31",          // cRLDistributionPoints
      "2.5.29.32",          // certificatePolicies
      "2.5.29.33",          // policyMappings
      "2.5.29.35",          // authorityKeyIdentifier
      "2.5.29.36",          // policyConstraints
      "2.5.29.37",          // extendedKeyUsage
      "2.5.29.46",          // freshestCRL
      "2.5.29.54",          // inhibitAnyPolicy
      "1.3.6.1.5.5.7.1.1",  // authorityInfoAccess
      "1.3.6.1.5.5.7.1.11", // subjectInfoAccess
      "1.3.6.1.5.5.7.1.24", // tlsFeature
  };
  return known.find(oid) != known.end();
}

bool parse_certificate_internal(std::span<const std::uint8_t> der,
                                CertificateInfo &out) {
  auto outer_opt = parse_one(der);
  if (!outer_opt.has_value()) {
    return false;
  }
  const DerNode &outer = *outer_opt;
  if (!is_universal(outer, kSequence, true)) {
    g_last_parse_error = "certificate is not a SEQUENCE";
    return false;
  }
  auto outer_parts = children(outer);
  if (outer_parts.size() != 3) {
    g_last_parse_error = "certificate must have three fields";
    return false;
  }
  const DerNode &tbs = outer_parts[0];
  const DerNode &outer_alg = outer_parts[1];
  const DerNode &signature_node = outer_parts[2];
  if (!is_universal(tbs, kSequence, true) ||
      !is_universal(signature_node, kBitString, false) ||
      signature_node.content_length == 0) {
    g_last_parse_error = "malformed certificate fields";
    return false;
  }

  auto tbs_parts = children(tbs);
  std::size_t index = 0;
  if (index < tbs_parts.size() && tbs_parts[index].tag_class == kContext &&
      tbs_parts[index].tag == 0 && tbs_parts[index].constructed) {
    DerReader version_reader(tbs_parts[index]);
    DerNode version_node;
    std::uint64_t version = 0;
    if (!version_reader.read(version_node) || !version_reader.at_end() ||
        !decode_uint64(version_node, version) || version > 2) {
      g_last_parse_error = "unsupported certificate version";
      return false;
    }
    ++index;
  }
  if (tbs_parts.size() < index + 6) {
    g_last_parse_error = "truncated TBSCertificate";
    return false;
  }

  const DerNode &serial_node = tbs_parts[index++];
  const DerNode &inner_alg = tbs_parts[index++];
  const DerNode &issuer_node = tbs_parts[index++];
  const DerNode &validity_node = tbs_parts[index++];
  const DerNode &subject_node = tbs_parts[index++];
  const DerNode &spki_node = tbs_parts[index++];
  if (!is_universal(serial_node, kInteger, false) ||
      !is_universal(spki_node, kSequence, true)) {
    g_last_parse_error = "malformed certificate serial or SPKI";
    return false;
  }
  if (!decode_positive_integer(serial_node, out.serial_number)) {
    g_last_parse_error = "invalid certificate serial number";
    return false;
  }
  if (!parse_name(issuer_node, out.issuer) ||
      !parse_name(subject_node, out.subject)) {
    g_last_parse_error = "invalid certificate name";
    return false;
  }
  if (!is_universal(validity_node, kSequence, true)) {
    g_last_parse_error = "invalid certificate validity";
    return false;
  }
  out.validity_der = validity_node.full_bytes();
  out.subject_public_key_info = spki_node.full_bytes();
  auto validity_parts = children(validity_node);
  if (validity_parts.size() != 2 ||
      !parse_time(validity_parts[0], out.not_before) ||
      !parse_time(validity_parts[1], out.not_after)) {
    g_last_parse_error = "invalid certificate validity time";
    return false;
  }

  std::string inner_alg_oid;
  if (!parse_algorithm_oid(outer_alg, out.signature_algorithm_oid) ||
      !parse_algorithm_oid(inner_alg, inner_alg_oid) ||
      inner_alg_oid != out.signature_algorithm_oid) {
    g_last_parse_error = "certificate signature algorithms differ";
    return false;
  }
  out.signature.assign(signature_node.content + 1,
                       signature_node.content + signature_node.content_length);

  bool extensions_seen = false;
  while (index < tbs_parts.size()) {
    const DerNode &extra = tbs_parts[index++];
    if (extra.tag_class == kContext && (extra.tag == 1 || extra.tag == 2)) {
      continue;
    }
    if (extra.tag_class == kContext && extra.tag == 3 && extra.constructed) {
      if (extensions_seen) {
        g_last_parse_error = "duplicate certificate extensions";
        return false;
      }
      extensions_seen = true;
      DerReader reader(extra);
      DerNode extensions;
      if (!reader.read(extensions) || !reader.at_end() ||
          !is_universal(extensions, kSequence, true)) {
        g_last_parse_error = "malformed certificate extensions";
        return false;
      }
      std::vector<ParsedExtension> parsed;
      if (!parse_extensions(extensions, parsed)) {
        g_last_parse_error = "malformed certificate extension";
        return false;
      }
      for (const ParsedExtension &extension : parsed) {
        if (extension.critical) {
          out.critical_extension_oids.push_back(extension.oid);
        }
        if (extension.oid == oid::basic_constraints) {
          if (!parse_basic_constraints(extension.value,
                                       out.basic_constraints_present, out.is_ca,
                                       out.path_length_constraint)) {
            g_last_parse_error = "malformed basicConstraints";
            return false;
          }
        } else if (extension.oid == oid::key_usage) {
          if (!parse_key_usage(extension.value, out.key_usage_present,
                               out.key_usage_bits)) {
            g_last_parse_error = "malformed keyUsage";
            return false;
          }
        } else if (extension.oid == oid::subject_key_identifier) {
          if (!parse_octet_string_value(extension.value,
                                        out.subject_key_identifier)) {
            g_last_parse_error = "malformed subjectKeyIdentifier";
            return false;
          }
        } else if (extension.oid == oid::authority_key_identifier) {
          if (!parse_authority_key_identifier(extension.value,
                                              out.authority_key_identifier,
                                              nullptr, nullptr)) {
            g_last_parse_error = "malformed authorityKeyIdentifier";
            return false;
          }
        } else if (extension.oid == oid::c_rl_distribution_points) {
          if (!parse_crl_distribution_points(extension.value,
                                             out.crl_distribution_points)) {
            g_last_parse_error = "malformed cRLDistributionPoints";
            return false;
          }
        }
      }
    } else {
      g_last_parse_error = "unexpected TBSCertificate field";
      return false;
    }
  }

  out.der = Bytes(der.begin(), der.end());
  out.tbs_certificate = tbs.full_bytes();
  return true;
}

bool is_known_crl_extension(std::string_view oid) {
  static const std::set<std::string_view> known{
      "2.5.29.18",         // issuerAltName
      "2.5.29.20",         // cRLNumber
      "2.5.29.27",         // deltaCRLIndicator
      "2.5.29.28",         // issuingDistributionPoint
      "2.5.29.35",         // authorityKeyIdentifier
      "2.5.29.46",         // freshestCRL
      "1.3.6.1.5.5.7.1.1", // authorityInfoAccess
  };
  return known.find(oid) != known.end();
}

bool is_known_crl_entry_extension(std::string_view oid) {
  static const std::set<std::string_view> known{
      "2.5.29.21", // reasonCode
      "2.5.29.23", // holdInstructionCode
      "2.5.29.24", // invalidityDate
      "2.5.29.29", // certificateIssuer
  };
  return known.find(oid) != known.end();
}

bool parse_issuing_distribution_point(const Bytes &value, CrlScope &scope) {
  DerNode node;
  if (!parse_single_der_value(value, node) ||
      !is_universal(node, kSequence, true)) {
    return false;
  }
  auto parts = children(node);
  for (const DerNode &part : parts) {
    if (part.tag_class != kContext) {
      return false;
    }
    switch (part.tag) {
    case 0: {
      DistributionPointName name;
      if (!parse_distribution_point_name(part, name)) {
        return false;
      }
      scope.distribution_point = std::move(name);
      break;
    }
    case 1:
      if (!decode_implicit_boolean(part, scope.only_contains_user_certs)) {
        return false;
      }
      break;
    case 2:
      if (!decode_implicit_boolean(part, scope.only_contains_ca_certs)) {
        return false;
      }
      break;
    case 3: {
      DerNode bits;
      bits.tag_class = kUniversal;
      bits.constructed = false;
      bits.tag = kBitString;
      bits.content = part.content;
      bits.content_length = part.content_length;
      if (!parse_reason_flags(bits, scope.only_some_reasons)) {
        return false;
      }
      break;
    }
    case 4:
      if (!decode_implicit_boolean(part, scope.indirect_crl)) {
        return false;
      }
      break;
    case 5:
      if (!decode_implicit_boolean(part, scope.only_contains_attribute_certs)) {
        return false;
      }
      break;
    default:
      return false;
    }
  }
  if (scope.only_contains_user_certs && scope.only_contains_ca_certs) {
    return false;
  }
  return true;
}

bool parse_crl_entry(const DerNode &entry_node, CrlEntry &out) {
  if (!is_universal(entry_node, kSequence, true)) {
    return false;
  }
  auto parts = children(entry_node);
  if (parts.size() < 2 || parts.size() > 3 ||
      !is_universal(parts[0], kInteger, false) ||
      !parse_time(parts[1], out.revocation_date)) {
    return false;
  }
  if (!decode_positive_integer(parts[0], out.serial_number)) {
    return false;
  }
  if (parts.size() == 3) {
    if (!is_universal(parts[2], kSequence, true)) {
      return false;
    }
    std::vector<ParsedExtension> extensions;
    if (!parse_extensions(parts[2], extensions)) {
      return false;
    }
    for (const ParsedExtension &extension : extensions) {
      if (extension.critical && !is_known_crl_entry_extension(extension.oid)) {
        g_last_parse_error =
            "unknown critical CRL entry extension " + extension.oid;
        g_last_parse_error_kind = CrlError::UnknownCriticalExtension;
        return false;
      }
      if (extension.oid == oid::reason_code) {
        DerNode reason;
        if (!parse_single_der_value(extension.value, reason) ||
            (!is_universal(reason, kEnumerated, false) &&
             !is_universal(reason, kInteger, false))) {
          return false;
        }
        std::uint64_t code = 0;
        if (!decode_uint64(reason, code) || code > 10 || code == 7) {
          return false;
        }
        out.reason_code = static_cast<int>(code);
      } else if (extension.oid == oid::invalidity_date) {
        DerNode date;
        TimePoint parsed_time{};
        if (!parse_single_der_value(extension.value, date) ||
            !parse_time(date, parsed_time)) {
          return false;
        }
        out.invalidity_date = parsed_time;
      } else if (extension.oid == oid::certificate_issuer) {
        DerNode names_node;
        if (!parse_single_der_value(extension.value, names_node) ||
            !parse_general_names(names_node, out.certificate_issuer)) {
          return false;
        }
      }
    }
  }
  return true;
}

bool parse_crl_internal(std::span<const std::uint8_t> der, CrlInfo &out) {
  auto outer_opt = parse_one(der);
  if (!outer_opt.has_value()) {
    return false;
  }
  const DerNode &outer = *outer_opt;
  if (!is_universal(outer, kSequence, true)) {
    g_last_parse_error = "CRL is not a SEQUENCE";
    return false;
  }
  auto outer_parts = children(outer);
  if (outer_parts.size() != 3) {
    g_last_parse_error = "CRL must have three fields";
    return false;
  }
  const DerNode &tbs = outer_parts[0];
  const DerNode &outer_alg = outer_parts[1];
  const DerNode &signature_node = outer_parts[2];
  if (!is_universal(tbs, kSequence, true) ||
      !is_universal(signature_node, kBitString, false) ||
      signature_node.content_length == 0) {
    g_last_parse_error = "malformed CRL fields";
    return false;
  }

  auto tbs_parts = children(tbs);
  std::size_t index = 0;
  std::uint64_t version = 0;
  if (index < tbs_parts.size() &&
      is_universal(tbs_parts[index], kInteger, false)) {
    if (!decode_uint64(tbs_parts[index], version) || version > 1) {
      g_last_parse_error = "unsupported CRL version";
      return false;
    }
    ++index;
  }
  if (tbs_parts.size() < index + 3) {
    g_last_parse_error = "truncated TBSCertList";
    return false;
  }

  const DerNode &inner_alg = tbs_parts[index++];
  const DerNode &issuer_node = tbs_parts[index++];
  const DerNode &this_update_node = tbs_parts[index++];
  if (!parse_name(issuer_node, out.issuer) ||
      !parse_time(this_update_node, out.this_update)) {
    g_last_parse_error = "invalid CRL issuer or thisUpdate";
    return false;
  }
  if (index < tbs_parts.size() &&
      (tbs_parts[index].tag == kUtcTime ||
       tbs_parts[index].tag == kGeneralizedTime) &&
      tbs_parts[index].tag_class == kUniversal) {
    TimePoint next{};
    if (!parse_time(tbs_parts[index], next)) {
      g_last_parse_error = "invalid CRL nextUpdate";
      return false;
    }
    out.next_update = next;
    ++index;
  }

  if (index < tbs_parts.size() &&
      is_universal(tbs_parts[index], kSequence, true)) {
    DerReader reader(tbs_parts[index]);
    DerNode entry;
    while (reader.read(entry)) {
      CrlEntry parsed;
      if (!parse_crl_entry(entry, parsed)) {
        g_last_parse_error = "malformed revoked certificate entry";
        return false;
      }
      out.entries.push_back(std::move(parsed));
    }
    ++index;
  }

  bool extensions_seen = false;
  if (index < tbs_parts.size() && tbs_parts[index].tag_class == kContext &&
      tbs_parts[index].tag == 0 && tbs_parts[index].constructed) {
    if (version != 1) {
      g_last_parse_error = "CRL extensions require v2";
      return false;
    }
    extensions_seen = true;
    DerReader reader(tbs_parts[index]);
    DerNode extensions;
    if (!reader.read(extensions) || !reader.at_end() ||
        !is_universal(extensions, kSequence, true)) {
      g_last_parse_error = "malformed CRL extensions";
      return false;
    }
    std::vector<ParsedExtension> parsed;
    if (!parse_extensions(extensions, parsed)) {
      g_last_parse_error = "malformed CRL extension";
      return false;
    }
    for (const ParsedExtension &extension : parsed) {
      if (extension.critical) {
        out.critical_extension_oids.push_back(extension.oid);
      }
      if (extension.critical && !is_known_crl_extension(extension.oid)) {
        g_last_parse_error = "unknown critical CRL extension " + extension.oid;
        g_last_parse_error_kind = CrlError::UnknownCriticalExtension;
        return false;
      }
      if (extension.oid == oid::crl_number) {
        DerNode number;
        std::uint64_t value = 0;
        if (!parse_single_der_value(extension.value, number) ||
            !decode_uint64(number, value)) {
          g_last_parse_error = "malformed cRLNumber";
          return false;
        }
        out.crl_number = value;
      } else if (extension.oid == oid::delta_crl_indicator) {
        if (!extension.critical) {
          g_last_parse_error = "deltaCRLIndicator must be critical";
          return false;
        }
        DerNode number;
        std::uint64_t value = 0;
        if (!parse_single_der_value(extension.value, number) ||
            !decode_uint64(number, value)) {
          g_last_parse_error = "malformed deltaCRLIndicator";
          return false;
        }
        out.base_crl_number = value;
      } else if (extension.oid == oid::issuing_distribution_point) {
        if (!extension.critical) {
          g_last_parse_error = "issuingDistributionPoint must be critical";
          return false;
        }
        if (!parse_issuing_distribution_point(extension.value, out.scope)) {
          g_last_parse_error = "malformed issuingDistributionPoint";
          return false;
        }
      } else if (extension.oid == oid::authority_key_identifier) {
        DistinguishedName ignored;
        Bytes ignored_serial;
        if (!parse_authority_key_identifier(extension.value,
                                            out.authority_key_identifier,
                                            &ignored, &ignored_serial)) {
          g_last_parse_error = "malformed CRL AKI";
          return false;
        }
      } else if (extension.oid == oid::issuer_alt_name) {
        DerNode names_node;
        if (!parse_single_der_value(extension.value, names_node) ||
            !parse_general_names(names_node, out.issuer_alt_names)) {
          g_last_parse_error = "malformed CRL issuerAltName";
          return false;
        }
      }
    }
    ++index;
  } else if (extensions_seen) {
    return false;
  }

  if (index != tbs_parts.size()) {
    g_last_parse_error = "unexpected TBSCertList field";
    return false;
  }
  if (out.is_delta() && !out.crl_number.has_value()) {
    g_last_parse_error = "delta CRL is missing cRLNumber";
    return false;
  }

  std::string inner_alg_oid;
  if (!parse_algorithm_oid(outer_alg, out.signature_algorithm_oid) ||
      !parse_algorithm_oid(inner_alg, inner_alg_oid) ||
      inner_alg_oid != out.signature_algorithm_oid) {
    g_last_parse_error = "CRL signature algorithms differ";
    return false;
  }
  out.signature.assign(signature_node.content + 1,
                       signature_node.content + signature_node.content_length);
  out.der = Bytes(der.begin(), der.end());
  out.tbs_cert_list = tbs.full_bytes();
  return true;
}

} // namespace

std::optional<CrlInfo> parse_crl(std::span<const std::uint8_t> der) {
  g_last_parse_error.clear();
  g_last_parse_error_kind = CrlError::MalformedCrl;
  CrlInfo info;
  if (!parse_crl_internal(der, info)) {
    return std::nullopt;
  }
  return info;
}

std::optional<CrlInfo> parse_crl(const Bytes &der) {
  return parse_crl(std::span<const std::uint8_t>(der.data(), der.size()));
}

std::optional<CertificateInfo>
parse_certificate(std::span<const std::uint8_t> der) {
  g_last_parse_error.clear();
  g_last_parse_error_kind = CrlError::MalformedCertificate;
  CertificateInfo info;
  if (!parse_certificate_internal(der, info)) {
    return std::nullopt;
  }
  return info;
}

std::optional<CertificateInfo> parse_certificate(const Bytes &der) {
  return parse_certificate(
      std::span<const std::uint8_t>(der.data(), der.size()));
}

std::string last_parse_error() { return g_last_parse_error; }

namespace {

struct SignatureAlgorithmInfo {
  bool rsa = false;
  bool ecdsa = false;
  std::string hash;
};

bool decode_signature_algorithm(std::string_view oid,
                                SignatureAlgorithmInfo &info) {
  info = {};
  if (oid == "1.2.840.113549.1.1.5") {
    info.rsa = true;
    info.hash = "SHA1";
  } else if (oid == "1.2.840.113549.1.1.14") {
    info.rsa = true;
    info.hash = "SHA224";
  } else if (oid == "1.2.840.113549.1.1.11") {
    info.rsa = true;
    info.hash = "SHA256";
  } else if (oid == "1.2.840.113549.1.1.12") {
    info.rsa = true;
    info.hash = "SHA384";
  } else if (oid == "1.2.840.113549.1.1.13") {
    info.rsa = true;
    info.hash = "SHA512";
  } else if (oid == "1.2.840.10045.4.1") {
    info.ecdsa = true;
    info.hash = "SHA1";
  } else if (oid == "1.2.840.10045.4.3.1") {
    info.ecdsa = true;
    info.hash = "SHA224";
  } else if (oid == "1.2.840.10045.4.3.2") {
    info.ecdsa = true;
    info.hash = "SHA256";
  } else if (oid == "1.2.840.10045.4.3.3") {
    info.ecdsa = true;
    info.hash = "SHA384";
  } else if (oid == "1.2.840.10045.4.3.4") {
    info.ecdsa = true;
    info.hash = "SHA512";
  } else {
    return false;
  }
  return true;
}

bool verify_signature_with_certificate(
    const CertificateInfo &signer, std::span<const std::uint8_t> signed_data,
    std::span<const std::uint8_t> signature, std::string_view algorithm_oid,
    std::string &detail) {
  SignatureAlgorithmInfo algorithm;
  if (!decode_signature_algorithm(algorithm_oid, algorithm)) {
    detail = "unsupported signature algorithm " + std::string(algorithm_oid);
    return false;
  }

#if defined(__APPLE__)
  SecKeyAlgorithm sec_algorithm = nullptr;
  if (algorithm.rsa) {
    if (algorithm.hash == "SHA1") {
      sec_algorithm = kSecKeyAlgorithmRSASignatureMessagePKCS1v15SHA1;
    } else if (algorithm.hash == "SHA224") {
      sec_algorithm = kSecKeyAlgorithmRSASignatureMessagePKCS1v15SHA224;
    } else if (algorithm.hash == "SHA256") {
      sec_algorithm = kSecKeyAlgorithmRSASignatureMessagePKCS1v15SHA256;
    } else if (algorithm.hash == "SHA384") {
      sec_algorithm = kSecKeyAlgorithmRSASignatureMessagePKCS1v15SHA384;
    } else if (algorithm.hash == "SHA512") {
      sec_algorithm = kSecKeyAlgorithmRSASignatureMessagePKCS1v15SHA512;
    }
  } else if (algorithm.ecdsa) {
    if (algorithm.hash == "SHA1") {
      sec_algorithm = kSecKeyAlgorithmECDSASignatureMessageX962SHA1;
    } else if (algorithm.hash == "SHA224") {
      sec_algorithm = kSecKeyAlgorithmECDSASignatureMessageX962SHA224;
    } else if (algorithm.hash == "SHA256") {
      sec_algorithm = kSecKeyAlgorithmECDSASignatureMessageX962SHA256;
    } else if (algorithm.hash == "SHA384") {
      sec_algorithm = kSecKeyAlgorithmECDSASignatureMessageX962SHA384;
    } else if (algorithm.hash == "SHA512") {
      sec_algorithm = kSecKeyAlgorithmECDSASignatureMessageX962SHA512;
    }
  }
  if (sec_algorithm == nullptr) {
    detail = "signature algorithm is not supported by the platform verifier";
    return false;
  }

  CFDataRef certificate_data =
      CFDataCreate(kCFAllocatorDefault, signer.der.data(),
                   static_cast<CFIndex>(signer.der.size()));
  SecCertificateRef certificate =
      certificate_data == nullptr
          ? nullptr
          : SecCertificateCreateWithData(kCFAllocatorDefault, certificate_data);
  SecKeyRef key =
      certificate == nullptr ? nullptr : SecCertificateCopyKey(certificate);
  CFDataRef message = CFDataCreate(kCFAllocatorDefault, signed_data.data(),
                                   static_cast<CFIndex>(signed_data.size()));
  CFDataRef signature_data =
      CFDataCreate(kCFAllocatorDefault, signature.data(),
                   static_cast<CFIndex>(signature.size()));
  CFErrorRef error = nullptr;
  bool verified = key != nullptr && message != nullptr &&
                  signature_data != nullptr &&
                  SecKeyVerifySignature(key, sec_algorithm, message,
                                        signature_data, &error);
  if (!verified) {
    detail = "signature verification failed";
    if (error != nullptr) {
      CFStringRef description = CFErrorCopyDescription(error);
      if (description != nullptr) {
        char buffer[256] = {};
        if (CFStringGetCString(description, buffer, sizeof(buffer),
                               kCFStringEncodingUTF8)) {
          detail += ": ";
          detail += buffer;
        }
        CFRelease(description);
      }
      CFRelease(error);
    }
  }
  if (signature_data != nullptr) {
    CFRelease(signature_data);
  }
  if (message != nullptr) {
    CFRelease(message);
  }
  if (key != nullptr) {
    CFRelease(key);
  }
  if (certificate != nullptr) {
    CFRelease(certificate);
  }
  if (certificate_data != nullptr) {
    CFRelease(certificate_data);
  }
  return verified;
#else
  (void)signer;
  (void)signed_data;
  (void)signature;
  detail = "signature verification is unavailable on this platform";
  return false;
#endif
}

bool verify_certificate_signature(const CertificateInfo &subject,
                                  const CertificateInfo &issuer,
                                  std::string &detail) {
  if (!subject.issuer.equivalent(issuer.subject)) {
    detail = "certificate issuer name does not match signer subject";
    return false;
  }
  if (issuer.basic_constraints_present && !issuer.is_ca) {
    detail = "certificate signer is not a CA";
    return false;
  }
  if (issuer.key_usage_present &&
      (issuer.key_usage_bits & static_cast<std::uint16_t>(1U << 5)) == 0) {
    detail = "certificate signer lacks keyCertSign";
    return false;
  }
  return verify_signature_with_certificate(
      issuer,
      std::span<const std::uint8_t>(subject.tbs_certificate.data(),
                                    subject.tbs_certificate.size()),
      std::span<const std::uint8_t>(subject.signature.data(),
                                    subject.signature.size()),
      subject.signature_algorithm_oid, detail);
}

bool certificate_has_unknown_critical_extension(
    const CertificateInfo &certificate) {
  for (const std::string &oid_value : certificate.critical_extension_oids) {
    if (!is_known_critical_certificate_extension(oid_value)) {
      return true;
    }
  }
  return false;
}

bool name_matches_general_name(const DistinguishedName &name,
                               const GeneralName &general_name) {
  if (general_name.type != GeneralNameType::DirectoryName) {
    return false;
  }
  return general_name.canonical == "dn:" + name.canonical ||
         general_name.canonical == "dn:" + name.canonical_sorted;
}

bool name_in_general_names(const DistinguishedName &name,
                           const std::vector<GeneralName> &names) {
  for (const GeneralName &general_name : names) {
    if (name_matches_general_name(name, general_name)) {
      return true;
    }
  }
  return false;
}

bool serial_equal(std::span<const std::uint8_t> left,
                  std::span<const std::uint8_t> right) {
  std::size_t left_start = 0;
  while (left_start < left.size() && left[left_start] == 0) {
    ++left_start;
  }
  std::size_t right_start = 0;
  while (right_start < right.size() && right[right_start] == 0) {
    ++right_start;
  }
  std::size_t left_size = left.size() - left_start;
  std::size_t right_size = right.size() - right_start;
  if (left_size != right_size) {
    return false;
  }
  return std::equal(left.begin() + static_cast<std::ptrdiff_t>(left_start),
                    left.end(),
                    right.begin() + static_cast<std::ptrdiff_t>(right_start));
}

struct SignerCandidate {
  CertificateInfo certificate;
  bool direct = false;
  bool explicitly_trusted = false;
  bool authorization_checked = false;
  bool authorized = false;
};

bool is_direct_issuer(const CrlInfo &crl,
                      const CertificateInfo &issuer_certificate) {
  return crl.issuer.equivalent(issuer_certificate.subject);
}

bool certificate_names_crl_issuer(const CertificateInfo &certificate,
                                  const DistinguishedName &crl_issuer) {
  for (const CrlDistributionPoint &dp : certificate.crl_distribution_points) {
    if (name_in_general_names(crl_issuer, dp.crl_issuers)) {
      return true;
    }
  }
  return false;
}

bool verify_delegation_chain(const CertificateInfo &signer,
                             const CertificateInfo &issuer_certificate,
                             const std::vector<CertificateInfo> &additional,
                             TimePoint now, std::size_t depth,
                             std::string &detail) {
  if (depth > 8) {
    detail = "certificate chain is too deep";
    return false;
  }
  if (!signer.valid_at(now)) {
    detail = "CRL signer certificate is outside its validity period";
    return false;
  }

  if (signer.issuer.equivalent(issuer_certificate.subject)) {
    return verify_certificate_signature(signer, issuer_certificate, detail);
  }

  for (const CertificateInfo &intermediate : additional) {
    if (!intermediate.subject.equivalent(signer.issuer) ||
        intermediate.der == signer.der) {
      continue;
    }
    if (!intermediate.valid_at(now)) {
      continue;
    }
    std::string local_detail;
    if (!verify_certificate_signature(signer, intermediate, local_detail)) {
      continue;
    }
    return verify_delegation_chain(intermediate, issuer_certificate, additional,
                                   now, depth + 1, detail);
  }
  detail = "CRL signer does not chain to the certificate issuer";
  return false;
}

std::vector<CertificateInfo>
parse_additional_certificates(const std::vector<Bytes> &certificates) {
  std::vector<CertificateInfo> parsed;
  for (const Bytes &der : certificates) {
    auto certificate = parse_certificate(der);
    if (certificate.has_value()) {
      parsed.push_back(std::move(*certificate));
    }
  }
  return parsed;
}

bool select_and_verify_signer(const CertificateInfo &certificate,
                              const CertificateInfo &issuer_certificate,
                              const CrlInfo &crl,
                              const ValidationContext &context,
                              CertificateInfo &selected, CrlError &error,
                              std::string &detail) {
  std::vector<SignerCandidate> candidates;
  std::set<std::string> seen;

  auto add_candidate = [&](CertificateInfo candidate, bool direct,
                           bool trusted) {
    if (!candidate.subject.equivalent(crl.issuer)) {
      return;
    }
    if (!seen.insert(hex_encode(std::span<const std::uint8_t>(
                         candidate.der.data(), candidate.der.size())))
             .second) {
      return;
    }
    SignerCandidate entry;
    entry.certificate = std::move(candidate);
    entry.direct = direct;
    entry.explicitly_trusted = trusted;
    candidates.push_back(std::move(entry));
  };

  if (is_direct_issuer(crl, issuer_certificate)) {
    add_candidate(issuer_certificate, true, true);
  }

  for (const Bytes &der : context.trusted_crl_signer_certificates) {
    auto parsed = parse_certificate(der);
    if (parsed.has_value()) {
      add_candidate(std::move(*parsed), false, true);
    }
  }

  std::vector<CertificateInfo> additional =
      parse_additional_certificates(context.additional_certificates);
  for (CertificateInfo &parsed : additional) {
    add_candidate(parsed, false, false);
  }

  if (candidates.empty()) {
    error = CrlError::UnauthorizedCrlIssuer;
    detail = "no certificate matching CRL issuer \"" + crl.issuer.canonical +
             "\" was supplied";
    return false;
  }

  const TimePoint now = context.now;
  bool saw_authorized = false;
  bool saw_signature_failure = false;
  bool saw_key_usage_failure = false;
  bool saw_aki_mismatch = false;
  std::string signature_detail;

  for (SignerCandidate &candidate : candidates) {
    if (context.reject_unknown_critical_certificate_extensions &&
        certificate_has_unknown_critical_extension(candidate.certificate)) {
      error = CrlError::UnknownCriticalExtension;
      detail = "CRL signer certificate has an unknown critical extension";
      return false;
    }

    bool authorized = candidate.direct || candidate.explicitly_trusted;
    if (!authorized && context.allow_delegation_from_crl_distribution_point &&
        certificate_names_crl_issuer(certificate,
                                     candidate.certificate.subject)) {
      std::string chain_detail;
      if (verify_delegation_chain(candidate.certificate, issuer_certificate,
                                  additional, now, 0, chain_detail)) {
        authorized = true;
      } else {
        detail = chain_detail;
      }
    }
    if (!authorized) {
      continue;
    }
    saw_authorized = true;

    if (context.require_crl_signer_key_usage &&
        !candidate.certificate.can_sign_crls()) {
      saw_key_usage_failure = true;
      continue;
    }
    if (!candidate.certificate.valid_at(now)) {
      error = CrlError::Expired;
      detail = "CRL signer certificate is outside its validity period";
      continue;
    }
    if (!crl.authority_key_identifier.empty() &&
        !candidate.certificate.subject_key_identifier.empty() &&
        crl.authority_key_identifier !=
            candidate.certificate.subject_key_identifier) {
      saw_aki_mismatch = true;
      error = CrlError::IssuerMismatch;
      detail = "CRL authorityKeyIdentifier does not match the signer";
      continue;
    }

    std::string local_detail;
    if (!verify_signature_with_certificate(
            candidate.certificate,
            std::span<const std::uint8_t>(crl.tbs_cert_list.data(),
                                          crl.tbs_cert_list.size()),
            std::span<const std::uint8_t>(crl.signature.data(),
                                          crl.signature.size()),
            crl.signature_algorithm_oid, local_detail)) {
      saw_signature_failure = true;
      signature_detail = std::move(local_detail);
      continue;
    }

    selected = candidate.certificate;
    error = CrlError::None;
    detail.clear();
    return true;
  }

  if (saw_key_usage_failure) {
    error = CrlError::CrlSignerNotAuthorized;
    detail = "CRL signer certificate does not assert cRLSign";
  } else if (saw_aki_mismatch) {
    error = CrlError::IssuerMismatch;
    detail = "CRL authorityKeyIdentifier does not match the signer";
  } else if (saw_signature_failure) {
    error = CrlError::InvalidSignature;
    detail = signature_detail.empty() ? "CRL signature is invalid"
                                      : signature_detail;
  } else if (saw_authorized) {
    error = CrlError::UnauthorizedCrlIssuer;
    detail = "CRL signer certificate is not an authorized issuer";
  } else {
    error = CrlError::UnauthorizedCrlIssuer;
    detail = "CRL issuer is neither the certificate issuer nor explicitly "
             "delegated";
  }
  return false;
}

} // namespace

namespace {

bool check_crl_time(const CrlInfo &crl, const ValidationContext &context,
                    CrlError &error, std::string &detail) {
  const TimePoint now = context.now;
  if (context.reject_future_this_update &&
      crl.this_update > now + context.clock_skew) {
    error = CrlError::NotYetValid;
    detail = "CRL thisUpdate is in the future";
    return false;
  }
  if (crl.next_update.has_value()) {
    if (now > *crl.next_update + context.clock_skew) {
      error = CrlError::Expired;
      detail = "CRL nextUpdate has passed";
      return false;
    }
  } else if (context.require_next_update) {
    error = CrlError::MissingNextUpdate;
    detail = "CRL does not contain nextUpdate";
    return false;
  }
  error = CrlError::None;
  detail.clear();
  return true;
}

bool check_certificate_scope(const CertificateInfo &certificate,
                             const CertificateInfo &issuer_certificate,
                             const CrlInfo &crl, const CertificateInfo &signer,
                             CrlError &error, std::string &detail) {
  if (!certificate.issuer.equivalent(issuer_certificate.subject)) {
    error = CrlError::IssuerMismatch;
    detail = "issuer certificate does not match the certificate issuer name";
    return false;
  }

  if (!crl.scope.indirect_crl && !crl.issuer.equivalent(certificate.issuer)) {
    error = CrlError::ScopeMismatch;
    detail = "a non-indirect CRL is limited to certificates issued by the "
             "CRL issuer";
    return false;
  }

  if (crl.scope.only_contains_attribute_certs) {
    error = CrlError::ScopeMismatch;
    detail = "CRL is restricted to attribute certificates";
    return false;
  }
  if (crl.scope.only_contains_user_certs && certificate.is_ca) {
    error = CrlError::ScopeMismatch;
    detail = "CRL only contains user certificates but the certificate is a CA";
    return false;
  }
  if (crl.scope.only_contains_ca_certs && !certificate.is_ca) {
    error = CrlError::ScopeMismatch;
    detail =
        "CRL only contains CA certificates but the certificate is not a CA";
    return false;
  }

  if (crl.scope.distribution_point.has_value()) {
    const DistributionPointName &idp_dp = *crl.scope.distribution_point;
    bool matched = false;
    bool matched_issuer = false;
    bool found_explicit_issuer = false;
    for (const CrlDistributionPoint &cert_dp :
         certificate.crl_distribution_points) {
      if (!cert_dp.distribution_point.has_value() ||
          !cert_dp.distribution_point->intersects(idp_dp)) {
        continue;
      }
      matched = true;
      if (cert_dp.crl_issuers.empty()) {
        if (!crl.scope.indirect_crl ||
            crl.issuer.equivalent(certificate.issuer)) {
          matched_issuer = true;
        }
      } else {
        found_explicit_issuer = true;
        if (name_in_general_names(signer.subject, cert_dp.crl_issuers) ||
            name_in_general_names(crl.issuer, cert_dp.crl_issuers)) {
          matched_issuer = true;
        }
      }
    }
    if (!matched) {
      error = CrlError::ScopeMismatch;
      detail = "certificate has no cRLDistributionPoint matching the CRL "
               "issuing distribution point";
      return false;
    }
    if (found_explicit_issuer && !matched_issuer) {
      error = CrlError::ScopeMismatch;
      detail = "certificate's cRLIssuer does not name the CRL signer";
      return false;
    }
    if (crl.scope.indirect_crl && !matched_issuer) {
      error = CrlError::ScopeMismatch;
      detail = "indirect CRL has no explicitly delegated cRLIssuer for the "
               "certificate distribution point";
      return false;
    }
  } else if (crl.scope.indirect_crl) {
    bool any_explicit_issuer = false;
    bool matched_issuer = false;
    for (const CrlDistributionPoint &cert_dp :
         certificate.crl_distribution_points) {
      if (cert_dp.crl_issuers.empty()) {
        continue;
      }
      any_explicit_issuer = true;
      if (name_in_general_names(signer.subject, cert_dp.crl_issuers)) {
        matched_issuer = true;
      }
    }
    if (any_explicit_issuer && !matched_issuer) {
      error = CrlError::ScopeMismatch;
      detail = "indirect CRL signer is not named by any certificate "
               "cRLIssuer";
      return false;
    }
  } else if (!certificate.crl_distribution_points.empty()) {
    bool direct_dp = false;
    for (const CrlDistributionPoint &cert_dp :
         certificate.crl_distribution_points) {
      if (cert_dp.crl_issuers.empty() ||
          name_in_general_names(crl.issuer, cert_dp.crl_issuers)) {
        direct_dp = true;
        break;
      }
    }
    if (!direct_dp) {
      error = CrlError::ScopeMismatch;
      detail = "certificate's cRLIssuer points to a different CRL signer";
      return false;
    }
  }

  error = CrlError::None;
  detail.clear();
  return true;
}

struct MatchingEntry {
  const CrlEntry *entry = nullptr;
  std::size_t crl_index = 0;
  std::size_t entry_index = 0;
  bool reason_uncovered = false;
};

std::vector<MatchingEntry>
matching_entries(const CrlInfo &crl, std::size_t crl_index,
                 const CertificateInfo &certificate) {
  std::vector<MatchingEntry> matches;
  std::vector<GeneralName> current_issuer;

  for (std::size_t i = 0; i < crl.entries.size(); ++i) {
    const CrlEntry &entry = crl.entries[i];
    if (crl.scope.indirect_crl && !entry.certificate_issuer.empty()) {
      current_issuer = entry.certificate_issuer;
    }
    if (!serial_equal(entry.serial_number, certificate.serial_number)) {
      continue;
    }
    if (crl.scope.indirect_crl) {
      if (current_issuer.empty()) {
        if (!certificate.issuer.equivalent(crl.issuer)) {
          continue;
        }
      } else if (!name_in_general_names(certificate.issuer, current_issuer)) {
        continue;
      }
    }
    MatchingEntry match;
    match.entry = &entry;
    match.crl_index = crl_index;
    match.entry_index = i;
    int reason = entry.reason_code.value_or(0);
    match.reason_uncovered = !crl.scope.only_some_reasons.includes(reason);
    matches.push_back(match);
  }
  return matches;
}

struct StatusState {
  bool revoked = false;
  std::optional<MatchingEntry> match;
  bool saw_uncovered_reason = false;
};

void apply_entries(StatusState &state, const CrlInfo &crl,
                   std::size_t crl_index, const CertificateInfo &certificate,
                   TimePoint now, std::chrono::seconds skew) {
  for (const MatchingEntry &match :
       matching_entries(crl, crl_index, certificate)) {
    if (match.entry->revocation_date > now + skew) {
      continue;
    }
    if (match.reason_uncovered) {
      state.saw_uncovered_reason = true;
      continue;
    }
    if (match.entry->removes_from_crl()) {
      state.revoked = false;
      state.match.reset();
    } else {
      state.revoked = true;
      state.match = match;
    }
  }
}

std::string scope_group_key(const CrlInfo &crl) {
  std::string key = crl.issuer.canonical_sorted.empty()
                        ? crl.issuer.canonical
                        : crl.issuer.canonical_sorted;
  key += "|";
  if (crl.scope.distribution_point.has_value()) {
    const DistributionPointName &dp = *crl.scope.distribution_point;
    if (dp.type == DistributionPointNameType::FullName) {
      std::vector<std::string> names;
      for (const GeneralName &name : dp.full_names) {
        names.push_back(name.canonical);
      }
      std::sort(names.begin(), names.end());
      for (const std::string &name : names) {
        key += name;
        key.push_back(',');
      }
    } else {
      key += "relative:" + dp.relative_name_canonical;
    }
  } else {
    key += "no-dp";
  }
  key += crl.scope.only_contains_user_certs ? "|user" : "|not-user";
  key += crl.scope.only_contains_ca_certs ? "|ca" : "|not-ca";
  key += crl.scope.only_contains_attribute_certs ? "|attr" : "|not-attr";
  key += crl.scope.indirect_crl ? "|indirect" : "|direct";
  key += "|reasons:";
  key += std::to_string(crl.scope.only_some_reasons.present ? 1 : 0);
  key += ":";
  key += std::to_string(crl.scope.only_some_reasons.reason_mask);
  return key;
}

struct PreparedCrl {
  std::size_t index = 0;
  const CrlInfo *crl = nullptr;
  CertificateInfo signer;
  bool valid = false;
  bool scope_ok = false;
  CrlError error = CrlError::None;
  std::string detail;
};

bool prepare_crl(const CertificateInfo &certificate,
                 const CertificateInfo &issuer_certificate, const CrlInfo &crl,
                 std::size_t index, const ValidationContext &context,
                 PreparedCrl &prepared) {
  prepared.index = index;
  prepared.crl = &crl;
  prepared.valid = false;
  prepared.scope_ok = false;
  prepared.error = CrlError::None;
  prepared.detail.clear();

  if (!check_crl_time(crl, context, prepared.error, prepared.detail)) {
    return false;
  }

  CertificateInfo signer;
  CrlError signer_error = CrlError::None;
  std::string signer_detail;
  if (!select_and_verify_signer(certificate, issuer_certificate, crl, context,
                                signer, signer_error, signer_detail)) {
    prepared.error = signer_error;
    prepared.detail = std::move(signer_detail);
    return false;
  }
  prepared.signer = std::move(signer);
  prepared.valid = true;

  if (!check_certificate_scope(certificate, issuer_certificate, crl,
                               prepared.signer, prepared.error,
                               prepared.detail)) {
    return false;
  }
  prepared.scope_ok = true;
  return true;
}

struct ChainOutcome {
  CrlValidationResult result;
  int rank = 0; // 2 = revoked, 1 = good, 0 = unknown
};

ChainOutcome evaluate_chain(const PreparedCrl &base,
                            const std::vector<const PreparedCrl *> &deltas,
                            const CertificateInfo &certificate,
                            const ValidationContext &context) {
  ChainOutcome outcome;
  CrlValidationResult &result = outcome.result;
  result.valid = true;
  result.applicable = true;
  result.status = RevocationStatus::Unknown;
  result.error = CrlError::None;
  result.matching_crl_index = base.index;
  result.crl_number = base.crl->crl_number;
  result.base_crl_number = base.crl->crl_number;

  StatusState state;
  apply_entries(state, *base.crl, base.index, certificate, context.now,
                context.clock_skew);
  for (const PreparedCrl *delta : deltas) {
    apply_entries(state, *delta->crl, delta->index, certificate, context.now,
                  context.clock_skew);
    result.crl_number = delta->crl->crl_number;
  }

  if (state.revoked && state.match.has_value()) {
    result.status = RevocationStatus::Revoked;
    result.revoked = true;
    result.matching_crl_index = state.match->crl_index;
    result.revocation_entry = *state.match->entry;
    outcome.rank = 2;
    return outcome;
  }

  if (state.saw_uncovered_reason ||
      !base.crl->scope.only_some_reasons.covers_all_reasons()) {
    result.valid = false;
    result.status = RevocationStatus::Unknown;
    result.error = CrlError::NotApplicable;
    result.detail = "the CRL covers only a subset of revocation reasons, so "
                    "absence from it is not proof of good status";
    outcome.rank = 0;
    return outcome;
  }

  result.status = RevocationStatus::Good;
  result.revoked = false;
  outcome.rank = 1;
  return outcome;
}

CrlValidationResult
check_revocation_parsed(const CertificateInfo &certificate,
                        const CertificateInfo &issuer_certificate,
                        const std::vector<CrlInfo> &crls,
                        const ValidationContext &context) {
  std::vector<PreparedCrl> prepared;
  prepared.reserve(crls.size());
  for (std::size_t i = 0; i < crls.size(); ++i) {
    PreparedCrl item;
    prepare_crl(certificate, issuer_certificate, crls[i], i, context, item);
    prepared.push_back(std::move(item));
  }

  std::map<std::string, std::vector<const PreparedCrl *>> groups;
  for (const PreparedCrl &item : prepared) {
    if (!item.valid || !item.scope_ok) {
      continue;
    }
    groups[scope_group_key(*item.crl)].push_back(&item);
  }

  if (groups.empty()) {
    CrlValidationResult result;
    result.status = RevocationStatus::Unknown;
    result.error = CrlError::NotApplicable;
    result.detail = "no usable CRL evidence for this certificate";

    int best_priority = -1;
    auto priority = [](CrlError error) {
      switch (error) {
      case CrlError::InvalidSignature:
      case CrlError::UnsupportedAlgorithm:
        return 8;
      case CrlError::Expired:
      case CrlError::NotYetValid:
      case CrlError::MissingNextUpdate:
        return 7;
      case CrlError::UnknownCriticalExtension:
        return 6;
      case CrlError::UnauthorizedCrlIssuer:
      case CrlError::CrlSignerNotAuthorized:
        return 5;
      case CrlError::IssuerMismatch:
        return 4;
      case CrlError::ScopeMismatch:
      case CrlError::NotApplicable:
        return 3;
      case CrlError::DeltaWithoutBase:
      case CrlError::DeltaGap:
      case CrlError::DeltaScopeMismatch:
      case CrlError::BaseCrlNumberMismatch:
        return 2;
      default:
        return 1;
      }
    };
    for (const PreparedCrl &item : prepared) {
      if (!item.scope_ok) {
        result.applicable = false;
      }
      int current = priority(item.error);
      if (current > best_priority) {
        best_priority = current;
        result.error = item.error;
        result.detail = item.detail;
      }
    }
    return result;
  }

  std::optional<ChainOutcome> best;
  for (const auto &[key, group] : groups) {
    (void)key;
    std::vector<const PreparedCrl *> bases;
    std::vector<const PreparedCrl *> deltas;
    for (const PreparedCrl *item : group) {
      if (item->crl->is_delta()) {
        deltas.push_back(item);
      } else {
        bases.push_back(item);
      }
    }

    if (bases.empty()) {
      ChainOutcome outcome;
      outcome.result.status = RevocationStatus::Unknown;
      outcome.result.applicable = true;
      outcome.result.error = CrlError::DeltaWithoutBase;
      outcome.result.detail =
          "a delta CRL was supplied without its complete base CRL";
      if (!deltas.empty()) {
        outcome.result.matching_crl_index = deltas.front()->index;
        outcome.result.base_crl_number = deltas.front()->crl->base_crl_number;
      }
      if (!best.has_value() || best->rank < outcome.rank) {
        best = std::move(outcome);
      }
      continue;
    }

    const PreparedCrl *base = bases.front();
    for (const PreparedCrl *candidate : bases) {
      if (candidate->crl->crl_number.has_value() &&
          (!base->crl->crl_number.has_value() ||
           *candidate->crl->crl_number > *base->crl->crl_number)) {
        base = candidate;
      } else if (!base->crl->crl_number.has_value() &&
                 candidate->crl->this_update > base->crl->this_update) {
        base = candidate;
      }
    }

    bool blocked = false;
    CrlError blocked_error = CrlError::None;
    std::string blocked_detail;
    if (base->crl->crl_number.has_value()) {
      const std::uint64_t base_number = *base->crl->crl_number;
      for (const PreparedCrl &candidate : prepared) {
        if (!candidate.crl->is_delta() ||
            !candidate.crl->issuer.equivalent(base->crl->issuer) ||
            !candidate.crl->base_crl_number.has_value() ||
            *candidate.crl->base_crl_number != base_number) {
          continue;
        }
        if (candidate.index == base->index) {
          continue;
        }
        if (!candidate.valid) {
          blocked = true;
          blocked_error = candidate.error;
          blocked_detail =
              "a delta CRL for this base could not be validated: " +
              candidate.detail;
          break;
        }
        if (!candidate.scope_ok || scope_group_key(*candidate.crl) != key) {
          blocked = true;
          blocked_error = CrlError::DeltaScopeMismatch;
          blocked_detail =
              "a delta CRL for this base has a different issuer or "
              "scope";
          break;
        }
      }
    }
    if (blocked) {
      ChainOutcome outcome;
      outcome.result.status = RevocationStatus::Unknown;
      outcome.result.applicable = true;
      outcome.result.error = blocked_error;
      outcome.result.detail = std::move(blocked_detail);
      outcome.result.matching_crl_index = base->index;
      outcome.result.base_crl_number = base->crl->crl_number;
      if (!best.has_value() || best->rank < outcome.rank) {
        best = std::move(outcome);
      }
      continue;
    }

    if (!deltas.empty() && !base->crl->crl_number.has_value()) {
      ChainOutcome outcome;
      outcome.result.status = RevocationStatus::Unknown;
      outcome.result.applicable = true;
      outcome.result.error = CrlError::CrlNumberMissing;
      outcome.result.detail =
          "base CRL has no cRLNumber and cannot anchor a delta chain";
      outcome.result.matching_crl_index = base->index;
      if (!best.has_value() || best->rank < outcome.rank) {
        best = std::move(outcome);
      }
      continue;
    }

    bool gap = false;
    std::string gap_detail;
    std::vector<const PreparedCrl *> chain;
    if (base->crl->crl_number.has_value()) {
      const std::uint64_t base_number = *base->crl->crl_number;
      if (base_number == (std::numeric_limits<std::uint64_t>::max)()) {
        gap = true;
        gap_detail = "base CRL number cannot be extended";
      }
      for (const PreparedCrl *delta : deltas) {
        if (!delta->crl->crl_number.has_value()) {
          gap = true;
          gap_detail = "delta CRL has no cRLNumber";
          break;
        }
        if (!delta->crl->base_crl_number.has_value()) {
          gap = true;
          gap_detail = "delta CRL has no base CRL number";
          break;
        }
        if (*delta->crl->base_crl_number > base_number) {
          gap = true;
          gap_detail = "delta CRL references a base CRL that was not supplied";
          break;
        }
        if (*delta->crl->base_crl_number < base_number) {
          continue;
        }
        if (*delta->crl->crl_number <= base_number) {
          gap = true;
          gap_detail = "delta CRL number is not greater than the base CRL "
                       "number";
          break;
        }
        chain.push_back(delta);
      }
      if (!gap) {
        std::sort(chain.begin(), chain.end(),
                  [](const PreparedCrl *left, const PreparedCrl *right) {
                    return *left->crl->crl_number < *right->crl->crl_number;
                  });
        std::vector<const PreparedCrl *> deduplicated;
        for (const PreparedCrl *delta : chain) {
          if (delta->crl->this_update < base->crl->this_update) {
            gap = true;
            gap_detail = "delta CRL thisUpdate precedes the base CRL";
            break;
          }
          if (!deduplicated.empty() && *deduplicated.back()->crl->crl_number ==
                                           *delta->crl->crl_number) {
            if (delta->crl->this_update >
                deduplicated.back()->crl->this_update) {
              deduplicated.back() = delta;
            }
            continue;
          }
          deduplicated.push_back(delta);
        }
        chain = std::move(deduplicated);

        std::uint64_t expected = base_number + 1;
        std::vector<const PreparedCrl *> contiguous;
        for (const PreparedCrl *delta : chain) {
          std::uint64_t number = *delta->crl->crl_number;
          if (number < expected) {
            continue;
          }
          if (number > expected) {
            gap = true;
            gap_detail = "delta CRL chain is missing cRLNumber " +
                         std::to_string(expected);
            break;
          }
          contiguous.push_back(delta);
          ++expected;
        }
        chain = std::move(contiguous);
      }
    }

    if (gap) {
      ChainOutcome outcome;
      outcome.result.status = RevocationStatus::Unknown;
      outcome.result.applicable = true;
      outcome.result.error = CrlError::DeltaGap;
      outcome.result.detail = std::move(gap_detail);
      outcome.result.matching_crl_index = base->index;
      outcome.result.base_crl_number = base->crl->crl_number;
      if (!best.has_value() || best->rank < outcome.rank) {
        best = std::move(outcome);
      }
      continue;
    }

    ChainOutcome outcome = evaluate_chain(*base, chain, certificate, context);
    if (!best.has_value() || outcome.rank > best->rank ||
        (outcome.rank == best->rank && outcome.result.error == CrlError::None &&
         best->result.error != CrlError::None)) {
      best = std::move(outcome);
    }
  }

  if (best.has_value()) {
    return best->result;
  }

  CrlValidationResult result;
  result.status = RevocationStatus::Unknown;
  result.error = CrlError::NotApplicable;
  result.detail = "no usable CRL evidence for this certificate";
  return result;
}

} // namespace

CrlValidationResult validate_crl(const CertificateInfo &certificate,
                                 const CertificateInfo &issuer_certificate,
                                 const CrlInfo &crl,
                                 const ValidationContext &context) {
  return check_revocation_parsed(certificate, issuer_certificate, {crl},
                                 context);
}

CrlValidationResult validate_crl(const Bytes &certificate_der,
                                 const Bytes &issuer_certificate_der,
                                 const Bytes &crl_der,
                                 const ValidationContext &context) {
  CrlValidationResult result;
  if (certificate_der.empty() || issuer_certificate_der.empty() ||
      crl_der.empty()) {
    result.error = CrlError::EmptyInput;
    result.detail = "certificate, issuer certificate and CRL are required";
    return result;
  }

  auto certificate = parse_certificate(certificate_der);
  if (!certificate.has_value()) {
    result.error = CrlError::MalformedCertificate;
    result.detail = g_last_parse_error;
    return result;
  }
  auto issuer = parse_certificate(issuer_certificate_der);
  if (!issuer.has_value()) {
    result.error = CrlError::MalformedCertificate;
    result.detail = "issuer certificate: " + g_last_parse_error;
    return result;
  }
  auto crl = parse_crl(crl_der);
  if (!crl.has_value()) {
    result.error = g_last_parse_error_kind;
    result.detail = g_last_parse_error;
    return result;
  }
  return validate_crl(*certificate, *issuer, *crl, context);
}

CrlValidationResult check_revocation(const CertificateInfo &certificate,
                                     const CertificateInfo &issuer_certificate,
                                     const std::vector<CrlInfo> &crls,
                                     const ValidationContext &context) {
  if (crls.empty()) {
    CrlValidationResult result;
    result.error = CrlError::EmptyInput;
    result.detail = "no CRLs supplied";
    return result;
  }
  if (context.reject_unknown_critical_certificate_extensions &&
      (certificate_has_unknown_critical_extension(certificate) ||
       certificate_has_unknown_critical_extension(issuer_certificate))) {
    CrlValidationResult result;
    result.error = CrlError::UnknownCriticalExtension;
    result.detail = "the certificate or its issuer has an unknown critical "
                    "extension";
    return result;
  }
  if (context.reject_expired_certificate &&
      !certificate.valid_at(context.now)) {
    CrlValidationResult result;
    result.error = CrlError::Expired;
    result.detail = "certificate is outside its validity period";
    return result;
  }
  return check_revocation_parsed(certificate, issuer_certificate, crls,
                                 context);
}

CrlValidationResult check_revocation(const Bytes &certificate_der,
                                     const Bytes &issuer_certificate_der,
                                     const std::vector<Bytes> &crl_ders,
                                     const ValidationContext &context) {
  CrlValidationResult result;
  if (certificate_der.empty() || issuer_certificate_der.empty() ||
      crl_ders.empty()) {
    result.error = CrlError::EmptyInput;
    result.detail = "certificate, issuer certificate and at least one CRL "
                    "are required";
    return result;
  }

  auto certificate = parse_certificate(certificate_der);
  if (!certificate.has_value()) {
    result.error = CrlError::MalformedCertificate;
    result.detail = g_last_parse_error;
    return result;
  }
  auto issuer = parse_certificate(issuer_certificate_der);
  if (!issuer.has_value()) {
    result.error = CrlError::MalformedCertificate;
    result.detail = "issuer certificate: " + g_last_parse_error;
    return result;
  }

  std::vector<CrlInfo> parsed_crls;
  parsed_crls.reserve(crl_ders.size());
  for (const Bytes &der : crl_ders) {
    auto crl = parse_crl(der);
    if (!crl.has_value()) {
      result.valid = false;
      result.applicable = false;
      result.status = RevocationStatus::Unknown;
      result.error = g_last_parse_error_kind;
      result.detail = g_last_parse_error;
      return result;
    }
    parsed_crls.push_back(std::move(*crl));
  }

  return check_revocation(*certificate, *issuer, parsed_crls, context);
}

CrlValidationResult check_revocation(const Bytes &certificate_der,
                                     const Bytes &issuer_certificate_der,
                                     const Bytes &crl_der,
                                     const ValidationContext &context) {
  return check_revocation(certificate_der, issuer_certificate_der,
                          std::vector<Bytes>{crl_der}, context);
}

CrlValidationResult validate_certificate(const Bytes &certificate_der,
                                         const Bytes &issuer_certificate_der,
                                         const std::vector<Bytes> &crl_ders,
                                         const ValidationContext &context) {
  CrlValidationResult result;
  auto certificate = parse_certificate(certificate_der);
  if (!certificate.has_value()) {
    result.error = CrlError::MalformedCertificate;
    result.detail = g_last_parse_error;
    return result;
  }
  auto issuer = parse_certificate(issuer_certificate_der);
  if (!issuer.has_value()) {
    result.error = CrlError::MalformedCertificate;
    result.detail = "issuer certificate: " + g_last_parse_error;
    return result;
  }
  if (!certificate->issuer.equivalent(issuer->subject)) {
    result.error = CrlError::IssuerMismatch;
    result.detail =
        "issuer certificate does not match the certificate issuer name";
    return result;
  }
  if (context.reject_unknown_critical_certificate_extensions &&
      (certificate_has_unknown_critical_extension(*certificate) ||
       certificate_has_unknown_critical_extension(*issuer))) {
    result.error = CrlError::UnknownCriticalExtension;
    result.detail =
        "the certificate or its issuer has an unknown critical extension";
    return result;
  }
  if (context.now < certificate->not_before) {
    result.error = CrlError::NotYetValid;
    result.detail = "certificate is not yet valid";
    return result;
  }
  if (context.now > certificate->not_after) {
    result.error = CrlError::Expired;
    result.detail = "certificate is expired";
    return result;
  }
  std::string signature_detail;
  if (!verify_certificate_signature(*certificate, *issuer, signature_detail)) {
    result.error = CrlError::InvalidSignature;
    result.detail = signature_detail;
    return result;
  }
  return check_revocation(certificate_der, issuer_certificate_der, crl_ders,
                          context);
}

CrlValidationResult validate_certificate(const Bytes &certificate_der,
                                         const Bytes &issuer_certificate_der,
                                         const Bytes &crl_der,
                                         const ValidationContext &context) {
  return validate_certificate(certificate_der, issuer_certificate_der,
                              std::vector<Bytes>{crl_der}, context);
}

CrlScopeComponent::CrlScopeComponent(ValidationContext context)
    : context_(std::move(context)) {}

bool CrlScopeComponent::import_crl(const Bytes &crl_der) {
  return import_crl(
      std::span<const std::uint8_t>(crl_der.data(), crl_der.size()));
}

bool CrlScopeComponent::import_crl(std::span<const std::uint8_t> crl_der) {
  auto parsed = parse_crl(crl_der);
  if (!parsed.has_value()) {
    import_errors_.push_back(g_last_parse_error);
    return false;
  }
  crls_.push_back(std::move(*parsed));
  return true;
}

void CrlScopeComponent::clear() {
  crls_.clear();
  import_errors_.clear();
}

CrlValidationResult
CrlScopeComponent::check(const Bytes &certificate_der,
                         const Bytes &issuer_certificate_der,
                         const ValidationContext *override_context) const {
  const ValidationContext &context =
      override_context == nullptr ? context_ : *override_context;

  auto certificate = parse_certificate(certificate_der);
  if (!certificate.has_value()) {
    CrlValidationResult result;
    result.error = CrlError::MalformedCertificate;
    result.detail = g_last_parse_error;
    return result;
  }
  auto issuer = parse_certificate(issuer_certificate_der);
  if (!issuer.has_value()) {
    CrlValidationResult result;
    result.error = CrlError::MalformedCertificate;
    result.detail = "issuer certificate: " + g_last_parse_error;
    return result;
  }
  if (crls_.empty()) {
    CrlValidationResult result;
    result.error = CrlError::EmptyInput;
    result.detail = "no CRLs have been imported";
    return result;
  }
  return check_revocation(*certificate, *issuer, crls_, context);
}

} // namespace netsec
