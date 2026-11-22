#pragma once

#include <cstdint>
#include <map>
#include <string>
#include <string_view>
#include <vector>

namespace gsb {

// A deliberately small JSON value used by the import format.  It supports the
// subset needed by the package (objects, arrays, strings, integers, booleans
// and null) while preserving 64-bit integer precision.
class Json {
public:
  enum class Type { Null, Bool, Int, UInt, Double, String, Array, Object };
  using Array = std::vector<Json>;
  using Object = std::map<std::string, Json, std::less<>>;

  Json() = default;
  Json(std::nullptr_t) : type_(Type::Null) {}
  Json(bool v) : type_(Type::Bool), bool_(v) {}
  Json(int v) : type_(Type::Int), int_(v) {}
  Json(int64_t v) : type_(Type::Int), int_(v) {}
  Json(uint64_t v) : type_(Type::UInt), uint_(v) {}
  Json(double v) : type_(Type::Double), double_(v) {}
  Json(std::string v) : type_(Type::String), string_(std::move(v)) {}
  Json(const char* v) : type_(Type::String), string_(v) {}
  Json(Array v) : type_(Type::Array), array_(std::move(v)) {}
  Json(Object v) : type_(Type::Object), object_(std::move(v)) {}

  Type type() const { return type_; }
  bool is_null() const { return type_ == Type::Null; }
  bool is_bool() const { return type_ == Type::Bool; }
  bool is_number() const {
    return type_ == Type::Int || type_ == Type::UInt || type_ == Type::Double;
  }
  bool is_string() const { return type_ == Type::String; }
  bool is_array() const { return type_ == Type::Array; }
  bool is_object() const { return type_ == Type::Object; }

  bool as_bool() const { return bool_; }
  int64_t as_i64() const;
  uint64_t as_u64() const;
  double as_double() const;
  const std::string& as_string() const { return string_; }
  const Array& as_array() const { return array_; }
  const Object& as_object() const { return object_; }

  Array& as_array() { return array_; }
  Object& as_object() { return object_; }

  const Json* find(std::string_view key) const;
  const Json& at(std::string_view key) const;
  Json& operator[](std::string key);

  static Json parse(std::string_view text, std::string* error = nullptr);
  std::string dump(int indent = 2) const;

private:
  Type type_ = Type::Null;
  bool bool_ = false;
  int64_t int_ = 0;
  uint64_t uint_ = 0;
  double double_ = 0.0;
  std::string string_;
  Array array_;
  Object object_;
};

}  // namespace gsb
