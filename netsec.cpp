#include "netsec.hpp"

#include <algorithm>
#include <array>
#include <cstdint>
#include <cstring>
#include <limits>
#include <sstream>
#include <string>
#include <unordered_set>
#include <utility>

namespace netsec {

namespace {

constexpr std::uint8_t kUniversal = DerTag::universal;
constexpr std::uint8_t kContext = DerTag::context_specific;

constexpr std::uint32_t kBoolean = 1;
constexpr std::uint32_t kInteger = 2;
constexpr std::uint32_t kBitString = 3;
constexpr std::uint32_t kOctetString = 4;
constexpr std::uint32_t kNull = 5;
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
constexpr std::uint32_t kUniversalString = 28;
constexpr std::uint32_t kBmpString = 30;

[[noreturn]] void fail(DerErrorCode code, std::size_t offset,
                       std::string message) {
  throw DerError(code, offset, std::move(message));
}

bool equal_bytes(ByteView a, ByteView b) {
  return a.size() == b.size() && std::equal(a.begin(), a.end(), b.begin());
}

bool less_bytes(ByteView a, ByteView b) {
  return std::lexicographical_compare(a.begin(), a.end(), b.begin(), b.end());
}

std::string hex_encode(ByteView bytes) {
  static constexpr char digits[] = "0123456789abcdef";
  std::string out;
  out.reserve(bytes.size() * 2);
  for (std::uint8_t b : bytes) {
    out.push_back(digits[b >> 4]);
    out.push_back(digits[b & 0x0F]);
  }
  return out;
}

void append_utf8(std::string& out, std::uint32_t cp) {
  if (cp <= 0x7F) {
    out.push_back(static_cast<char>(cp));
  } else if (cp <= 0x7FF) {
    out.push_back(static_cast<char>(0xC0 | (cp >> 6)));
    out.push_back(static_cast<char>(0x80 | (cp & 0x3F)));
  } else if (cp <= 0xFFFF) {
    out.push_back(static_cast<char>(0xE0 | (cp >> 12)));
    out.push_back(static_cast<char>(0x80 | ((cp >> 6) & 0x3F)));
    out.push_back(static_cast<char>(0x80 | (cp & 0x3F)));
  } else {
    out.push_back(static_cast<char>(0xF0 | (cp >> 18)));
    out.push_back(static_cast<char>(0x80 | ((cp >> 12) & 0x3F)));
    out.push_back(static_cast<char>(0x80 | ((cp >> 6) & 0x3F)));
    out.push_back(static_cast<char>(0x80 | (cp & 0x3F)));
  }
}

bool valid_utf8(ByteView bytes) {
  std::size_t i = 0;
  while (i < bytes.size()) {
    const std::uint8_t c = bytes[i++];
    if (c <= 0x7F) {
      continue;
    }
    std::uint32_t cp = 0;
    std::size_t continuation = 0;
    if ((c & 0xE0) == 0xC0) {
      cp = c & 0x1F;
      continuation = 1;
      if (cp < 2) {
        return false;
      }
    } else if ((c & 0xF0) == 0xE0) {
      cp = c & 0x0F;
      continuation = 2;
    } else if ((c & 0xF8) == 0xF0) {
      cp = c & 0x07;
      continuation = 3;
      if (cp > 4) {
        return false;
      }
    } else {
      return false;
    }
    if (i + continuation > bytes.size()) {
      return false;
    }
    for (std::size_t j = 0; j < continuation; ++j) {
      const std::uint8_t next = bytes[i++];
      if ((next & 0xC0) != 0x80) {
        return false;
      }
      cp = (cp << 6) | (next & 0x3F);
    }
    if (cp > 0x10FFFF || (cp >= 0xD800 && cp <= 0xDFFF)) {
      return false;
    }
    if ((continuation == 2 && cp < 0x800) ||
        (continuation == 3 && cp < 0x10000)) {
      return false;
    }
  }
  return true;
}

bool is_ascii(ByteView bytes) {
  return std::all_of(bytes.begin(), bytes.end(),
                     [](std::uint8_t b) { return b < 0x80; });
}

bool is_printable_string(ByteView bytes) {
  for (std::uint8_t b : bytes) {
    const bool allowed =
        (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z') ||
        (b >= '0' && b <= '9') || b == '\'' || b == '(' || b == ')' ||
        b == '+' || b == ',' || b == '-' || b == '.' || b == '/' || b == ':' ||
        b == '=' || b == '?' || b == ' ';
    if (!allowed) {
      return false;
    }
  }
  return true;
}

bool is_numeric_string(ByteView bytes) {
  return std::all_of(bytes.begin(), bytes.end(), [](std::uint8_t b) {
    return (b >= '0' && b <= '9') || b == ' ';
  });
}

bool is_digit(std::uint8_t b) { return b >= '0' && b <= '9'; }

bool is_leap_year(int year) {
  return (year % 4 == 0 && year % 100 != 0) || year % 400 == 0;
}

int days_in_month(int year, int month) {
  static constexpr int days[] = {31, 28, 31, 30, 31, 30,
                                 31, 31, 30, 31, 30, 31};
  if (month == 2 && is_leap_year(year)) {
    return 29;
  }
  return days[month - 1];
}

int decimal_at(ByteView bytes, std::size_t pos, std::size_t count) {
  int value = 0;
  for (std::size_t i = 0; i < count; ++i) {
    value = value * 10 + (bytes[pos + i] - '0');
  }
  return value;
}

void validate_date_time(int year, int month, int day, int hour, int minute,
                        int second, std::size_t offset) {
  if (year < 0 || year > 9999 || month < 1 || month > 12 || day < 1 ||
      hour < 0 || hour > 23 || minute < 0 || minute > 59 || second < 0 ||
      second > 59) {
    fail(DerErrorCode::invalid_time, offset, "time field out of range");
  }
  if (day > days_in_month(year, month)) {
    fail(DerErrorCode::invalid_time, offset, "invalid calendar day");
  }
}

void validate_utc_time(ByteView content, std::size_t offset) {
  if (content.size() != 13 || content.back() != 'Z') {
    fail(DerErrorCode::invalid_time, offset,
         "UTCTime must be YYMMDDHHMMSSZ");
  }
  for (std::size_t i = 0; i < 12; ++i) {
    if (!is_digit(content[i])) {
      fail(DerErrorCode::invalid_time, offset, "UTCTime has non-digit");
    }
  }
  const int yy = decimal_at(content, 0, 2);
  const int year = yy >= 50 ? 1900 + yy : 2000 + yy;
  validate_date_time(year, decimal_at(content, 2, 2),
                     decimal_at(content, 4, 2), decimal_at(content, 6, 2),
                     decimal_at(content, 8, 2), decimal_at(content, 10, 2),
                     offset);
}

void validate_generalized_time(ByteView content, std::size_t offset) {
  if (content.size() < 15 || content.back() != 'Z') {
    fail(DerErrorCode::invalid_time, offset,
         "GeneralizedTime must be YYYYMMDDHHMMSS[.fraction]Z");
  }
  for (std::size_t i = 0; i < 14; ++i) {
    if (!is_digit(content[i])) {
      fail(DerErrorCode::invalid_time, offset,
           "GeneralizedTime has non-digit");
    }
  }
  if (content.size() > 15) {
    if (content[14] != '.' || content.size() == 16) {
      fail(DerErrorCode::invalid_time, offset,
           "GeneralizedTime fraction must be non-empty and use '.'");
    }
    for (std::size_t i = 15; i + 1 < content.size(); ++i) {
      if (!is_digit(content[i])) {
        fail(DerErrorCode::invalid_time, offset,
             "GeneralizedTime fraction has non-digit");
      }
    }
    if (content[content.size() - 2] == '0') {
      fail(DerErrorCode::invalid_time, offset,
           "GeneralizedTime fraction has a trailing zero");
    }
  }
  validate_date_time(decimal_at(content, 0, 4), decimal_at(content, 4, 2),
                     decimal_at(content, 6, 2), decimal_at(content, 8, 2),
                     decimal_at(content, 10, 2), decimal_at(content, 12, 2),
                     offset);
}

std::string decode_oid(ByteView content, std::size_t offset) {
  if (content.empty()) {
    fail(DerErrorCode::invalid_oid, offset, "empty OBJECT IDENTIFIER");
  }
  std::vector<std::uint64_t> arcs;
  std::size_t i = 0;
  while (i < content.size()) {
    if (content[i] == 0x80) {
      fail(DerErrorCode::non_canonical_tag, offset,
           "OBJECT IDENTIFIER has a leading zero base-128 group");
    }
    std::uint64_t value = 0;
    bool terminated = false;
    while (i < content.size()) {
      const std::uint8_t b = content[i++];
      if (value > (std::numeric_limits<std::uint64_t>::max() >> 7)) {
        fail(DerErrorCode::invalid_oid, offset, "OBJECT IDENTIFIER arc too big");
      }
      value = (value << 7) | (b & 0x7F);
      if ((b & 0x80) == 0) {
        terminated = true;
        break;
      }
    }
    if (!terminated) {
      fail(DerErrorCode::invalid_oid, offset,
           "unterminated OBJECT IDENTIFIER arc");
    }
    if (arcs.empty()) {
      if (value < 40) {
        arcs.push_back(0);
        arcs.push_back(value);
      } else if (value < 80) {
        arcs.push_back(1);
        arcs.push_back(value - 40);
      } else {
        arcs.push_back(2);
        arcs.push_back(value - 80);
      }
    } else {
      arcs.push_back(value);
    }
  }
  std::string out;
  for (std::size_t n = 0; n < arcs.size(); ++n) {
    if (n != 0) {
      out.push_back('.');
    }
    out += std::to_string(arcs[n]);
  }
  return out;
}

std::optional<bool> universal_constructed(std::uint32_t number) {
  switch (number) {
    case 8:
    case 11:
    case 16:
    case 17:
    case 29:
      return true;
    case 1:
    case 2:
    case 3:
    case 4:
    case 5:
    case 6:
    case 7:
    case 9:
    case 10:
    case 12:
    case 13:
    case 14:
    case 18:
    case 19:
    case 20:
    case 21:
    case 22:
    case 23:
    case 24:
    case 25:
    case 26:
    case 27:
    case 28:
    case 30:
      return false;
    default:
      return std::nullopt;
  }
}

void validate_bit_string(ByteView content, std::size_t offset) {
  if (content.empty()) {
    fail(DerErrorCode::invalid_bit_string, offset,
         "BIT STRING lacks the unused-bits octet");
  }
  const std::uint8_t unused = content[0];
  if (unused > 7) {
    fail(DerErrorCode::invalid_bit_string, offset,
         "BIT STRING unused-bits count is not 0..7");
  }
  if (content.size() == 1) {
    if (unused != 0) {
      fail(DerErrorCode::invalid_bit_string, offset,
           "empty BIT STRING cannot have unused bits");
    }
    return;
  }
  if (unused != 0) {
    const std::uint8_t mask =
        static_cast<std::uint8_t>((1U << unused) - 1U);
    if ((content.back() & mask) != 0) {
      fail(DerErrorCode::invalid_bit_string, offset,
           "BIT STRING has non-zero padding bits");
    }
  }
}

void validate_integer(ByteView content, std::size_t offset) {
  if (content.empty()) {
    fail(DerErrorCode::invalid_integer, offset, "empty INTEGER");
  }
  if (content.size() >= 2) {
    if (content[0] == 0x00 && (content[1] & 0x80) == 0) {
      fail(DerErrorCode::non_canonical_tag, offset,
           "INTEGER has a redundant leading zero");
    }
    if (content[0] == 0xFF && (content[1] & 0x80) != 0) {
      fail(DerErrorCode::non_canonical_tag, offset,
           "INTEGER has a redundant leading 0xff");
    }
  }
}

void validate_primitive(const DerNode& node, std::size_t offset) {
  if (node.tag.tag_class != kUniversal) {
    return;
  }
  const ByteView content = node.content;
  switch (node.tag.number) {
    case kBoolean:
      if (content.size() != 1 || (content[0] != 0x00 && content[0] != 0xFF)) {
        fail(DerErrorCode::invalid_boolean, offset,
             "DER BOOLEAN must be one octet 00 or ff");
      }
      break;
    case kInteger:
    case kEnumerated:
      validate_integer(content, offset);
      break;
    case kBitString:
      validate_bit_string(content, offset);
      break;
    case kOctetString:
      break;
    case kNull:
      if (!content.empty()) {
        fail(DerErrorCode::invalid_null, offset, "DER NULL must be empty");
      }
      break;
    case kObjectIdentifier:
      (void)decode_oid(content, offset);
      break;
    case kUtf8String:
      if (!valid_utf8(content)) {
        fail(DerErrorCode::invalid_string, offset, "invalid UTF8String");
      }
      break;
    case kNumericString:
      if (!is_numeric_string(content)) {
        fail(DerErrorCode::invalid_string, offset, "invalid NumericString");
      }
      break;
    case kPrintableString:
      if (!is_printable_string(content)) {
        fail(DerErrorCode::invalid_string, offset, "invalid PrintableString");
      }
      break;
    case kIa5String:
      if (!is_ascii(content)) {
        fail(DerErrorCode::invalid_string, offset, "invalid IA5String");
      }
      break;
    case kUtcTime:
      validate_utc_time(content, offset);
      break;
    case kGeneralizedTime:
      validate_generalized_time(content, offset);
      break;
    case kBmpString:
      if (content.size() % 2 != 0) {
        fail(DerErrorCode::invalid_string, offset, "odd-length BMPString");
      }
      for (std::size_t i = 0; i < content.size(); i += 2) {
        const std::uint16_t unit =
            static_cast<std::uint16_t>((content[i] << 8) | content[i + 1]);
        if (unit >= 0xD800 && unit <= 0xDFFF) {
          fail(DerErrorCode::invalid_string, offset,
               "BMPString contains a surrogate");
        }
      }
      break;
    case kUniversalString:
      if (content.size() % 4 != 0) {
        fail(DerErrorCode::invalid_string, offset,
             "UniversalString length is not a multiple of four");
      }
      for (std::size_t i = 0; i < content.size(); i += 4) {
        const std::uint32_t cp =
            (static_cast<std::uint32_t>(content[i]) << 24) |
            (static_cast<std::uint32_t>(content[i + 1]) << 16) |
            (static_cast<std::uint32_t>(content[i + 2]) << 8) |
            static_cast<std::uint32_t>(content[i + 3]);
        if (cp > 0x10FFFF || (cp >= 0xD800 && cp <= 0xDFFF)) {
          fail(DerErrorCode::invalid_string, offset,
               "UniversalString has an invalid code point");
        }
      }
      break;
    case kTeletexString:
    case 7:
    case 9:
    case 13:
    case 14:
    case 21:
    case 25:
    case 26:
    case 27:
      break;
    default:
      fail(DerErrorCode::invalid_tag, offset,
           "unsupported universal DER tag " + std::to_string(node.tag.number));
  }
}

class Parser {
 public:
  Parser(ByteView input, const DerLimits& limits)
      : input_(input), limits_(limits) {}

  DerNode parse_document() {
    if (input_.empty()) {
      fail(DerErrorCode::empty_input, 0, "empty input");
    }
    std::size_t pos = 0;
    DerNode node = parse_node(pos, input_.size(), 0);
    if (pos != input_.size()) {
      fail(DerErrorCode::trailing_data, pos,
           "bytes follow the single top-level DER value");
    }
    return node;
  }

 private:
  DerNode parse_node(std::size_t& pos, std::size_t end, std::size_t depth) {
    if (depth > limits_.max_depth) {
      fail(DerErrorCode::nesting_too_deep, pos, "DER nesting limit exceeded");
    }
    if (++node_count_ > limits_.max_nodes) {
      fail(DerErrorCode::too_many_nodes, pos, "DER node limit exceeded");
    }
    const std::size_t start = pos;
    if (pos >= end) {
      fail(DerErrorCode::truncated, pos, "missing DER identifier octet");
    }

    const std::uint8_t identifier = input_[pos++];
    const std::uint8_t tag_class = identifier >> 6;
    const bool constructed = (identifier & 0x20) != 0;
    std::uint32_t number = identifier & 0x1F;

    if (number == 0x1F) {
      if (pos >= end) {
        fail(DerErrorCode::truncated, pos, "missing high-tag-number octet");
      }
      // A canonical high-tag number never starts with a zero base-128 group.
      if ((input_[pos] & 0x7F) == 0) {
        fail(DerErrorCode::non_canonical_tag, pos,
             "non-minimal high-tag-number encoding");
      }
      std::uint64_t value = 0;
      bool terminated = false;
      while (pos < end) {
        const std::uint8_t b = input_[pos++];
        if (value > (std::numeric_limits<std::uint32_t>::max() >> 7)) {
          fail(DerErrorCode::invalid_tag, pos, "tag number overflow");
        }
        value = (value << 7) | (b & 0x7F);
        if ((b & 0x80) == 0) {
          terminated = true;
          break;
        }
      }
      if (!terminated) {
        fail(DerErrorCode::truncated, pos, "unterminated high-tag-number");
      }
      if (value < 31) {
        fail(DerErrorCode::non_canonical_tag, start,
             "high-tag-number form used for a low tag");
      }
      number = static_cast<std::uint32_t>(value);
    }

    if (tag_class == kUniversal) {
      if (number == 0) {
        fail(DerErrorCode::unexpected_end_of_contents, start,
             "end-of-contents is forbidden in DER");
      }
      const auto expected_constructed = universal_constructed(number);
      if (!expected_constructed.has_value()) {
        fail(DerErrorCode::invalid_tag, start,
             "unsupported universal tag " + std::to_string(number));
      }
      if (*expected_constructed != constructed) {
        fail(DerErrorCode::non_canonical_tag, start,
             "universal tag has the wrong primitive/constructed form");
      }
    }

    if (pos >= end) {
      fail(DerErrorCode::truncated, pos, "missing DER length octet");
    }
    const std::uint8_t length_octet = input_[pos++];
    std::size_t length = 0;
    if (length_octet < 0x80) {
      length = length_octet;
    } else {
      if (length_octet == 0x80) {
        fail(DerErrorCode::indefinite_length, start,
             "indefinite-length BER encoding is not DER");
      }
      const std::size_t count = length_octet & 0x7F;
      if (count > sizeof(std::size_t)) {
        fail(DerErrorCode::length_overflow, pos, "length field is too wide");
      }
      if (pos + count > end) {
        fail(DerErrorCode::truncated, pos, "truncated long-form length");
      }
      if (input_[pos] == 0x00) {
        fail(DerErrorCode::non_minimal_length, pos,
             "long-form length has a leading zero");
      }
      std::size_t value = 0;
      for (std::size_t i = 0; i < count; ++i) {
        if (value > (std::numeric_limits<std::size_t>::max() >> 8)) {
          fail(DerErrorCode::length_overflow, pos, "length overflow");
        }
        value = (value << 8) | input_[pos++];
      }
      if (value < 0x80) {
        fail(DerErrorCode::non_minimal_length, pos,
             "long-form length used for a short value");
      }
      length = value;
    }

    if (length > limits_.max_value_size) {
      fail(DerErrorCode::value_too_large, start, "DER value exceeds limit");
    }
    const std::size_t content_start = pos;
    if (length > end - content_start) {
      fail(DerErrorCode::truncated, start,
           "value length runs past the containing DER value");
    }
    const std::size_t content_end = content_start + length;

    DerNode node;
    node.tag = DerTag{tag_class, constructed, number};
    node.encoded = input_.subspan(start, (content_end - start));
    node.content = input_.subspan(content_start, length);

    if (constructed) {
      std::size_t child_pos = content_start;
      while (child_pos < content_end) {
        node.children.push_back(parse_node(child_pos, content_end, depth + 1));
      }
      if (node.tag.tag_class == kUniversal && number == kSet) {
        for (std::size_t i = 1; i < node.children.size(); ++i) {
          if (less_bytes(node.children[i].encoded,
                         node.children[i - 1].encoded)) {
            fail(DerErrorCode::unsorted_set, start,
                 "SET OF elements are not in DER order");
          }
        }
      }
    } else {
      validate_primitive(node, start);
    }

    pos = content_end;
    return node;
  }

  ByteView input_;
  DerLimits limits_;
  std::size_t node_count_ = 0;
};

std::string decode_attribute_text(const DerNode& value) {
  if (value.tag.tag_class != kUniversal) {
    return "#" + hex_encode(value.encoded);
  }
  switch (value.tag.number) {
    case kUtf8String:
    case kPrintableString:
    case kTeletexString:
    case kIa5String:
    case 21:
    case 25:
    case 26:
    case 27:
      return std::string(reinterpret_cast<const char*>(value.content.data()),
                         value.content.size());
    case kBmpString: {
      std::string out;
      for (std::size_t i = 0; i + 1 < value.content.size(); i += 2) {
        append_utf8(out, static_cast<std::uint32_t>(
                             (value.content[i] << 8) | value.content[i + 1]));
      }
      return out;
    }
    case kUniversalString: {
      std::string out;
      for (std::size_t i = 0; i + 3 < value.content.size(); i += 4) {
        const std::uint32_t cp =
            (static_cast<std::uint32_t>(value.content[i]) << 24) |
            (static_cast<std::uint32_t>(value.content[i + 1]) << 16) |
            (static_cast<std::uint32_t>(value.content[i + 2]) << 8) |
            static_cast<std::uint32_t>(value.content[i + 3]);
        append_utf8(out, cp);
      }
      return out;
    }
    default:
      return "#" + hex_encode(value.encoded);
  }
}

std::string escape_dn_value(std::string value) {
  std::string out;
  out.reserve(value.size());
  for (std::size_t i = 0; i < value.size(); ++i) {
    const unsigned char c = static_cast<unsigned char>(value[i]);
    if (c == ',' || c == '+' || c == '"' || c == '\\' || c == '<' ||
        c == '>' || c == ';' || c == '=') {
      out.push_back('\\');
      out.push_back(static_cast<char>(c));
    } else if (c < 0x20 || c == 0x7F) {
      out.push_back('\\');
      out.push_back(static_cast<char>('0' + (c >> 4)));
      out.push_back(static_cast<char>('0' + (c & 0x0F)));
    } else {
      out.push_back(static_cast<char>(c));
    }
  }
  if (!out.empty() && out.front() == ' ') {
    out.insert(out.begin(), '\\');
  }
  if (!out.empty() && out.back() == ' ') {
    out.insert(out.end() - 1, '\\');
  }
  if (!out.empty() && out.front() == '#') {
    out.insert(out.begin(), '\\');
  }
  return out;
}

std::string attribute_short_name(std::string_view oid) {
  if (oid == "2.5.4.3") return "CN";
  if (oid == "2.5.4.4") return "SN";
  if (oid == "2.5.4.5") return "serialNumber";
  if (oid == "2.5.4.6") return "C";
  if (oid == "2.5.4.7") return "L";
  if (oid == "2.5.4.8") return "ST";
  if (oid == "2.5.4.9") return "street";
  if (oid == "2.5.4.10") return "O";
  if (oid == "2.5.4.11") return "OU";
  if (oid == "1.2.840.113549.1.9.1") return "emailAddress";
  if (oid == "0.9.2342.19200300.100.1.25") return "DC";
  return std::string(oid);
}

void append_length_delimited(std::string& out, std::string_view value) {
  out += std::to_string(value.size());
  out.push_back(':');
  out.append(value.data(), value.size());
  out.push_back(';');
}

}  // namespace

struct AlgorithmInfo {
  std::string oid;
  ByteView parameters{};
  ByteView der{};
};

struct ParsedExtensions {
  std::vector<Extension> list;
  std::vector<GeneralName> subject_alt_names;
  BasicConstraints basic_constraints;
  KeyUsage key_usage;
};

class CertificateParser {
 public:
  CertificateParser(ByteView input, const DerLimits& limits)
      : input_(input), limits_(limits), base_(input.data()) {}

  Certificate parse(DerNode root) {
    if (!root.is_universal(kSequence) || root.children.size() != 3) {
      fail(DerErrorCode::invalid_certificate, 0,
           "Certificate must be SEQUENCE { tbs, algorithm, signature }");
    }

    Certificate cert;
    cert.root_ = std::move(root);
    cert.tbs_ = cert.root_.children[0];
    if (!cert.tbs_.is_universal(kSequence)) {
      fail(DerErrorCode::invalid_certificate, off(cert.tbs_),
           "TBSCertificate must be a SEQUENCE");
    }

    const AlgorithmInfo outer_algorithm =
        parse_algorithm(cert.root_.children[1], "signatureAlgorithm");
    const DerNode& signature_node = cert.root_.children[2];
    if (!signature_node.is_universal(kBitString)) {
      fail(DerErrorCode::invalid_certificate, off(signature_node),
           "signatureValue must be a BIT STRING");
    }
    if (signature_node.content.empty() || signature_node.content[0] != 0) {
      fail(DerErrorCode::invalid_certificate, off(signature_node),
           "certificate signature must be octet-aligned");
    }
    cert.signature_ = signature_node.content.subspan(1);
    cert.signature_algorithm_oid_ = outer_algorithm.oid;
    cert.signature_parameters_ = outer_algorithm.parameters;

    parse_tbs(cert);

    if (cert.signature_algorithm_oid_ != cert.tbs_signature_algorithm_oid_ ||
        !equal_bytes(cert.signature_parameters_,
                     cert.tbs_signature_parameters_)) {
      fail(DerErrorCode::invalid_certificate, off(cert.root_.children[1]),
           "inner and outer signature AlgorithmIdentifiers differ");
    }
    return cert;
  }

 private:
  std::size_t off(const DerNode& node) const {
    if (base_ == nullptr || node.encoded.data() == nullptr) {
      return 0;
    }
    return static_cast<std::size_t>(node.encoded.data() - base_);
  }

  [[noreturn]] void cert_fail(DerErrorCode code, const DerNode& node,
                              std::string message) const {
    fail(code, off(node), std::move(message));
  }

  void require_universal(const DerNode& node, std::uint32_t number,
                         const char* what) const {
    if (!node.is_universal(number)) {
      cert_fail(DerErrorCode::invalid_certificate, node,
                std::string(what) + " has the wrong DER tag");
    }
  }

  AlgorithmInfo parse_algorithm(const DerNode& node, const char* what) const {
    require_universal(node, kSequence, what);
    if (node.children.empty() || node.children.size() > 2) {
      cert_fail(DerErrorCode::invalid_certificate, node,
                std::string(what) + " must have one or two fields");
    }
    require_universal(node.children[0], kObjectIdentifier,
                      "algorithm identifier");
    AlgorithmInfo result;
    result.oid = decode_oid(node.children[0].content, off(node.children[0]));
    if (node.children.size() == 2) {
      result.parameters = node.children[1].encoded;
    }
    result.der = node.encoded;
    return result;
  }

  std::uint64_t parse_nonnegative_integer(const DerNode& node,
                                          const char* what) const {
    require_universal(node, kInteger, what);
    ByteView content = node.content;
    if (content.empty() || (content[0] & 0x80) != 0) {
      cert_fail(DerErrorCode::invalid_certificate, node,
                std::string(what) + " must be non-negative");
    }
    std::size_t i = 0;
    if (content[0] == 0) {
      i = 1;
    }
    if (i == content.size()) {
      return 0;
    }
    std::uint64_t value = 0;
    for (; i < content.size(); ++i) {
      if (value > (std::numeric_limits<std::uint64_t>::max() >> 8)) {
        cert_fail(DerErrorCode::invalid_certificate, node,
                  std::string(what) + " is too large");
      }
      value = (value << 8) | content[i];
    }
    return value;
  }

  std::string parse_serial(const DerNode& node) const {
    require_universal(node, kInteger, "serialNumber");
    ByteView content = node.content;
    if (content.empty() || (content[0] & 0x80) != 0) {
      cert_fail(DerErrorCode::invalid_certificate, node,
                "serialNumber must be a positive INTEGER");
    }
    std::size_t start = 0;
    if (content[0] == 0) {
      start = 1;
    }
    const std::size_t significant = content.size() - start;
    if (significant == 0) {
      cert_fail(DerErrorCode::invalid_certificate, node,
                "serialNumber must not be zero");
    }
    if (significant > 20) {
      cert_fail(DerErrorCode::invalid_certificate, node,
                "serialNumber exceeds 20 octets");
    }
    return hex_encode(content.subspan(start));
  }

  Name parse_name(const DerNode& node, const char* what) const {
    require_universal(node, kSequence, what);
    Name name;
    name.der = node.encoded;
    for (const DerNode& rdn_node : node.children) {
      require_universal(rdn_node, kSet, "RelativeDistinguishedName");
      if (rdn_node.children.empty()) {
        cert_fail(DerErrorCode::invalid_certificate, rdn_node,
                  "RelativeDistinguishedName must not be empty");
      }
      Rdn rdn;
      for (const DerNode& atv : rdn_node.children) {
        require_universal(atv, kSequence, "AttributeTypeAndValue");
        if (atv.children.size() != 2) {
          cert_fail(DerErrorCode::invalid_certificate, atv,
                    "AttributeTypeAndValue must have two fields");
        }
        require_universal(atv.children[0], kObjectIdentifier,
                          "attribute type");
        NameAttribute attribute;
        attribute.oid =
            decode_oid(atv.children[0].content, off(atv.children[0]));
        attribute.value_der = atv.children[1].encoded;
        attribute.text = decode_attribute_text(atv.children[1]);
        rdn.attributes.push_back(std::move(attribute));
      }
      name.rdns.push_back(std::move(rdn));
    }
    return name;
  }

  std::string parse_time(const DerNode& node, const char* what) const {
    if (!node.is_universal(kUtcTime) && !node.is_universal(kGeneralizedTime)) {
      cert_fail(DerErrorCode::invalid_certificate, node,
                std::string(what) + " must be UTCTime or GeneralizedTime");
    }
    return std::string(reinterpret_cast<const char*>(node.content.data()),
                       node.content.size());
  }

  static bool algorithm_equal(const AlgorithmInfo& a, const AlgorithmInfo& b) {
    return a.oid == b.oid && equal_bytes(a.parameters, b.parameters);
  }

  static std::string format_ip(ByteView bytes) {
    if (bytes.size() == 4) {
      std::string out;
      for (std::size_t i = 0; i < 4; ++i) {
        if (i != 0) out.push_back('.');
        out += std::to_string(bytes[i]);
      }
      return out;
    }
    std::array<std::uint16_t, 8> words{};
    for (std::size_t i = 0; i < 8; ++i) {
      words[i] = static_cast<std::uint16_t>((bytes[i * 2] << 8) |
                                            bytes[i * 2 + 1]);
    }
    // RFC 5952-style longest zero run compression, sufficient for an
    // unambiguous display value.
    std::size_t best_start = 8;
    std::size_t best_len = 0;
    for (std::size_t i = 0; i < 8;) {
      if (words[i] != 0) {
        ++i;
        continue;
      }
      std::size_t j = i;
      while (j < 8 && words[j] == 0) ++j;
      if (j - i > best_len && j - i >= 2) {
        best_start = i;
        best_len = j - i;
      }
      i = j;
    }
    std::ostringstream out;
    out << std::hex;
    for (std::size_t i = 0; i < 8; ++i) {
      if (i == best_start) {
        out << "::";
        i += best_len - 1;
        continue;
      }
      if (i != 0 && i != best_start + best_len) out << ':';
      out << words[i];
    }
    std::string text = out.str();
    if (text.find("::") == std::string::npos && best_len != 0) {
      text = "::";
    }
    return text;
  }

  std::vector<GeneralName> parse_san(const DerNode& tree) const {
    require_universal(tree, kSequence, "subjectAltName");
    if (tree.children.empty()) {
      cert_fail(DerErrorCode::invalid_extension, tree,
                "subjectAltName must not be empty");
    }
    std::vector<GeneralName> result;
    for (const DerNode& entry : tree.children) {
      if (entry.tag.tag_class != kContext || entry.tag.number > 8) {
        cert_fail(DerErrorCode::invalid_extension, entry,
                  "invalid GeneralName tag");
      }
      static constexpr bool constructed_by_tag[9] = {
          true, false, false, true, true, true, false, false, false};
      if (entry.tag.constructed != constructed_by_tag[entry.tag.number]) {
        cert_fail(DerErrorCode::invalid_extension, entry,
                  "GeneralName has the wrong primitive/constructed form");
      }
      GeneralName name;
      name.tag = entry.tag.number;
      name.der = entry.encoded;
      switch (entry.tag.number) {
        case 0:
          name.kind = GeneralNameKind::other_name;
          name.value = "#" + hex_encode(entry.content);
          break;
        case 1:
        case 2:
        case 6: {
          if (entry.content.empty() || !is_ascii(entry.content) ||
              std::find(entry.content.begin(), entry.content.end(), 0) !=
                  entry.content.end()) {
            cert_fail(DerErrorCode::invalid_extension, entry,
                      "GeneralName IA5 value is invalid");
          }
          name.kind = entry.tag.number == 1
                          ? GeneralNameKind::rfc822_name
                          : (entry.tag.number == 2 ? GeneralNameKind::dns_name
                                                   : GeneralNameKind::uri);
          name.value.assign(
              reinterpret_cast<const char*>(entry.content.data()),
              entry.content.size());
          break;
        }
        case 3:
          name.kind = GeneralNameKind::x400_address;
          name.value = "#" + hex_encode(entry.content);
          break;
        case 4: {
          if (entry.children.size() != 1 ||
              !entry.children[0].is_universal(kSequence)) {
            cert_fail(DerErrorCode::invalid_extension, entry,
                      "directoryName must contain one Name");
          }
          name.kind = GeneralNameKind::directory_name;
          name.value = parse_name(entry.children[0], "directoryName")
                           .canonical();
          break;
        }
        case 5:
          name.kind = GeneralNameKind::edi_party_name;
          name.value = "#" + hex_encode(entry.content);
          break;
        case 7:
          if (entry.content.size() != 4 && entry.content.size() != 16) {
            cert_fail(DerErrorCode::invalid_extension, entry,
                      "iPAddress must contain 4 or 16 octets");
          }
          name.kind = GeneralNameKind::ip_address;
          name.value = format_ip(entry.content);
          break;
        case 8:
          name.kind = GeneralNameKind::registered_id;
          name.value = decode_oid(entry.content, off(entry));
          break;
      }
      result.push_back(std::move(name));
    }
    return result;
  }

  BasicConstraints parse_basic_constraints(const DerNode& tree) const {
    require_universal(tree, kSequence, "basicConstraints");
    BasicConstraints result;
    result.present = true;
    std::size_t i = 0;
    if (i < tree.children.size() && tree.children[i].is_universal(kBoolean)) {
      if (tree.children[i].content[0] != 0xFF) {
        cert_fail(DerErrorCode::non_canonical_tag, tree.children[i],
                  "basicConstraints cA=FALSE is the DEFAULT and must be "
                  "omitted in DER");
      }
      result.ca = true;
      ++i;
    }
    if (i < tree.children.size() && tree.children[i].is_universal(kInteger)) {
      result.path_length =
          parse_nonnegative_integer(tree.children[i], "pathLenConstraint");
      ++i;
    }
    if (i != tree.children.size()) {
      cert_fail(DerErrorCode::invalid_extension, tree,
                "unexpected field in basicConstraints");
    }
    if (result.path_length.has_value() && !result.ca) {
      cert_fail(DerErrorCode::invalid_extension, tree,
                "pathLenConstraint requires cA=TRUE");
    }
    return result;
  }

  KeyUsage parse_key_usage(const DerNode& tree) const {
    require_universal(tree, kBitString, "keyUsage");
    KeyUsage result;
    result.present = true;
    const ByteView content = tree.content;
    const std::size_t bit_count =
        content.size() <= 1 ? 0 : (content.size() - 1) * 8 - content[0];
    if (bit_count > 9) {
      cert_fail(DerErrorCode::invalid_extension, tree,
                "keyUsage has more than nine named bits");
    }
    for (std::size_t bit = 0; bit < bit_count; ++bit) {
      const std::size_t byte = 1 + bit / 8;
      const unsigned shift = 7 - static_cast<unsigned>(bit % 8);
      if ((content[byte] & (1U << shift)) != 0) {
        result.bits = static_cast<std::uint16_t>(result.bits | (1U << bit));
      }
    }
    return result;
  }

  ParsedExtensions parse_extensions(const DerNode& wrapper) const {
    if (wrapper.tag.tag_class != kContext || wrapper.tag.number != 3 ||
        !wrapper.tag.constructed || wrapper.children.size() != 1) {
      cert_fail(DerErrorCode::invalid_certificate, wrapper,
                "extensions must be [3] EXPLICIT containing one SEQUENCE");
    }
    const DerNode& sequence = wrapper.children[0];
    require_universal(sequence, kSequence, "Extensions");
    if (sequence.children.empty()) {
      cert_fail(DerErrorCode::invalid_extension, sequence,
                "Extensions must not be empty");
    }

    ParsedExtensions result;
    std::unordered_set<std::string> seen;
    for (const DerNode& node : sequence.children) {
      require_universal(node, kSequence, "Extension");
      if (node.children.size() < 2 || node.children.size() > 3) {
        cert_fail(DerErrorCode::invalid_extension, node,
                  "Extension must have two or three fields");
      }
      require_universal(node.children[0], kObjectIdentifier,
                        "extension OID");
      Extension extension;
      extension.oid = decode_oid(node.children[0].content,
                                 off(node.children[0]));
      extension.der = node.encoded;

      std::size_t i = 1;
      if (i < node.children.size() && node.children[i].is_universal(kBoolean)) {
        if (node.children[i].content[0] != 0xFF) {
          cert_fail(DerErrorCode::non_canonical_tag, node.children[i],
                    "extension critical=FALSE is the DEFAULT and must be "
                    "omitted in DER");
        }
        extension.critical = true;
        ++i;
      }
      if (i >= node.children.size() ||
          !node.children[i].is_universal(kOctetString)) {
        cert_fail(DerErrorCode::invalid_extension, node,
                  "Extension extnValue must be an OCTET STRING");
      }
      ++i;
      if (i != node.children.size()) {
        cert_fail(DerErrorCode::invalid_extension, node,
                  "unexpected field in Extension");
      }
      extension.value = node.children[node.children.size() - 1].content;

      if (!seen.insert(extension.oid).second) {
        cert_fail(DerErrorCode::duplicate_extension, node,
                  "duplicate extension OID " + extension.oid);
      }

      if (extension.oid == "2.5.29.17") {
        extension.decoded = StrictDer::parse(extension.value, limits_);
        const auto names = parse_san(*extension.decoded);
        result.subject_alt_names.insert(result.subject_alt_names.end(),
                                        names.begin(), names.end());
      } else if (extension.oid == "2.5.29.19") {
        extension.decoded = StrictDer::parse(extension.value, limits_);
        result.basic_constraints = parse_basic_constraints(*extension.decoded);
      } else if (extension.oid == "2.5.29.15") {
        extension.decoded = StrictDer::parse(extension.value, limits_);
        result.key_usage = parse_key_usage(*extension.decoded);
      }
      result.list.push_back(std::move(extension));
    }
    return result;
  }

  void parse_tbs(Certificate& cert) const {
    const auto& fields = cert.tbs_.children;
    std::size_t i = 0;
    cert.version_ = 0;
    if (i < fields.size() && fields[i].tag.tag_class == kContext &&
        fields[i].tag.number == 0 && fields[i].tag.constructed) {
      if (fields[i].children.size() != 1 ||
          !fields[i].children[0].is_universal(kInteger)) {
        cert_fail(DerErrorCode::invalid_certificate, fields[i],
                  "version must be [0] EXPLICIT INTEGER");
      }
      cert.version_ = parse_nonnegative_integer(fields[i].children[0],
                                                "version");
      if (cert.version_ == 0) {
        cert_fail(DerErrorCode::non_canonical_tag, fields[i],
                  "explicit v1 version is the DEFAULT and must be omitted");
      }
      if (cert.version_ > 2) {
        cert_fail(DerErrorCode::invalid_certificate, fields[i],
                  "unsupported certificate version");
      }
      ++i;
    }

    if (i >= fields.size()) {
      cert_fail(DerErrorCode::invalid_certificate, cert.tbs_,
                "TBSCertificate is missing serialNumber");
    }
    cert.serial_number_ = parse_serial(fields[i++]);

    if (i >= fields.size()) {
      cert_fail(DerErrorCode::invalid_certificate, cert.tbs_,
                "TBSCertificate is missing signature");
    }
    const AlgorithmInfo tbs_algorithm =
        parse_algorithm(fields[i++], "TBSCertificate.signature");
    cert.tbs_signature_algorithm_oid_ = tbs_algorithm.oid;
    cert.tbs_signature_parameters_ = tbs_algorithm.parameters;

    if (i >= fields.size()) {
      cert_fail(DerErrorCode::invalid_certificate, cert.tbs_,
                "TBSCertificate is missing issuer");
    }
    cert.issuer_ = parse_name(fields[i++], "issuer");

    if (i >= fields.size()) {
      cert_fail(DerErrorCode::invalid_certificate, cert.tbs_,
                "TBSCertificate is missing validity");
    }
    require_universal(fields[i], kSequence, "validity");
    if (fields[i].children.size() != 2) {
      cert_fail(DerErrorCode::invalid_certificate, fields[i],
                "validity must have exactly two times");
    }
    cert.not_before_ = parse_time(fields[i].children[0], "notBefore");
    cert.not_after_ = parse_time(fields[i].children[1], "notAfter");
    ++i;

    if (i >= fields.size()) {
      cert_fail(DerErrorCode::invalid_certificate, cert.tbs_,
                "TBSCertificate is missing subject");
    }
    cert.subject_ = parse_name(fields[i++], "subject");

    if (i >= fields.size()) {
      cert_fail(DerErrorCode::invalid_certificate, cert.tbs_,
                "TBSCertificate is missing subjectPublicKeyInfo");
    }
    require_universal(fields[i], kSequence, "subjectPublicKeyInfo");
    if (fields[i].children.size() != 2 ||
        !fields[i].children[0].is_universal(kSequence) ||
        !fields[i].children[1].is_universal(kBitString)) {
      cert_fail(DerErrorCode::invalid_certificate, fields[i],
                "subjectPublicKeyInfo is malformed");
    }
    (void)parse_algorithm(fields[i].children[0], "subjectPublicKeyAlgorithm");
    cert.subject_public_key_info_ = fields[i].encoded;
    ++i;

    bool issuer_uid = false;
    bool subject_uid = false;
    if (i < fields.size() && fields[i].tag.tag_class == kContext &&
        fields[i].tag.number == 1) {
      if (fields[i].tag.constructed) {
        cert_fail(DerErrorCode::invalid_certificate, fields[i],
                  "issuerUniqueID must be IMPLICIT BIT STRING");
      }
      validate_bit_string(fields[i].content, off(fields[i]));
      issuer_uid = true;
      ++i;
    }
    if (i < fields.size() && fields[i].tag.tag_class == kContext &&
        fields[i].tag.number == 2) {
      if (fields[i].tag.constructed) {
        cert_fail(DerErrorCode::invalid_certificate, fields[i],
                  "subjectUniqueID must be IMPLICIT BIT STRING");
      }
      validate_bit_string(fields[i].content, off(fields[i]));
      subject_uid = true;
      ++i;
    }

    bool extensions_present = false;
    if (i < fields.size() && fields[i].tag.tag_class == kContext &&
        fields[i].tag.number == 3) {
      ParsedExtensions parsed = parse_extensions(fields[i]);
      cert.extensions_ = std::move(parsed.list);
      cert.subject_alt_names_ = std::move(parsed.subject_alt_names);
      cert.basic_constraints_ = parsed.basic_constraints;
      cert.key_usage_ = parsed.key_usage;
      extensions_present = true;
      ++i;
    }
    if (i != fields.size()) {
      cert_fail(DerErrorCode::invalid_certificate, fields[i],
                "unexpected trailing field in TBSCertificate");
    }
    if (extensions_present && cert.version_ != 2) {
      cert_fail(DerErrorCode::invalid_certificate, cert.tbs_,
                "extensions require v3");
    }
    if ((issuer_uid || subject_uid) && cert.version_ == 0) {
      cert_fail(DerErrorCode::invalid_certificate, cert.tbs_,
                "unique identifiers require v2 or v3");
    }
  }

  ByteView input_;
  DerLimits limits_;
  const std::uint8_t* base_;
};

const char* to_string(DerErrorCode code) noexcept {
  switch (code) {
    case DerErrorCode::empty_input: return "empty_input";
    case DerErrorCode::truncated: return "truncated";
    case DerErrorCode::trailing_data: return "trailing_data";
    case DerErrorCode::unexpected_end_of_contents:
      return "unexpected_end_of_contents";
    case DerErrorCode::invalid_tag: return "invalid_tag";
    case DerErrorCode::non_canonical_tag: return "non_canonical_tag";
    case DerErrorCode::indefinite_length: return "indefinite_length";
    case DerErrorCode::non_minimal_length: return "non_minimal_length";
    case DerErrorCode::length_overflow: return "length_overflow";
    case DerErrorCode::value_too_large: return "value_too_large";
    case DerErrorCode::nesting_too_deep: return "nesting_too_deep";
    case DerErrorCode::too_many_nodes: return "too_many_nodes";
    case DerErrorCode::invalid_boolean: return "invalid_boolean";
    case DerErrorCode::invalid_integer: return "invalid_integer";
    case DerErrorCode::invalid_bit_string: return "invalid_bit_string";
    case DerErrorCode::invalid_null: return "invalid_null";
    case DerErrorCode::invalid_oid: return "invalid_oid";
    case DerErrorCode::invalid_string: return "invalid_string";
    case DerErrorCode::invalid_time: return "invalid_time";
    case DerErrorCode::unsorted_set: return "unsorted_set";
    case DerErrorCode::invalid_certificate: return "invalid_certificate";
    case DerErrorCode::duplicate_extension: return "duplicate_extension";
    case DerErrorCode::invalid_extension: return "invalid_extension";
    case DerErrorCode::invalid_public_key: return "invalid_public_key";
    case DerErrorCode::signature_mismatch: return "signature_mismatch";
  }
  return "unknown";
}

const char* to_string(GeneralNameKind kind) noexcept {
  switch (kind) {
    case GeneralNameKind::other_name: return "otherName";
    case GeneralNameKind::rfc822_name: return "rfc822Name";
    case GeneralNameKind::dns_name: return "dNSName";
    case GeneralNameKind::x400_address: return "x400Address";
    case GeneralNameKind::directory_name: return "directoryName";
    case GeneralNameKind::edi_party_name: return "ediPartyName";
    case GeneralNameKind::uri: return "uniformResourceIdentifier";
    case GeneralNameKind::ip_address: return "iPAddress";
    case GeneralNameKind::registered_id: return "registeredID";
  }
  return "unknown";
}

DerError::DerError(DerErrorCode code, std::size_t offset, std::string message)
    : std::runtime_error(std::string(to_string(code)) + " at byte " +
                         std::to_string(offset) + ": " + message),
      code_(code),
      offset_(offset) {}

bool DerNode::is_universal(std::uint32_t number) const noexcept {
  return tag.tag_class == DerTag::universal && tag.number == number;
}

bool DerNode::is_context(std::uint32_t number, bool constructed) const noexcept {
  return tag.tag_class == DerTag::context_specific && tag.number == number &&
         tag.constructed == constructed;
}

DerNode StrictDer::parse(ByteView input, const DerLimits& limits) {
  return Parser(input, limits).parse_document();
}

std::string Name::text() const {
  std::string out;
  bool first_rdn = true;
  for (const Rdn& rdn : rdns) {
    if (!first_rdn) {
      out.push_back(',');
    }
    first_rdn = false;
    bool first_attr = true;
    for (const NameAttribute& attribute : rdn.attributes) {
      if (!first_attr) {
        out.push_back('+');
      }
      first_attr = false;
      out += attribute_short_name(attribute.oid);
      out.push_back('=');
      out += escape_dn_value(attribute.text);
    }
  }
  return out;
}

std::string Name::canonical() const {
  std::string out;
  bool first_rdn = true;
  for (const Rdn& rdn : rdns) {
    if (!first_rdn) {
      out.push_back('|');
    }
    first_rdn = false;
    bool first_attr = true;
    for (const NameAttribute& attribute : rdn.attributes) {
      if (!first_attr) {
        out.push_back('+');
      }
      first_attr = false;
      append_length_delimited(out, attribute.oid);
      out.push_back('=');
      append_length_delimited(out, hex_encode(attribute.value_der));
    }
  }
  return out;
}

std::string CertificateIdentity::canonical() const {
  std::string out;
  out += "subject=";
  append_length_delimited(out, subject);
  out += "san.count=";
  append_length_delimited(out, std::to_string(subject_alt_names.size()));
  for (const std::string& name : subject_alt_names) {
    out += "san=";
    append_length_delimited(out, name);
  }
  out += "basicConstraints.ca=";
  append_length_delimited(out, basic_constraints_ca ? "1" : "0");
  out += "basicConstraints.pathLen=";
  append_length_delimited(out, path_length.has_value()
                                   ? std::to_string(*path_length)
                                   : std::string("-"));
  out += "keyUsage=";
  append_length_delimited(out, std::to_string(key_usage_bits));
  return out;
}

Certificate Certificate::parse(ByteView der, const DerLimits& limits) {
  auto encoded = std::make_shared<Bytes>(der.begin(), der.end());
  DerNode root = StrictDer::parse(*encoded, limits);
  CertificateParser parser(*encoded, limits);
  Certificate cert = parser.parse(std::move(root));
  cert.encoded_ = std::move(encoded);
  return cert;
}

ByteView Certificate::tbs_der() const noexcept { return tbs_.encoded; }

ByteView Certificate::signature() const noexcept { return signature_; }

const Extension* Certificate::find_extension(std::string_view oid) const
    noexcept {
  for (const Extension& extension : extensions_) {
    if (extension.oid == oid) {
      return &extension;
    }
  }
  return nullptr;
}

bool Certificate::has_unsupported_critical_extensions() const {
  for (const Extension& extension : extensions_) {
    if (!extension.critical) {
      continue;
    }
    if (extension.oid != "2.5.29.17" && extension.oid != "2.5.29.19" &&
        extension.oid != "2.5.29.15") {
      return true;
    }
  }
  return false;
}

CertificateIdentity Certificate::identity() const {
  CertificateIdentity result;
  result.subject = subject_.canonical();
  result.subject_alt_names.reserve(subject_alt_names_.size());
  for (const GeneralName& name : subject_alt_names_) {
    result.subject_alt_names.push_back(std::string(to_string(name.kind)) +
                                       ":" + name.value);
  }
  result.basic_constraints_ca = basic_constraints_.ca;
  result.path_length = basic_constraints_.path_length;
  result.key_usage_bits = key_usage_.bits;
  return result;
}

bool Certificate::verify_signature(SignatureVerifier& verifier) const {
  const SignatureRequest request{
      signature_algorithm_oid_, signature_parameters_, tbs_.encoded,
      signature_, subject_public_key_info_};
  return verifier.verify(request);
}

bool Certificate::verify_signature_with_key(
    SignatureVerifier& verifier, ByteView verification_key_spki) const {
  const SignatureRequest request{signature_algorithm_oid_,
                                 signature_parameters_, tbs_.encoded,
                                 signature_, verification_key_spki};
  return verifier.verify(request);
}

namespace {
namespace rsa {

using Limb = std::uint32_t;
using DWord = std::uint64_t;

void trim(std::vector<Limb>& value) {
  while (!value.empty() && value.back() == 0) {
    value.pop_back();
  }
}

std::vector<Limb> from_be(ByteView bytes) {
  std::vector<Limb> out((bytes.size() + 3) / 4, 0);
  for (std::size_t i = 0; i < bytes.size(); ++i) {
    const std::size_t from_end = bytes.size() - 1 - i;
    out[from_end / 4] |= static_cast<Limb>(bytes[i])
                         << (8 * static_cast<unsigned>(from_end % 4));
  }
  trim(out);
  return out;
}

std::vector<std::uint8_t> to_be(const std::vector<Limb>& value,
                                std::size_t length) {
  std::vector<std::uint8_t> out(length, 0);
  std::size_t pos = length;
  for (Limb limb : value) {
    for (unsigned i = 0; i < 4; ++i) {
      const std::uint8_t b = static_cast<std::uint8_t>(limb & 0xFF);
      if (pos != 0) {
        out[--pos] = b;
      } else if (b != 0) {
        return {};
      }
      limb >>= 8;
    }
  }
  return out;
}

int compare(const std::vector<Limb>& a, const std::vector<Limb>& b) {
  if (a.size() != b.size()) {
    return a.size() < b.size() ? -1 : 1;
  }
  for (std::size_t i = a.size(); i != 0; --i) {
    if (a[i - 1] != b[i - 1]) {
      return a[i - 1] < b[i - 1] ? -1 : 1;
    }
  }
  return 0;
}

int compare_fixed(const std::vector<Limb>& a, const std::vector<Limb>& b) {
  for (std::size_t i = a.size(); i != 0; --i) {
    if (a[i - 1] != b[i - 1]) {
      return a[i - 1] < b[i - 1] ? -1 : 1;
    }
  }
  return 0;
}

std::size_t bit_length(const std::vector<Limb>& value) {
  if (value.empty()) {
    return 0;
  }
  Limb top = value.back();
  std::size_t bits = (value.size() - 1) * 32;
  while (top != 0) {
    ++bits;
    top >>= 1;
  }
  return bits;
}

bool bit(const std::vector<Limb>& value, std::size_t index) {
  const std::size_t word = index / 32;
  if (word >= value.size()) {
    return false;
  }
  return ((value[word] >> (index % 32)) & 1U) != 0;
}

// Montgomery arithmetic modulo an odd RSA modulus.  All values inside the
// context have exactly k limbs, which keeps the CIOS loop simple.
class Montgomery {
 public:
  explicit Montgomery(std::vector<Limb> modulus) : n_(std::move(modulus)) {
    trim(n_);
    k_ = n_.size();
    if (k_ == 0 || (n_[0] & 1U) == 0) {
      throw DerError(DerErrorCode::invalid_public_key, 0,
                     "RSA modulus must be odd and non-zero");
    }
    Limb inverse = 1;
    for (int i = 0; i < 5; ++i) {
      inverse = static_cast<Limb>(inverse * (2U - n_[0] * inverse));
    }
    n0_inverse_ = static_cast<Limb>(0U - inverse);

    std::vector<Limb> r(k_, 0);
    r[0] = 1;
    for (std::size_t i = 0; i < 32 * k_; ++i) {
      double_mod(r);
    }
    one_ = r;
  }

  std::vector<Limb> one() const { return one_; }

  std::vector<Limb> to_mont(const std::vector<Limb>& value) const {
    std::vector<Limb> result(k_, 0);
    for (std::size_t i = 0; i < value.size() && i < k_; ++i) {
      result[i] = value[i];
    }
    for (std::size_t i = 0; i < 32 * k_; ++i) {
      double_mod(result);
    }
    return result;
  }

  std::vector<Limb> from_mont(const std::vector<Limb>& value) const {
    std::vector<Limb> one(k_, 0);
    one[0] = 1;
    return multiply(value, one);
  }

  std::vector<Limb> multiply(const std::vector<Limb>& a,
                             const std::vector<Limb>& b) const {
    std::vector<Limb> t(k_ + 2, 0);
    for (std::size_t i = 0; i < k_; ++i) {
      DWord carry = 0;
      for (std::size_t j = 0; j < k_; ++j) {
        const DWord sum = static_cast<DWord>(t[j]) +
                          static_cast<DWord>(a[j]) * b[i] + carry;
        t[j] = static_cast<Limb>(sum);
        carry = sum >> 32;
      }
      DWord sum = static_cast<DWord>(t[k_]) + carry;
      t[k_] = static_cast<Limb>(sum);
      t[k_ + 1] = static_cast<Limb>(sum >> 32);

      const Limb m = static_cast<Limb>(t[0] * n0_inverse_);
      carry = 0;
      for (std::size_t j = 0; j < k_; ++j) {
        const DWord add = static_cast<DWord>(t[j]) +
                          static_cast<DWord>(m) * n_[j] + carry;
        t[j] = static_cast<Limb>(add);
        carry = add >> 32;
      }
      sum = static_cast<DWord>(t[k_]) + carry;
      t[k_] = static_cast<Limb>(sum);
      t[k_ + 1] = static_cast<Limb>(t[k_ + 1] + (sum >> 32));

      for (std::size_t j = 0; j < k_; ++j) {
        t[j] = t[j + 1];
      }
      t[k_] = t[k_ + 1];
      t[k_ + 1] = 0;
    }

    std::vector<Limb> result(t.begin(), t.begin() + k_);
    if (t[k_] != 0 || compare_fixed(result, n_) >= 0) {
      DWord borrow = 0;
      for (std::size_t j = 0; j < k_; ++j) {
        const DWord sub = static_cast<DWord>(n_[j]) + borrow;
        const DWord current = result[j];
        result[j] = static_cast<Limb>(current - sub);
        borrow = current < sub ? 1 : 0;
      }
    }
    return result;
  }

 private:
  void double_mod(std::vector<Limb>& value) const {
    Limb carry = 0;
    for (std::size_t i = 0; i < k_; ++i) {
      const DWord doubled = (static_cast<DWord>(value[i]) << 1) | carry;
      value[i] = static_cast<Limb>(doubled);
      carry = static_cast<Limb>(doubled >> 32);
    }
    if (carry != 0 || compare_fixed(value, n_) >= 0) {
      DWord borrow = 0;
      for (std::size_t i = 0; i < k_; ++i) {
        const DWord sub = static_cast<DWord>(n_[i]) + borrow;
        const DWord current = value[i];
        value[i] = static_cast<Limb>(current - sub);
        borrow = current < sub ? 1 : 0;
      }
    }
  }

  std::vector<Limb> n_;
  std::size_t k_ = 0;
  Limb n0_inverse_ = 0;
  std::vector<Limb> one_;
};

std::vector<Limb> pow_mod(const std::vector<Limb>& base,
                          const std::vector<Limb>& exponent,
                          const std::vector<Limb>& modulus) {
  Montgomery context(modulus);
  std::vector<Limb> result = context.one();
  const std::vector<Limb> mont_base = context.to_mont(base);
  const std::size_t bits = bit_length(exponent);
  for (std::size_t i = bits; i != 0; --i) {
    result = context.multiply(result, result);
    if (bit(exponent, i - 1)) {
      result = context.multiply(result, mont_base);
    }
  }
  return context.from_mont(result);
}

std::uint32_t rotate_right(std::uint32_t value, unsigned count) {
  return (value >> count) | (value << (32 - count));
}

std::uint32_t rotate_left(std::uint32_t value, unsigned count) {
  return (value << count) | (value >> (32 - count));
}

std::uint64_t rotate_right64(std::uint64_t value, unsigned count) {
  return (value >> count) | (value << (64 - count));
}

std::array<std::uint8_t, 20> sha1(ByteView message) {
  static constexpr std::uint32_t k[4] = {0x5A827999U, 0x6ED9EBA1U,
                                         0x8F1BBCDCU, 0xCA62C1D6U};
  std::uint32_t h0 = 0x67452301U;
  std::uint32_t h1 = 0xEFCDAB89U;
  std::uint32_t h2 = 0x98BADCFEU;
  std::uint32_t h3 = 0x10325476U;
  std::uint32_t h4 = 0xC3D2E1F0U;

  Bytes padded(message.begin(), message.end());
  padded.push_back(0x80);
  while ((padded.size() % 64) != 56) {
    padded.push_back(0);
  }
  const std::uint64_t bit_count =
      static_cast<std::uint64_t>(message.size()) * 8;
  for (int i = 7; i >= 0; --i) {
    padded.push_back(static_cast<std::uint8_t>(bit_count >> (i * 8)));
  }

  for (std::size_t block = 0; block < padded.size(); block += 64) {
    std::array<std::uint32_t, 80> w{};
    for (std::size_t i = 0; i < 16; ++i) {
      w[i] = (static_cast<std::uint32_t>(padded[block + i * 4]) << 24) |
             (static_cast<std::uint32_t>(padded[block + i * 4 + 1]) << 16) |
             (static_cast<std::uint32_t>(padded[block + i * 4 + 2]) << 8) |
             static_cast<std::uint32_t>(padded[block + i * 4 + 3]);
    }
    for (std::size_t i = 16; i < 80; ++i) {
      w[i] = rotate_left(w[i - 3] ^ w[i - 8] ^ w[i - 14] ^ w[i - 16], 1);
    }
    std::uint32_t a = h0;
    std::uint32_t b = h1;
    std::uint32_t c = h2;
    std::uint32_t d = h3;
    std::uint32_t e = h4;
    for (std::size_t i = 0; i < 80; ++i) {
      std::uint32_t f = 0;
      std::uint32_t kk = 0;
      if (i < 20) {
        f = (b & c) | ((~b) & d);
        kk = k[0];
      } else if (i < 40) {
        f = b ^ c ^ d;
        kk = k[1];
      } else if (i < 60) {
        f = (b & c) | (b & d) | (c & d);
        kk = k[2];
      } else {
        f = b ^ c ^ d;
        kk = k[3];
      }
      const std::uint32_t temp =
          rotate_left(a, 5) + f + e + kk + w[i];
      e = d;
      d = c;
      c = rotate_left(b, 30);
      b = a;
      a = temp;
    }
    h0 += a;
    h1 += b;
    h2 += c;
    h3 += d;
    h4 += e;
  }
  std::array<std::uint8_t, 20> out{};
  const std::uint32_t words[5] = {h0, h1, h2, h3, h4};
  for (std::size_t i = 0; i < 5; ++i) {
    out[i * 4] = static_cast<std::uint8_t>(words[i] >> 24);
    out[i * 4 + 1] = static_cast<std::uint8_t>(words[i] >> 16);
    out[i * 4 + 2] = static_cast<std::uint8_t>(words[i] >> 8);
    out[i * 4 + 3] = static_cast<std::uint8_t>(words[i]);
  }
  return out;
}

std::array<std::uint8_t, 32> sha256(ByteView message) {
  static constexpr std::uint32_t k[64] = {
      0x428A2F98U, 0x71374491U, 0xB5C0FBCFU, 0xE9B5DBA5U,
      0x3956C25BU, 0x59F111F1U, 0x923F82A4U, 0xAB1C5ED5U,
      0xD807AA98U, 0x12835B01U, 0x243185BEU, 0x550C7DC3U,
      0x72BE5D74U, 0x80DEB1FEU, 0x9BDC06A7U, 0xC19BF174U,
      0xE49B69C1U, 0xEFBE4786U, 0x0FC19DC6U, 0x240CA1CCU,
      0x2DE92C6FU, 0x4A7484AAU, 0x5CB0A9DCU, 0x76F988DAU,
      0x983E5152U, 0xA831C66DU, 0xB00327C8U, 0xBF597FC7U,
      0xC6E00BF3U, 0xD5A79147U, 0x06CA6351U, 0x14292967U,
      0x27B70A85U, 0x2E1B2138U, 0x4D2C6DFCU, 0x53380D13U,
      0x650A7354U, 0x766A0ABBU, 0x81C2C92EU, 0x92722C85U,
      0xA2BFE8A1U, 0xA81A664BU, 0xC24B8B70U, 0xC76C51A3U,
      0xD192E819U, 0xD6990624U, 0xF40E3585U, 0x106AA070U,
      0x19A4C116U, 0x1E376C08U, 0x2748774CU, 0x34B0BCB5U,
      0x391C0CB3U, 0x4ED8AA4AU, 0x5B9CCA4FU, 0x682E6FF3U,
      0x748F82EEU, 0x78A5636FU, 0x84C87814U, 0x8CC70208U,
      0x90BEFFFAU, 0xA4506CEBU, 0xBEF9A3F7U, 0xC67178F2U};
  std::uint32_t h[8] = {0x6A09E667U, 0xBB67AE85U, 0x3C6EF372U, 0xA54FF53AU,
                        0x510E527FU, 0x9B05688CU, 0x1F83D9ABU, 0x5BE0CD19U};

  Bytes padded(message.begin(), message.end());
  padded.push_back(0x80);
  while ((padded.size() % 64) != 56) {
    padded.push_back(0);
  }
  const std::uint64_t bit_count =
      static_cast<std::uint64_t>(message.size()) * 8;
  for (int i = 7; i >= 0; --i) {
    padded.push_back(static_cast<std::uint8_t>(bit_count >> (i * 8)));
  }

  for (std::size_t block = 0; block < padded.size(); block += 64) {
    std::array<std::uint32_t, 64> w{};
    for (std::size_t i = 0; i < 16; ++i) {
      w[i] = (static_cast<std::uint32_t>(padded[block + i * 4]) << 24) |
             (static_cast<std::uint32_t>(padded[block + i * 4 + 1]) << 16) |
             (static_cast<std::uint32_t>(padded[block + i * 4 + 2]) << 8) |
             static_cast<std::uint32_t>(padded[block + i * 4 + 3]);
    }
    for (std::size_t i = 16; i < 64; ++i) {
      const std::uint32_t s0 = rotate_right(w[i - 15], 7) ^
                               rotate_right(w[i - 15], 18) ^
                               (w[i - 15] >> 3);
      const std::uint32_t s1 = rotate_right(w[i - 2], 17) ^
                               rotate_right(w[i - 2], 19) ^
                               (w[i - 2] >> 10);
      w[i] = w[i - 16] + s0 + w[i - 7] + s1;
    }
    std::uint32_t a = h[0];
    std::uint32_t b = h[1];
    std::uint32_t c = h[2];
    std::uint32_t d = h[3];
    std::uint32_t e = h[4];
    std::uint32_t f = h[5];
    std::uint32_t g = h[6];
    std::uint32_t hh = h[7];
    for (std::size_t i = 0; i < 64; ++i) {
      const std::uint32_t s1 = rotate_right(e, 6) ^ rotate_right(e, 11) ^
                               rotate_right(e, 25);
      const std::uint32_t ch = (e & f) ^ ((~e) & g);
      const std::uint32_t temp1 = hh + s1 + ch + k[i] + w[i];
      const std::uint32_t s0 = rotate_right(a, 2) ^ rotate_right(a, 13) ^
                               rotate_right(a, 22);
      const std::uint32_t maj = (a & b) ^ (a & c) ^ (b & c);
      const std::uint32_t temp2 = s0 + maj;
      hh = g;
      g = f;
      f = e;
      e = d + temp1;
      d = c;
      c = b;
      b = a;
      a = temp1 + temp2;
    }
    h[0] += a;
    h[1] += b;
    h[2] += c;
    h[3] += d;
    h[4] += e;
    h[5] += f;
    h[6] += g;
    h[7] += hh;
  }
  std::array<std::uint8_t, 32> out{};
  for (std::size_t i = 0; i < 8; ++i) {
    out[i * 4] = static_cast<std::uint8_t>(h[i] >> 24);
    out[i * 4 + 1] = static_cast<std::uint8_t>(h[i] >> 16);
    out[i * 4 + 2] = static_cast<std::uint8_t>(h[i] >> 8);
    out[i * 4 + 3] = static_cast<std::uint8_t>(h[i]);
  }
  return out;
}

std::array<std::uint8_t, 64> sha512_core(ByteView message,
                                        const std::array<std::uint64_t, 8>& iv) {
  static constexpr std::uint64_t k[80] = {
      0x428A2F98D728AE22ULL, 0x7137449123EF65CDULL, 0xB5C0FBCFEC4D3B2FULL,
      0xE9B5DBA58189DBBCULL, 0x3956C25BF348B538ULL, 0x59F111F1B605D019ULL,
      0x923F82A4AF194F9BULL, 0xAB1C5ED5DA6D8118ULL, 0xD807AA98A3030242ULL,
      0x12835B0145706FBEULL, 0x243185BE4EE4B28CULL, 0x550C7DC3D5FFB4E2ULL,
      0x72BE5D74F27B896FULL, 0x80DEB1FE3B1696B1ULL, 0x9BDC06A725C71235ULL,
      0xC19BF174CF692694ULL, 0xE49B69C19EF14AD2ULL, 0xEFBE4786384F25E3ULL,
      0x0FC19DC68B8CD5B5ULL, 0x240CA1CC77AC9C65ULL, 0x2DE92C6F592B0275ULL,
      0x4A7484AA6EA6E483ULL, 0x5CB0A9DCBD41FBD4ULL, 0x76F988DA831153B5ULL,
      0x983E5152EE66DFABULL, 0xA831C66D2DB43210ULL, 0xB00327C898FB213FULL,
      0xBF597FC7BEEF0EE4ULL, 0xC6E00BF33DA88FC2ULL, 0xD5A79147930AA725ULL,
      0x06CA6351E003826FULL, 0x142929670A0E6E70ULL, 0x27B70A8546D22FFCULL,
      0x2E1B21385C26C926ULL, 0x4D2C6DFC5AC42AEDULL, 0x53380D139D95B3DFULL,
      0x650A73548BAF63DEULL, 0x766A0ABB3C77B2A8ULL, 0x81C2C92E47EDAEE6ULL,
      0x92722C851482353BULL, 0xA2BFE8A14CF10364ULL, 0xA81A664BBC423001ULL,
      0xC24B8B70D0F89791ULL, 0xC76C51A30654BE30ULL, 0xD192E819D6EF5218ULL,
      0xD69906245565A910ULL, 0xF40E35855771202AULL, 0x106AA07032BBD1B8ULL,
      0x19A4C116B8D2D0C8ULL, 0x1E376C085141AB53ULL, 0x2748774CDF8EEB99ULL,
      0x34B0BCB5E19B48A8ULL, 0x391C0CB3C5C95A63ULL, 0x4ED8AA4AE3418ACBULL,
      0x5B9CCA4F7763E373ULL, 0x682E6FF3D6B2B8A3ULL, 0x748F82EE5DEFB2FCULL,
      0x78A5636F43172F60ULL, 0x84C87814A1F0AB72ULL, 0x8CC702081A6439ECULL,
      0x90BEFFFA23631E28ULL, 0xA4506CEBDE82BDE9ULL, 0xBEF9A3F7B2C67915ULL,
      0xC67178F2E372532BULL, 0xCA273ECEEA26619CULL, 0xD186B8C721C0C207ULL,
      0xEADA7DD6CDE0EB1EULL, 0xF57D4F7FEE6ED178ULL, 0x06F067AA72176FBAULL,
      0x0A637DC5A2C898A6ULL, 0x113F9804BEF90DAEULL, 0x1B710B35131C471BULL,
      0x28DB77F523047D84ULL, 0x32CAAB7B40C72493ULL, 0x3C9EBE0A15C9BEBCULL,
      0x431D67C49C100D4CULL, 0x4CC5D4BECB3E42B6ULL, 0x597F299CFC657E2AULL,
      0x5FCB6FAB3AD6FAECULL, 0x6C44198C4A475817ULL};
  std::array<std::uint64_t, 8> h = iv;

  Bytes padded(message.begin(), message.end());
  padded.push_back(0x80);
  while ((padded.size() % 128) != 112) {
    padded.push_back(0);
  }
  const std::uint64_t bit_count =
      static_cast<std::uint64_t>(message.size()) * 8;
  for (int i = 0; i < 8; ++i) {
    padded.push_back(0);
  }
  for (int i = 7; i >= 0; --i) {
    padded.push_back(static_cast<std::uint8_t>(bit_count >> (i * 8)));
  }

  for (std::size_t block = 0; block < padded.size(); block += 128) {
    std::array<std::uint64_t, 80> w{};
    for (std::size_t i = 0; i < 16; ++i) {
      std::uint64_t word = 0;
      for (std::size_t j = 0; j < 8; ++j) {
        word = (word << 8) | padded[block + i * 8 + j];
      }
      w[i] = word;
    }
    for (std::size_t i = 16; i < 80; ++i) {
      const std::uint64_t s0 = rotate_right64(w[i - 15], 1) ^
                               rotate_right64(w[i - 15], 8) ^
                               (w[i - 15] >> 7);
      const std::uint64_t s1 = rotate_right64(w[i - 2], 19) ^
                               rotate_right64(w[i - 2], 61) ^
                               (w[i - 2] >> 6);
      w[i] = w[i - 16] + s0 + w[i - 7] + s1;
    }
    std::uint64_t a = h[0];
    std::uint64_t b = h[1];
    std::uint64_t c = h[2];
    std::uint64_t d = h[3];
    std::uint64_t e = h[4];
    std::uint64_t f = h[5];
    std::uint64_t g = h[6];
    std::uint64_t hh = h[7];
    for (std::size_t i = 0; i < 80; ++i) {
      const std::uint64_t s1 = rotate_right64(e, 14) ^
                               rotate_right64(e, 18) ^
                               rotate_right64(e, 41);
      const std::uint64_t ch = (e & f) ^ ((~e) & g);
      const std::uint64_t temp1 = hh + s1 + ch + k[i] + w[i];
      const std::uint64_t s0 = rotate_right64(a, 28) ^
                               rotate_right64(a, 34) ^
                               rotate_right64(a, 39);
      const std::uint64_t maj = (a & b) ^ (a & c) ^ (b & c);
      const std::uint64_t temp2 = s0 + maj;
      hh = g;
      g = f;
      f = e;
      e = d + temp1;
      d = c;
      c = b;
      b = a;
      a = temp1 + temp2;
    }
    h[0] += a;
    h[1] += b;
    h[2] += c;
    h[3] += d;
    h[4] += e;
    h[5] += f;
    h[6] += g;
    h[7] += hh;
  }

  std::array<std::uint8_t, 64> out{};
  for (std::size_t i = 0; i < 8; ++i) {
    for (std::size_t j = 0; j < 8; ++j) {
      out[i * 8 + j] =
          static_cast<std::uint8_t>(h[i] >> (56 - static_cast<unsigned>(j) * 8));
    }
  }
  return out;
}

std::array<std::uint8_t, 48> sha384(ByteView message) {
  static constexpr std::array<std::uint64_t, 8> iv = {
      0xCBBB9D5DC1059ED8ULL, 0x629A292A367CD507ULL, 0x9159015A3070DD17ULL,
      0x152FECD8F70E5939ULL, 0x67332667FFC00B31ULL, 0x8EB44A8768581511ULL,
      0xDB0C2E0D64F98FA7ULL, 0x47B5481DBEFA4FA4ULL};
  const auto full = sha512_core(message, iv);
  std::array<std::uint8_t, 48> out{};
  std::copy(full.begin(), full.begin() + 48, out.begin());
  return out;
}

std::array<std::uint8_t, 64> sha512(ByteView message) {
  static constexpr std::array<std::uint64_t, 8> iv = {
      0x6A09E667F3BCC908ULL, 0xBB67AE8584CAA73BULL, 0x3C6EF372FE94F82BULL,
      0xA54FF53A5F1D36F1ULL, 0x510E527FADE682D1ULL, 0x9B05688C2B3E6C1FULL,
      0x1F83D9ABFB41BD6BULL, 0x5BE0CD19137E2179ULL};
  return sha512_core(message, iv);
}

enum class HashKind { sha1, sha256, sha384, sha512 };

bool hash_for_algorithm(std::string_view oid, HashKind& kind) {
  if (oid == "1.2.840.113549.1.1.5") {
    kind = HashKind::sha1;
    return true;
  }
  if (oid == "1.2.840.113549.1.1.11") {
    kind = HashKind::sha256;
    return true;
  }
  if (oid == "1.2.840.113549.1.1.12") {
    kind = HashKind::sha384;
    return true;
  }
  if (oid == "1.2.840.113549.1.1.13") {
    kind = HashKind::sha512;
    return true;
  }
  return false;
}

std::vector<std::uint8_t> digest_info_prefix(HashKind kind) {
  switch (kind) {
    case HashKind::sha1:
      return {0x30, 0x21, 0x30, 0x09, 0x06, 0x05, 0x2B, 0x0E,
              0x03, 0x02, 0x1A, 0x05, 0x00, 0x04, 0x14};
    case HashKind::sha256:
      return {0x30, 0x31, 0x30, 0x0D, 0x06, 0x09, 0x60, 0x86,
              0x48, 0x01, 0x65, 0x03, 0x04, 0x02, 0x01, 0x05,
              0x00, 0x04, 0x20};
    case HashKind::sha384:
      return {0x30, 0x41, 0x30, 0x0D, 0x06, 0x09, 0x60, 0x86,
              0x48, 0x01, 0x65, 0x03, 0x04, 0x02, 0x02, 0x05,
              0x00, 0x04, 0x30};
    case HashKind::sha512:
      return {0x30, 0x51, 0x30, 0x0D, 0x06, 0x09, 0x60, 0x86,
              0x48, 0x01, 0x65, 0x03, 0x04, 0x02, 0x03, 0x05,
              0x00, 0x04, 0x40};
  }
  return {};
}

Bytes digest(HashKind kind, ByteView message) {
  switch (kind) {
    case HashKind::sha1: {
      const auto d = sha1(message);
      return Bytes(d.begin(), d.end());
    }
    case HashKind::sha256: {
      const auto d = sha256(message);
      return Bytes(d.begin(), d.end());
    }
    case HashKind::sha384: {
      const auto d = sha384(message);
      return Bytes(d.begin(), d.end());
    }
    case HashKind::sha512: {
      const auto d = sha512(message);
      return Bytes(d.begin(), d.end());
    }
  }
  return {};
}

bool unsigned_integer(const DerNode& node, ByteView& out) {
  if (!node.is_universal(kInteger) || node.content.empty() ||
      (node.content[0] & 0x80) != 0) {
    return false;
  }
  std::size_t start = 0;
  if (node.content[0] == 0x00) {
    start = 1;
  }
  out = node.content.subspan(start);
  return !out.empty();
}

}  // namespace rsa
}  // namespace

bool RsaPkcs1Verifier::verify(const SignatureRequest& request) {
  try {
    rsa::HashKind hash_kind;
    if (!rsa::hash_for_algorithm(request.algorithm_oid, hash_kind)) {
      return false;
    }

    const DerNode spki = StrictDer::parse(request.public_key_info);
    if (!spki.is_universal(kSequence) || spki.children.size() != 2 ||
        !spki.children[0].is_universal(kSequence) ||
        !spki.children[1].is_universal(kBitString)) {
      return false;
    }
    const DerNode& algorithm = spki.children[0];
    if (algorithm.children.empty() ||
        !algorithm.children[0].is_universal(kObjectIdentifier) ||
        decode_oid(algorithm.children[0].content,
                   static_cast<std::size_t>(
                       algorithm.children[0].encoded.data() -
                       request.public_key_info.data())) !=
            "1.2.840.113549.1.1.1") {
      return false;
    }
    const DerNode& key_bit_string = spki.children[1];
    if (key_bit_string.content.empty() || key_bit_string.content[0] != 0) {
      return false;
    }
    const DerNode rsa_key = StrictDer::parse(key_bit_string.content.subspan(1));
    if (!rsa_key.is_universal(kSequence) || rsa_key.children.size() != 2) {
      return false;
    }
    ByteView modulus_bytes;
    ByteView exponent_bytes;
    if (!rsa::unsigned_integer(rsa_key.children[0], modulus_bytes) ||
        !rsa::unsigned_integer(rsa_key.children[1], exponent_bytes)) {
      return false;
    }

    const std::vector<rsa::Limb> modulus = rsa::from_be(modulus_bytes);
    const std::vector<rsa::Limb> exponent = rsa::from_be(exponent_bytes);
    if (modulus.empty() || exponent.empty()) {
      return false;
    }
    const std::size_t modulus_size = modulus_bytes.size();
    if (request.signature.size() > modulus_size) {
      return false;
    }
    Bytes signature(modulus_size, 0);
    std::copy(request.signature.begin(), request.signature.end(),
              signature.end() - static_cast<std::ptrdiff_t>(
                                     request.signature.size()));
    const std::vector<rsa::Limb> signature_value = rsa::from_be(signature);
    if (rsa::compare(signature_value, modulus) >= 0) {
      return false;
    }

    const std::vector<rsa::Limb> recovered =
        rsa::pow_mod(signature_value, exponent, modulus);
    const std::vector<std::uint8_t> em =
        rsa::to_be(recovered, modulus_size);
    if (em.size() != modulus_size) {
      return false;
    }

    const std::vector<std::uint8_t> prefix =
        rsa::digest_info_prefix(hash_kind);
    const Bytes message_digest = rsa::digest(hash_kind, request.tbs);
    const std::size_t encoded_size = prefix.size() + message_digest.size();
    if (modulus_size < encoded_size + 11) {
      return false;
    }
    std::vector<std::uint8_t> expected(modulus_size, 0);
    expected[0] = 0x00;
    expected[1] = 0x01;
    const std::size_t padding = modulus_size - encoded_size - 3;
    std::fill(expected.begin() + 2,
              expected.begin() + 2 + static_cast<std::ptrdiff_t>(padding),
              0xFF);
    expected[2 + padding] = 0x00;
    std::copy(prefix.begin(), prefix.end(),
              expected.begin() + 3 + static_cast<std::ptrdiff_t>(padding));
    std::copy(message_digest.begin(), message_digest.end(),
              expected.begin() + 3 + static_cast<std::ptrdiff_t>(padding) +
                  static_cast<std::ptrdiff_t>(prefix.size()));
    return em == expected;
  } catch (const DerError&) {
    return false;
  } catch (const std::exception&) {
    return false;
  }
}

bool Certificate::verify_signature() const {
  RsaPkcs1Verifier verifier;
  return verify_signature(verifier);
}

bool Certificate::verify_signature_with_key(ByteView verification_key_spki) const {
  RsaPkcs1Verifier verifier;
  return verify_signature_with_key(verifier, verification_key_spki);
}

}  // namespace netsec
