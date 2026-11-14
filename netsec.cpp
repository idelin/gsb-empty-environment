#include "netsec.hpp"

#include <array>
#include <charconv>

namespace netsec {

bool matches_type(const Value& value, DataType type) noexcept {
  switch (type) {
    case DataType::kBool:
      return std::holds_alternative<bool>(value);
    case DataType::kUint8:
      return std::holds_alternative<std::uint8_t>(value);
    case DataType::kUint16:
      return std::holds_alternative<std::uint16_t>(value);
    case DataType::kUint32:
      return std::holds_alternative<std::uint32_t>(value);
    case DataType::kString:
      return std::holds_alternative<std::string>(value);
    case DataType::kBytes:
      return std::holds_alternative<std::vector<std::uint8_t>>(value);
  }
  return false;
}

const AttributeDef* ClassDef::find_attribute(std::uint16_t attr_id) const
    noexcept {
  for (const auto& attr : attributes) {
    if (attr.id == attr_id) {
      return &attr;
    }
  }
  return nullptr;
}

void ObjectDictionary::add_class(ClassDef def) {
  classes_[def.class_id] = std::move(def);
}

const ClassDef* ObjectDictionary::find_class(std::uint16_t class_id) const
    noexcept {
  const auto it = classes_.find(class_id);
  return it == classes_.end() ? nullptr : &it->second;
}

const char* to_string(PathError err) noexcept {
  switch (err) {
    case PathError::kMalformed:
      return "malformed path";
    case PathError::kCrossClass:
      return "cross-class path rejected";
    case PathError::kUnknownClass:
      return "unknown class";
    case PathError::kInstanceOutOfRange:
      return "instance out of range";
    case PathError::kUnknownAttribute:
      return "unknown attribute";
    case PathError::kAccessDenied:
      return "access denied for session role";
    case PathError::kTypeMismatch:
      return "stored value type mismatch";
    case PathError::kNoSuchInstance:
      return "instance has no data";
  }
  return "unknown error";
}

bool InstanceStore::Key::operator<(const Key& other) const noexcept {
  if (class_id != other.class_id) return class_id < other.class_id;
  if (instance_id != other.instance_id)
    return instance_id < other.instance_id;
  return attribute_id < other.attribute_id;
}

void InstanceStore::set(std::uint16_t class_id, std::uint16_t instance_id,
                        std::uint16_t attribute_id, Value value) {
  values_[Key{class_id, instance_id, attribute_id}] = std::move(value);
}

const Value* InstanceStore::get(std::uint16_t class_id,
                                std::uint16_t instance_id,
                                std::uint16_t attribute_id) const noexcept {
  const auto it = values_.find(Key{class_id, instance_id, attribute_id});
  return it == values_.end() ? nullptr : &it->second;
}

PathResolver::PathResolver(const ObjectDictionary& dictionary,
                           const InstanceStore& store)
    : dictionary_(dictionary), store_(store) {}

namespace {

Result<std::uint16_t> parse_segment(std::string_view segment) {
  if (segment.empty()) {
    return Result<std::uint16_t>::err(PathError::kMalformed);
  }
  std::uint32_t value = 0;
  const char* first = segment.data();
  const char* last = first + segment.size();
  const auto [ptr, ec] = std::from_chars(first, last, value, 10);
  if (ec != std::errc{} || ptr != last || value > 0xFFFFu) {
    return Result<std::uint16_t>::err(PathError::kMalformed);
  }
  return Result<std::uint16_t>::ok(static_cast<std::uint16_t>(value));
}

}  // namespace

Result<ObjectPath> PathResolver::parse(std::string_view text) {
  std::array<std::string_view, 3> segments{};
  std::size_t count = 0;
  std::size_t pos = 0;
  while (pos <= text.size()) {
    const std::size_t slash = text.find('/', pos);
    if (count >= segments.size()) {
      // 超过三段：试图沿路径跨入其它类
      return Result<ObjectPath>::err(PathError::kCrossClass);
    }
    segments[count++] = text.substr(
        pos, slash == std::string_view::npos ? slash : slash - pos);
    if (slash == std::string_view::npos) {
      break;
    }
    pos = slash + 1;
  }
  if (count != segments.size()) {
    return Result<ObjectPath>::err(PathError::kMalformed);
  }
  ObjectPath path{};
  std::array<std::uint16_t*, 3> out{&path.class_id, &path.instance_id,
                                    &path.attribute_id};
  for (std::size_t i = 0; i < segments.size(); ++i) {
    auto parsed = parse_segment(segments[i]);
    if (!parsed) {
      return Result<ObjectPath>::err(parsed.error());
    }
    *out[i] = parsed.value();
  }
  return Result<ObjectPath>::ok(path);
}

Result<ResolvedAttribute> PathResolver::resolve(std::string_view text,
                                                const Session& session) const {
  auto parsed = parse(text);
  if (!parsed) {
    return Result<ResolvedAttribute>::err(parsed.error());
  }
  return resolve(parsed.value(), session);
}

Result<ResolvedAttribute> PathResolver::resolve(const ObjectPath& path,
                                                const Session& session) const {
  const ClassDef* cls = dictionary_.find_class(path.class_id);
  if (cls == nullptr) {
    return Result<ResolvedAttribute>::err(PathError::kUnknownClass);
  }
  if (path.instance_id == 0 || path.instance_id > cls->max_instance) {
    return Result<ResolvedAttribute>::err(PathError::kInstanceOutOfRange);
  }
  const AttributeDef* attr = cls->find_attribute(path.attribute_id);
  if (attr == nullptr) {
    // 属性号必须在本类内定义；借用其它类的属性号同样在此被拒绝
    return Result<ResolvedAttribute>::err(PathError::kUnknownAttribute);
  }
  if (static_cast<std::uint8_t>(session.role) <
      static_cast<std::uint8_t>(attr->min_read_role)) {
    return Result<ResolvedAttribute>::err(PathError::kAccessDenied);
  }
  return Result<ResolvedAttribute>::ok(ResolvedAttribute{path, attr});
}

Result<Value> PathResolver::read(std::string_view text,
                                 const Session& session) const {
  auto parsed = parse(text);
  if (!parsed) {
    return Result<Value>::err(parsed.error());
  }
  return read(parsed.value(), session);
}

Result<Value> PathResolver::read(const ObjectPath& path,
                                 const Session& session) const {
  auto resolved = resolve(path, session);
  if (!resolved) {
    return Result<Value>::err(resolved.error());
  }
  const Value* value =
      store_.get(path.class_id, path.instance_id, path.attribute_id);
  if (value == nullptr) {
    return Result<Value>::err(PathError::kNoSuchInstance);
  }
  if (!matches_type(*value, resolved.value().def->type)) {
    return Result<Value>::err(PathError::kTypeMismatch);
  }
  return Result<Value>::ok(*value);
}

}  // namespace netsec
