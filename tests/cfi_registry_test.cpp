#include "netsec.hpp"

#include <cstddef>
#include <cstdint>
#include <cstdlib>
#include <iostream>
#include <string_view>
#include <vector>

namespace {

using netsec::CheckStatus;
using netsec::TargetRecord;

int failures = 0;

void expect(const std::string& name, CheckStatus actual, CheckStatus expected) {
    const bool passed = actual == expected;
    if (!passed) {
        ++failures;
    }
    std::cout << (passed ? "[ PASS ] " : "[ FAIL ] ") << name
              << " (status=" << netsec::reason(actual) << ")\n";
}

int add_one(int value) {
    return value + 1;
}

int add_two(int value) {
    return value + 2;
}

double double_value(double value) {
    return value * 2.0;
}

void never_referenced() {}

using IntUnary = int(int);
using DoubleUnary = double(double);
using VoidFn = void();

template <typename Fn>
const void* entry_of(Fn* fn) {
    return reinterpret_cast<const void*>(fn);
}

const void* offset_entry(const void* entry, std::size_t offset) {
    return reinterpret_cast<const void*>(
        reinterpret_cast<std::uintptr_t>(entry) + offset);
}

}  // namespace

int main() {
    netsec::Registry registry;

    constexpr std::size_t function_body_size = 64;

    // 模拟模块加载：入口、类型标识、函数体范围和取地址标记。
    std::vector<TargetRecord> module_records;
    module_records.push_back(
        netsec::make_target_record<IntUnary>(&add_one, function_body_size, true));
    module_records.push_back(
        netsec::make_target_record<IntUnary>(&add_two, function_body_size, true));
    module_records.push_back(
        netsec::make_target_record<DoubleUnary>(&double_value, function_body_size, true));
    module_records.push_back(
        netsec::make_target_record<VoidFn>(&never_referenced, function_body_size, false));

    // 模拟 ICF：两个签名不同但机器码被折叠的函数映射到同一个入口。
    const void* folded_entry = entry_of(&add_two);
    module_records.push_back(
        TargetRecord{folded_entry, std::type_index(typeid(DoubleUnary)),
                     function_body_size, true});

    registry.load_module("test-module", module_records);

    // 1. 合法回调：入口精确命中、已取地址、类型完全相同。
    expect("legal callback accepts exact typed entry",
           registry.validate<IntUnary>(&add_one).status, CheckStatus::Ok);

    // 2. 类型不同：同一入口存在，但调用点提交的期望类型不匹配。
    expect("different function type is rejected",
           registry.validate(entry_of(&add_one),
                             std::type_index(typeid(DoubleUnary))).status,
           CheckStatus::TypeMismatch);

    // 3. 未取地址函数：即使入口和类型正确，也禁止间接调用。
    expect("function whose address was never taken is rejected",
           registry.validate(entry_of(&never_referenced),
                             std::type_index(typeid(VoidFn))).status,
           CheckStatus::AddressNotTaken);

    // 4. 入口偏移 4 字节：落在函数体内部，不是合法入口。
    const void* interior = offset_entry(entry_of(&add_one), 4);
    expect("pointer four bytes into function body is rejected",
           registry.validate(interior, std::type_index(typeid(IntUnary))).status,
           CheckStatus::InteriorPointer);

    // 5. ICF 折叠别名：同入口登记过不同类型，不能共享类型，失败关闭。
    expect("folded aliases with different types are rejected",
           registry.validate(folded_entry, std::type_index(typeid(IntUnary))).status,
           CheckStatus::FoldedAliasConflict);
    expect("folded alias alternate type is also rejected",
           registry.validate(folded_entry, std::type_index(typeid(DoubleUnary))).status,
           CheckStatus::FoldedAliasConflict);

    // 6. 未登记地址不属于任何函数入口。
    int stack_candidate = 0;
    expect("unknown address is rejected",
           registry.validate(&stack_candidate, std::type_index(typeid(IntUnary))).status,
           CheckStatus::UnknownTarget);

    // 7. 受控调用路径：合法函数可实际调用，非法函数抛出 CFI 异常。
    netsec::Registry clean_registry;
    clean_registry.register_target(
        netsec::make_target_record<IntUnary>(&add_one, function_body_size, true));

    bool call_succeeded = false;
    try {
        call_succeeded = (checked_call_with(clean_registry, &add_one, 41) == 42);
    } catch (const netsec::Violation&) {
        call_succeeded = false;
    }
    std::cout << (call_succeeded ? "[ PASS ] " : "[ FAIL ] ")
              << "checked call invokes legal callback\n";
    if (!call_succeeded) {
        ++failures;
    }

    bool violation_thrown = false;
    try {
        DoubleUnary* mistyped =
            reinterpret_cast<DoubleUnary*>(const_cast<void*>(entry_of(&add_one)));
        checked_call_with(clean_registry, mistyped, 1.0);
    } catch (const netsec::Violation& error) {
        violation_thrown = error.status == CheckStatus::TypeMismatch;
    }
    std::cout << (violation_thrown ? "[ PASS ] " : "[ FAIL ] ")
              << "checked call throws on type mismatch\n";
    if (!violation_thrown) {
        ++failures;
    }

    if (failures != 0) {
        std::cout << failures << " CFI registry test(s) failed\n";
        return EXIT_FAILURE;
    }
    std::cout << "all CFI registry tests passed\n";
    return EXIT_SUCCESS;
}
