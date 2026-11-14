#include "netsec.hpp"

#include <cstdio>
#include <cstdlib>
#include <string>
#include <string_view>
#include <vector>

namespace {

int g_failures = 0;

void check(bool condition, std::string_view name) {
  if (condition) {
    std::printf("[PASS] %.*s\n", static_cast<int>(name.size()), name.data());
  } else {
    std::printf("[FAIL] %.*s\n", static_cast<int>(name.size()), name.data());
    ++g_failures;
  }
}

using netsec::ClassDef;
using netsec::DataType;
using netsec::InstanceStore;
using netsec::ObjectDictionary;
using netsec::PathError;
using netsec::PathResolver;
using netsec::Role;
using netsec::Session;
using netsec::Value;

// 类 0x01 Identity：公开属性 + 受限配置；类 0x02 Credential：仅管理员可读
struct Fixture {
  ObjectDictionary dictionary;
  InstanceStore store;
  PathResolver resolver{dictionary, store};

  Fixture() {
    dictionary.add_class(ClassDef{
        0x01, "Identity", 2,
        {{1, "vendor_id", DataType::kUint16, Role::kOperator},
         {2, "device_name", DataType::kString, Role::kOperator},
         {3, "ip_config", DataType::kString, Role::kEngineer}}});
    dictionary.add_class(ClassDef{
        0x02, "Credential", 1,
        {{1, "username", DataType::kString, Role::kAdmin},
         {2, "password_hash", DataType::kBytes, Role::kAdmin}}});

    store.set(0x01, 1, 1, Value{std::uint16_t{0x01AF}});
    store.set(0x01, 1, 2, Value{std::string{"controller-a"}});
    store.set(0x01, 1, 3, Value{std::string{"192.168.1.10/24"}});
    store.set(0x02, 1, 1, Value{std::string{"svc_account"}});
    store.set(0x02, 1, 2,
              Value{std::vector<std::uint8_t>{0xDE, 0xAD, 0xBE, 0xEF}});
  }
};

void test_out_of_range_instance() {
  Fixture f;
  Session admin{Role::kAdmin};

  auto beyond = f.resolver.read("1/3/1", admin);  // max_instance = 2
  check(!beyond && beyond.error() == PathError::kInstanceOutOfRange,
        "越界实例: instance 3 超出范围被拒绝");

  auto zero = f.resolver.read("1/0/1", admin);
  check(!zero && zero.error() == PathError::kInstanceOutOfRange,
        "越界实例: instance 0 被拒绝");

  auto in_range_no_data = f.resolver.read("1/2/1", admin);
  check(!in_range_no_data &&
            in_range_no_data.error() == PathError::kNoSuchInstance,
        "范围内但未载入的实例不泄露数据");
}

void test_cross_class_path() {
  Fixture f;
  Session admin{Role::kAdmin};

  // 四段路径：试图从 Identity 跨入 Credential
  auto hop = f.resolver.read("1/1/2/2", admin);
  check(!hop && hop.error() == PathError::kCrossClass,
        "跨类路径: 多余段被拒绝");

  // 本类属性 2 是 device_name，不会串到 Credential 的属性 2
  auto own_attr = f.resolver.read("1/1/2", admin);
  check(own_attr &&
            std::get<std::string>(own_attr.value()) == "controller-a",
        "本类属性正常解析，不串到他类同编号属性");

  auto unknown_class = f.resolver.read("127/1/1", admin);
  check(!unknown_class && unknown_class.error() == PathError::kUnknownClass,
        "未定义类被拒绝");

  auto non_numeric = f.resolver.read("0x7F/1/1", admin);
  check(!non_numeric && non_numeric.error() == PathError::kMalformed,
        "非数字段被拒绝");

  // Credential 类上没有属性 3，不能借此探测
  auto no_attr = f.resolver.read("2/1/3", admin);
  check(!no_attr && no_attr.error() == PathError::kUnknownAttribute,
        "类内未定义属性被拒绝");
}

void test_attribute_escalation() {
  Fixture f;
  Session op{Role::kOperator};
  Session eng{Role::kEngineer};

  auto cred_by_op = f.resolver.read("2/1/1", op);
  check(!cred_by_op && cred_by_op.error() == PathError::kAccessDenied,
        "属性提升: 操作员读凭据被拒绝");

  auto cred_by_eng = f.resolver.read("2/1/2", eng);
  check(!cred_by_eng && cred_by_eng.error() == PathError::kAccessDenied,
        "属性提升: 工程师读凭据被拒绝");

  auto cfg_by_op = f.resolver.read("1/1/3", op);
  check(!cfg_by_op && cfg_by_op.error() == PathError::kAccessDenied,
        "属性提升: 操作员读网络配置被拒绝");
}

void test_legitimate_reads() {
  Fixture f;
  Session op{Role::kOperator};
  Session eng{Role::kEngineer};
  Session admin{Role::kAdmin};

  auto name = f.resolver.read("1/1/2", op);
  check(name && std::get<std::string>(name.value()) == "controller-a",
        "合法读取: 操作员读公开属性");

  auto cfg = f.resolver.read("1/1/3", eng);
  check(cfg && std::get<std::string>(cfg.value()) == "192.168.1.10/24",
        "合法读取: 工程师读配置");

  auto cred = f.resolver.read("2/1/1", admin);
  check(cred && std::get<std::string>(cred.value()) == "svc_account",
        "合法读取: 管理员读凭据");

  auto hash = f.resolver.read("2/1/2", admin);
  check(hash && std::get<std::vector<std::uint8_t>>(hash.value()).size() == 4,
        "合法读取: 字节串类型校验通过");
}

void test_role_change() {
  Fixture f;
  Session session{Role::kAdmin};

  auto before = f.resolver.read("2/1/1", session);
  check(before.has_value(), "角色变更前: 管理员可读凭据");

  session.role = Role::kOperator;  // 会话降级
  auto after = f.resolver.read("2/1/1", session);
  check(!after && after.error() == PathError::kAccessDenied,
        "角色变更后: 同一会话降级后立即失去凭据访问权");

  auto still_public = f.resolver.read("1/1/2", session);
  check(still_public.has_value(), "角色变更后: 公开属性仍可读");
}

void test_type_mismatch() {
  Fixture f;
  // 存储值被篡改为与声明类型不符
  f.store.set(0x01, 1, 1, Value{std::string{"not-a-uint16"}});
  Session admin{Role::kAdmin};
  auto bad = f.resolver.read("1/1/1", admin);
  check(!bad && bad.error() == PathError::kTypeMismatch,
        "数据类型校验: 类型不符的值不返回");
}

}  // namespace

int main() {
  test_out_of_range_instance();
  test_cross_class_path();
  test_attribute_escalation();
  test_legitimate_reads();
  test_role_change();
  test_type_mismatch();
  if (g_failures != 0) {
    std::printf("%d check(s) failed\n", g_failures);
    return EXIT_FAILURE;
  }
  std::printf("all checks passed\n");
  return EXIT_SUCCESS;
}
