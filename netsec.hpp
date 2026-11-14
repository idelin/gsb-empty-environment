#pragma once

#include <cstdint>
#include <map>
#include <optional>
#include <string>
#include <string_view>
#include <variant>
#include <vector>

namespace netsec {

// 会话角色，数值越大权限越高
enum class Role : std::uint8_t {
  kOperator = 0,  // 操作员：仅公开运行数据
  kEngineer = 1,  // 工程师：可读配置
  kAdmin = 2,     // 管理员：可读凭据等敏感属性
};

struct Session {
  Role role = Role::kOperator;
};

enum class DataType : std::uint8_t {
  kBool,
  kUint8,
  kUint16,
  kUint32,
  kString,
  kBytes,
};

using Value = std::variant<bool, std::uint8_t, std::uint16_t, std::uint32_t,
                           std::string, std::vector<std::uint8_t>>;

// 值与声明类型是否一致
bool matches_type(const Value& value, DataType type) noexcept;

struct AttributeDef {
  std::uint16_t id = 0;
  std::string name;
  DataType type = DataType::kUint16;
  Role min_read_role = Role::kOperator;  // 读取该属性所需的最低角色
};

struct ClassDef {
  std::uint16_t class_id = 0;
  std::string name;
  std::uint16_t max_instance = 0;  // 合法实例编号为 [1, max_instance]
  std::vector<AttributeDef> attributes;

  const AttributeDef* find_attribute(std::uint16_t attr_id) const noexcept;
};

class ObjectDictionary {
 public:
  void add_class(ClassDef def);
  const ClassDef* find_class(std::uint16_t class_id) const noexcept;

 private:
  std::map<std::uint16_t, ClassDef> classes_;
};

// 三段式对象路径：类 / 实例 / 属性
struct ObjectPath {
  std::uint16_t class_id = 0;
  std::uint16_t instance_id = 0;
  std::uint16_t attribute_id = 0;
};

enum class PathError : std::uint8_t {
  kMalformed,          // 段数不为 3、段为空或含非数字字符
  kCrossClass,         // 路径试图跨越到其它类（多余段）
  kUnknownClass,       // 字典中不存在该类
  kInstanceOutOfRange, // 实例编号越界
  kUnknownAttribute,   // 该类未定义此属性（含借用他类属性号）
  kAccessDenied,       // 会话角色不足
  kTypeMismatch,       // 存储值与声明类型不符
  kNoSuchInstance,     // 实例在范围内但未载入数据
};

const char* to_string(PathError err) noexcept;

// 简化版 expected：要么持有 T，要么持有 PathError
template <typename T>
class Result {
 public:
  static Result ok(T value) { return Result(std::move(value)); }
  static Result err(PathError error) { return Result(error); }

  bool has_value() const noexcept { return std::holds_alternative<T>(data_); }
  explicit operator bool() const noexcept { return has_value(); }
  const T& value() const& { return std::get<T>(data_); }
  T& value() & { return std::get<T>(data_); }
  PathError error() const { return std::get<PathError>(data_); }

 private:
  explicit Result(T value) : data_(std::move(value)) {}
  explicit Result(PathError error) : data_(error) {}
  std::variant<T, PathError> data_;
};

// 实例数据存储：按 (类, 实例, 属性) 存放当前值
class InstanceStore {
 public:
  void set(std::uint16_t class_id, std::uint16_t instance_id,
           std::uint16_t attribute_id, Value value);
  const Value* get(std::uint16_t class_id, std::uint16_t instance_id,
                   std::uint16_t attribute_id) const noexcept;

 private:
  struct Key {
    std::uint16_t class_id;
    std::uint16_t instance_id;
    std::uint16_t attribute_id;
    bool operator<(const Key& other) const noexcept;
  };
  std::map<Key, Value> values_;
};

struct ResolvedAttribute {
  ObjectPath path;
  const AttributeDef* def = nullptr;
};

// 路径解析与访问控制：所有读取都必须经过该类
class PathResolver {
 public:
  PathResolver(const ObjectDictionary& dictionary, const InstanceStore& store);

  // 仅做词法解析："class/instance/attribute"，拒绝多余段（跨类路径）
  static Result<ObjectPath> parse(std::string_view text);

  // 词法解析 + 字典核对 + 角色校验
  Result<ResolvedAttribute> resolve(std::string_view text,
                                    const Session& session) const;
  Result<ResolvedAttribute> resolve(const ObjectPath& path,
                                    const Session& session) const;

  // 解析并读取，返回前校验存储值类型与声明类型一致
  Result<Value> read(std::string_view text, const Session& session) const;
  Result<Value> read(const ObjectPath& path, const Session& session) const;

 private:
  const ObjectDictionary& dictionary_;
  const InstanceStore& store_;
};

}  // namespace netsec
