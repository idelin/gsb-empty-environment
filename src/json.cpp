#include "gsb/json.hpp"

#include <charconv>
#include <cmath>
#include <cstdio>
#include <cstdlib>
#include <limits>
#include <stdexcept>

namespace gsb {

namespace {

class Parser {
public:
  explicit Parser(std::string_view text) : text_(text) {}

  Json parse() {
    skip_ws();
    Json value = parse_value(0);
    skip_ws();
    if (pos_ != text_.size()) {
      fail("trailing characters");
    }
    return value;
  }

private:
  [[noreturn]] void fail(const std::string& message) const {
    throw std::runtime_error(message + " at byte " + std::to_string(pos_));
  }

  void skip_ws() {
    while (pos_ < text_.size()) {
      char c = text_[pos_];
      if (c == ' ' || c == '\t' || c == '\n' || c == '\r') {
        ++pos_;
      } else {
        break;
      }
    }
  }

  char peek() const {
    if (pos_ >= text_.size()) {
      fail("unexpected end of input");
    }
    return text_[pos_];
  }

  char take() {
    char c = peek();
    ++pos_;
    return c;
  }

  void expect(char c) {
    if (take() != c) {
      fail(std::string("expected '") + c + "'");
    }
  }

  bool consume(std::string_view word) {
    if (text_.substr(pos_, word.size()) == word) {
      pos_ += word.size();
      return true;
    }
    return false;
  }

  Json parse_value(int depth) {
    if (depth > 256) {
      fail("nesting too deep");
    }
    skip_ws();
    char c = peek();
    switch (c) {
      case 'n':
        if (!consume("null")) fail("invalid token");
        return Json();
      case 't':
        if (!consume("true")) fail("invalid token");
        return Json(true);
      case 'f':
        if (!consume("false")) fail("invalid token");
        return Json(false);
      case '"':
        return Json(parse_string());
      case '[':
        return parse_array(depth + 1);
      case '{':
        return parse_object(depth + 1);
      default:
        if (c == '-' || (c >= '0' && c <= '9')) {
          return parse_number();
        }
        fail("unexpected character");
    }
  }

  Json parse_array(int depth) {
    expect('[');
    Json::Array out;
    skip_ws();
    if (peek() == ']') {
      ++pos_;
      return Json(std::move(out));
    }
    while (true) {
      out.push_back(parse_value(depth));
      skip_ws();
      char c = take();
      if (c == ']') break;
      if (c != ',') fail("expected ',' or ']'");
    }
    return Json(std::move(out));
  }

  Json parse_object(int depth) {
    expect('{');
    Json::Object out;
    skip_ws();
    if (peek() == '}') {
      ++pos_;
      return Json(std::move(out));
    }
    while (true) {
      skip_ws();
      if (peek() != '"') fail("expected object key");
      std::string key = parse_string();
      skip_ws();
      expect(':');
      Json value = parse_value(depth);
      if (!out.emplace(std::move(key), std::move(value)).second) {
        fail("duplicate object key");
      }
      skip_ws();
      char c = take();
      if (c == '}') break;
      if (c != ',') fail("expected ',' or '}'");
    }
    return Json(std::move(out));
  }

  static void append_utf8(std::string& out, uint32_t cp) {
    if (cp <= 0x7f) {
      out.push_back(static_cast<char>(cp));
    } else if (cp <= 0x7ff) {
      out.push_back(static_cast<char>(0xc0 | (cp >> 6)));
      out.push_back(static_cast<char>(0x80 | (cp & 0x3f)));
    } else if (cp <= 0xffff) {
      out.push_back(static_cast<char>(0xe0 | (cp >> 12)));
      out.push_back(static_cast<char>(0x80 | ((cp >> 6) & 0x3f)));
      out.push_back(static_cast<char>(0x80 | (cp & 0x3f)));
    } else {
      out.push_back(static_cast<char>(0xf0 | (cp >> 18)));
      out.push_back(static_cast<char>(0x80 | ((cp >> 12) & 0x3f)));
      out.push_back(static_cast<char>(0x80 | ((cp >> 6) & 0x3f)));
      out.push_back(static_cast<char>(0x80 | (cp & 0x3f)));
    }
  }

  uint32_t parse_hex4() {
    uint32_t value = 0;
    for (int i = 0; i < 4; ++i) {
      char c = take();
      value <<= 4;
      if (c >= '0' && c <= '9') {
        value |= static_cast<uint32_t>(c - '0');
      } else if (c >= 'a' && c <= 'f') {
        value |= static_cast<uint32_t>(c - 'a' + 10);
      } else if (c >= 'A' && c <= 'F') {
        value |= static_cast<uint32_t>(c - 'A' + 10);
      } else {
        fail("invalid \\u escape");
      }
    }
    return value;
  }

  std::string parse_string() {
    expect('"');
    std::string out;
    while (true) {
      char c = take();
      if (c == '"') break;
      if (static_cast<unsigned char>(c) < 0x20) {
        fail("control character in string");
      }
      if (c != '\\') {
        out.push_back(c);
        continue;
      }
      char esc = take();
      switch (esc) {
        case '"': out.push_back('"'); break;
        case '\\': out.push_back('\\'); break;
        case '/': out.push_back('/'); break;
        case 'b': out.push_back('\b'); break;
        case 'f': out.push_back('\f'); break;
        case 'n': out.push_back('\n'); break;
        case 'r': out.push_back('\r'); break;
        case 't': out.push_back('\t'); break;
        case 'u': {
          uint32_t cp = parse_hex4();
          if (cp >= 0xd800 && cp <= 0xdbff) {
            if (!consume("\\u")) fail("missing low surrogate");
            uint32_t low = parse_hex4();
            if (low < 0xdc00 || low > 0xdfff) fail("invalid low surrogate");
            cp = 0x10000 + ((cp - 0xd800) << 10) + (low - 0xdc00);
          } else if (cp >= 0xdc00 && cp <= 0xdfff) {
            fail("unexpected low surrogate");
          }
          append_utf8(out, cp);
          break;
        }
        default:
          fail("invalid escape");
      }
    }
    return out;
  }

  Json parse_number() {
    size_t start = pos_;
    if (peek() == '-') ++pos_;
    if (pos_ >= text_.size()) fail("invalid number");
    if (text_[pos_] == '0') {
      ++pos_;
    } else if (text_[pos_] >= '1' && text_[pos_] <= '9') {
      while (pos_ < text_.size() && text_[pos_] >= '0' && text_[pos_] <= '9') ++pos_;
    } else {
      fail("invalid number");
    }
    bool floating = false;
    if (pos_ < text_.size() && text_[pos_] == '.') {
      floating = true;
      ++pos_;
      if (pos_ >= text_.size() || text_[pos_] < '0' || text_[pos_] > '9') {
        fail("invalid fraction");
      }
      while (pos_ < text_.size() && text_[pos_] >= '0' && text_[pos_] <= '9') ++pos_;
    }
    if (pos_ < text_.size() && (text_[pos_] == 'e' || text_[pos_] == 'E')) {
      floating = true;
      ++pos_;
      if (pos_ < text_.size() && (text_[pos_] == '+' || text_[pos_] == '-')) ++pos_;
      if (pos_ >= text_.size() || text_[pos_] < '0' || text_[pos_] > '9') {
        fail("invalid exponent");
      }
      while (pos_ < text_.size() && text_[pos_] >= '0' && text_[pos_] <= '9') ++pos_;
    }
    std::string token(text_.substr(start, pos_ - start));
    if (floating) {
      char* end = nullptr;
      double value = std::strtod(token.c_str(), &end);
      if (end == nullptr || *end != '\0') fail("invalid number");
      return Json(value);
    }
    if (!token.empty() && token[0] == '-') {
      int64_t value = 0;
      auto result = std::from_chars(token.data(), token.data() + token.size(), value);
      if (result.ec != std::errc()) fail("integer out of range");
      return Json(value);
    }
    uint64_t value = 0;
    auto result = std::from_chars(token.data(), token.data() + token.size(), value);
    if (result.ec != std::errc()) fail("integer out of range");
    if (value <= static_cast<uint64_t>(std::numeric_limits<int64_t>::max())) {
      return Json(static_cast<int64_t>(value));
    }
    return Json(value);
  }

  std::string_view text_;
  size_t pos_ = 0;
};

void dump_string(std::string& out, std::string_view value) {
  out.push_back('"');
  for (unsigned char c : value) {
    switch (c) {
      case '"': out += "\\\""; break;
      case '\\': out += "\\\\"; break;
      case '\b': out += "\\b"; break;
      case '\f': out += "\\f"; break;
      case '\n': out += "\\n"; break;
      case '\r': out += "\\r"; break;
      case '\t': out += "\\t"; break;
      default:
        if (c < 0x20) {
          char buffer[7];
          std::snprintf(buffer, sizeof(buffer), "\\u%04x", c);
          out += buffer;
        } else {
          out.push_back(static_cast<char>(c));
        }
    }
  }
  out.push_back('"');
}

void dump_value(std::string& out, const Json& value, int indent, int level) {
  auto newline = [&](int lev) {
    out.push_back('\n');
    out.append(static_cast<size_t>(indent * lev), ' ');
  };
  switch (value.type()) {
    case Json::Type::Null:
      out += "null";
      break;
    case Json::Type::Bool:
      out += value.as_bool() ? "true" : "false";
      break;
    case Json::Type::Int:
      out += std::to_string(value.as_i64());
      break;
    case Json::Type::UInt:
      out += std::to_string(value.as_u64());
      break;
    case Json::Type::Double: {
      char buffer[64];
      std::snprintf(buffer, sizeof(buffer), "%.17g", value.as_double());
      out += buffer;
      break;
    }
    case Json::Type::String:
      dump_string(out, value.as_string());
      break;
    case Json::Type::Array: {
      const auto& array = value.as_array();
      if (array.empty()) {
        out += "[]";
        break;
      }
      out.push_back('[');
      for (size_t i = 0; i < array.size(); ++i) {
        if (i) out.push_back(',');
        if (indent > 0) newline(level + 1);
        dump_value(out, array[i], indent, level + 1);
      }
      if (indent > 0) newline(level);
      out.push_back(']');
      break;
    }
    case Json::Type::Object: {
      const auto& object = value.as_object();
      if (object.empty()) {
        out += "{}";
        break;
      }
      out.push_back('{');
      bool first = true;
      for (const auto& [key, child] : object) {
        if (!first) out.push_back(',');
        first = false;
        if (indent > 0) newline(level + 1);
        dump_string(out, key);
        out += indent > 0 ? ": " : ":";
        dump_value(out, child, indent, level + 1);
      }
      if (indent > 0) newline(level);
      out.push_back('}');
      break;
    }
  }
}

}  // namespace

int64_t Json::as_i64() const {
  switch (type_) {
    case Type::Int: return int_;
    case Type::UInt:
      if (uint_ > static_cast<uint64_t>(std::numeric_limits<int64_t>::max())) {
        throw std::runtime_error("integer does not fit int64");
      }
      return static_cast<int64_t>(uint_);
    case Type::Double:
      if (!std::isfinite(double_)) throw std::runtime_error("non-finite number");
      return static_cast<int64_t>(double_);
    default:
      throw std::runtime_error("JSON value is not an integer");
  }
}

uint64_t Json::as_u64() const {
  switch (type_) {
    case Type::UInt: return uint_;
    case Type::Int:
      if (int_ < 0) throw std::runtime_error("negative integer where unsigned expected");
      return static_cast<uint64_t>(int_);
    case Type::Double:
      if (!std::isfinite(double_) || double_ < 0) {
        throw std::runtime_error("invalid unsigned number");
      }
      return static_cast<uint64_t>(double_);
    default:
      throw std::runtime_error("JSON value is not an integer");
  }
}

double Json::as_double() const {
  switch (type_) {
    case Type::Double: return double_;
    case Type::Int: return static_cast<double>(int_);
    case Type::UInt: return static_cast<double>(uint_);
    default:
      throw std::runtime_error("JSON value is not a number");
  }
}

const Json* Json::find(std::string_view key) const {
  if (type_ != Type::Object) return nullptr;
  auto it = object_.find(key);
  return it == object_.end() ? nullptr : &it->second;
}

const Json& Json::at(std::string_view key) const {
  const Json* value = find(key);
  if (!value) throw std::runtime_error("missing JSON key: " + std::string(key));
  return *value;
}

Json& Json::operator[](std::string key) {
  if (type_ != Type::Object) {
    type_ = Type::Object;
    object_.clear();
  }
  return object_[std::move(key)];
}

Json Json::parse(std::string_view text, std::string* error) {
  try {
    return Parser(text).parse();
  } catch (const std::exception& ex) {
    if (error) *error = ex.what();
    return Json();
  }
}

std::string Json::dump(int indent) const {
  std::string out;
  dump_value(out, *this, indent, 0);
  if (indent > 0) out.push_back('\n');
  return out;
}

}  // namespace gsb
