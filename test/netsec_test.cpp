#include "netsec.hpp"

#include <cassert>
#include <iostream>

using netsec::Func;
using netsec::FuncType;
using netsec::Table;
using netsec::Trap;
using netsec::ValType;
using netsec::Value;

int main() {
    Table table;

    // 表项 0：类型 (i32, i32) -> i32
    const std::size_t add_index = table.emplace(Func{
        FuncType{{ValType::i32, ValType::i32}, {ValType::i32}},
        [](const std::vector<Value>& args) {
            return std::vector<Value>{args[0] + args[1]};
        },
    });

    // 表项 1：参数个数不同的函数，类型 (i32) -> i32
    const std::size_t negate_index = table.emplace(Func{
        FuncType{{ValType::i32}, {ValType::i32}},
        [](const std::vector<Value>& args) {
            return std::vector<Value>{-args[0]};
        },
    });

    const FuncType expected{{ValType::i32, ValType::i32}, {ValType::i32}};

    // 类型全等：间接调用执行
    const auto result = table.call_indirect(expected, add_index, {20, 22});
    assert(result.size() == 1 && result[0] == 42);
    std::cout << "equal types: call executed, result = " << result[0] << '\n';

    // 类型不等（参数个数不同）：陷入，绝不按另一签名解释参数
    bool trapped = false;
    try {
        table.call_indirect(expected, negate_index, {20, 22});
    } catch (const Trap& e) {
        trapped = true;
        std::cout << "mismatched types: trapped: " << e.what() << '\n';
    }
    assert(trapped);

    // 结果个数不同同样陷入
    const std::size_t void_index = table.emplace(Func{
        FuncType{{ValType::i32, ValType::i32}, {}},
        [](const std::vector<Value>&) { return std::vector<Value>{}; },
    });
    trapped = false;
    try {
        table.call_indirect(expected, void_index, {20, 22});
    } catch (const Trap&) {
        trapped = true;
    }
    assert(trapped);

    // 越界表索引：陷入
    trapped = false;
    try {
        table.call_indirect(expected, table.size(), {20, 22});
    } catch (const Trap&) {
        trapped = true;
    }
    assert(trapped);

    std::cout << "all indirect-call type checks passed\n";
    return 0;
}
