# pacbti-verifier

一个可嵌入宿主的 C++17 ARM64 PAC/BTI 控制流图符号验证库。它不依赖反汇编器、JSON 库或其他第三方组件，输入是已经解析好的基本块、间接分支目标表、导出入口和异常返回点；输出是稳定的诊断码、说明以及最短见证指令路径。

验证器针对以下攻击面做路径敏感分析：

- `PACIASP`/`PACIBSP` 与 `AUTIASP`/`AUTIBSP` 的密钥域和修饰符不匹配。
- 签名、认证和返回时的 `SP` 相对入口偏移不一致。
- 返回地址尚未签名就写入栈槽，或从错误栈槽恢复。
- `XPACI`/`XPACD` 剥离签名后直接经 `BR`、`BLR` 或 `RET` 跳转。
- `BLR`/`BR`、尾调用和异常返回目标缺失或带错类型的 `BTI` 着陆垫。
- `AUT*` 到实际访存/返回之间存在攻击者可控条件分支的 PACMAN 签名预言机窗口。

## 构建

```sh
cmake -S . -B build -DBUILD_TESTING=ON
cmake --build build
ctest --test-dir build --output-on-failure
```

库目标为 `pacbti::pacbti`。项目在 Clang/GCC 下启用：

```text
-Wall -Wextra -Wconversion -Werror
```

唯一运行时依赖是 C++17 标准库。

## 最小示例

```cpp
#include "pacbti/verifier.hpp"

#include <iostream>

int main() {
    using namespace pacbti;

    BasicBlock entry;
    entry.name = "entry";
    entry.instructions = {
        Instruction::parse("bti c"),
        Instruction::parse("paciasp"),
        Instruction::parse("autiasp"),
        Instruction::parse("ret"),
    };

    Function function;
    function.name = "example";
    function.entry = "entry";
    function.blocks.push_back(entry);

    Program program;
    program.functions.push_back(function);

    ExportEntry exportEntry;
    exportEntry.name = "example";
    exportEntry.function = "example";
    exportEntry.block = "entry";
    program.exports.push_back(exportEntry);

    const VerificationReport report = verify(program);
    std::cout << report.format();
    return report.ok() ? 0 : 1;
}
```

## 输入模型

`Instruction::parse` 支持分析所需的 AArch64 语法子集，包括：

- `paciasp`, `pacibsp`, `autiasp`, `autibsp`
- `pacia`, `pacib`, `pacda`, `pacdb`
- `autia`, `autib`, `autda`, `autdb`
- `xpaci`, `xpacd`
- `ret`, `retaa`, `retab`, `eret`, `brk`
- `blr`, `br`, `bl`, `b`, `b.<cond>`, `cbz`, `cbnz`, `tbz`, `tbnz`
- `bti c`, `bti j`, `bti jc`
- `ldr`, `str`, `ldp`, `stp` 的 offset/pre-index/post-index 形式
- `add`, `sub`, `mov` 的常见栈帧形式

未知助记符会保留为 `Opcode::Other`，不改变安全状态，便于宿主用反汇编文本渐进接入。

`BasicBlock::fallthrough` 必须显式指向条件分支未命中或块末尾顺序落入的后继块。直接分支使用 `Instruction::target`。`Function::entry` 和块名必须存在。

`IndirectBranchTarget` 以“函数 + 基本块 + 指令下标”定位 `BLR`/`BR`：

- `BranchPurpose::NormalCall`：普通间接调用，要求 `BTI c`、`BTI jc` 或隐式 `PACIASP`/`PACIBSP` 着陆垫。
- `BranchPurpose::TailCall`：尾调用，要求 `BTI j` 或 `BTI jc`，并检查 `SP` 是否恢复到入口值。
- `BranchPurpose::Jump`：函数内间接跳转，要求 `BTI j` 或 `BTI jc`。
- `Auto`：`BLR` 视为普通调用；同一函数内的 `BR` 视为跳转；跨函数 `BR` 视为尾调用。

目标寄存器为 `x16`/`x17` 时按 ABI 约定跳过 BTI 着陆垫检查。`target_register` 仅是宿主未填充指令寄存器时的后备字段，实际指令中的寄存器优先。

`ExportEntry` 中非数据导出的函数入口默认必须具备普通调用着陆垫。`ExceptionReturnPoint` 校验 `ERET` 站点以及目标处的 `BTI j`/`BTI jc` 权限。

## 分析规则

每个函数入口从相对 `SP = 0` 开始，按指令级 BFS 枚举可达状态。状态包含：

- `SP` 相对偏移和帧指针恢复能力。
- 会话密钥域 `IA`/`IB`/`DA`/`DB`。
- PAC 签名修饰符寄存器、签名时 `SP`。
- 寄存器与 8 字节栈槽中的签名状态。
- 待消费的认证结果和 PACMAN 窗口内是否经过条件分支。

返回路径要求认证时 `SP` 与签名时一致，普通 `RET` 时 `SP` 回到入口值。`RETAA`/`RETAB` 被视为认证与返回的原子组合。任何带有效 `IA`/`IB` 签名但仍未认证的 `RET` 都会报错。

条件分支的 `ConditionTrust` 默认为 `Unknown`，并在 `Options::treat_unknown_conditions_as_attacker_controlled = true` 时保守视为攻击者可控。若确认某个条件来自可信常量或不可控状态，可标记为 `ConditionTrust::Trusted`。PACMAN 诊断路径从对应 `AUT*` 开始，而不是从函数入口开始，以直接展示最短反例窗口。

## 诊断

`VerificationReport::ok()` 仅在不存在 `Severity::Error` 时返回真。主要诊断码包括：

| 诊断码 | 含义 |
| --- | --- |
| `sp_not_restored_at_authentication` | `AUT*` 时 `SP` 与签名时不一致 |
| `sp_not_restored_at_return` | 返回时 `SP` 未恢复到入口值 |
| `return_address_spilled_unsigned` | `x30` 在有效签名前写入栈槽 |
| `pac_modifier_mismatch` | 密钥域或 PAC 修饰符不匹配 |
| `missing_pac_context` | `AUTIASP`/`AUTIBSP` 没有可追踪的本地签名上下文 |
| `signed_return_not_authenticated` | `RET` 直接使用仍带 PAC 的返回地址 |
| `xpaci_downgrade` | `XPACI`/`XPACD` 后未经认证直接间接跳转 |
| `missing_bti_landing_pad` | 间接目标首指令没有着陆垫 |
| `bti_permission_mismatch` | 着陆垫类型与调用/尾调用权限不符 |
| `export_landing_pad_missing` | 导出函数入口不满足普通调用着陆要求 |
| `exception_target_landing_pad_missing` | 异常返回目标不是 `BTI j`/`BTI jc` |
| `pacman_oracle_window` | `AUT*` 后经条件分支才访存或返回 |

`Diagnostic::path` 是该问题的最短见证路径；BTI 诊断包含分支站点和目标首指令，PACMAN 诊断只包含 `AUT*` 到实际使用之间的最短窗口。

## 测试集

`tests/test_verifier.cpp` 是零依赖测试程序，覆盖：

- 合法 `PACIASP`/`AUTIASP` 序言尾声、pre/post-index 栈恢复和普通调用。
- `PACIASP` 作为隐式 BTI 调用着陆垫。
- `x16`/`x17` BTI 豁免。
- 认证时 `SP` 漂移、返回时 `SP` 未恢复、未签名返回地址溢出。
- `IA`/`IB` 与修饰符寄存器不匹配、缺少签名上下文。
- `XPACI` 后直接 `BR` 的降级路径。
- 缺失 `BTI`、尾调用使用 `BTI c` 的权限错误、尾调用 `SP` 漂移。
- PACMAN 最短窗口、可信条件分支关闭窗口、窗口状态不跨下一次认证泄漏。
- `ERET` 到 `BTI jc` 的合法与非法异常返回目标。

## 适用边界

这是面向固件/内核符号级 CFG 的轻量静态验证器，不是完整 AArch64 模拟器或形式化证明器。它不推断未知内存内容、不建模异常展开、不追踪通用整数污点，也不替代链接后反汇编。宿主应把真实指令、间接目标表和导出边界准确填入模型，并根据目标 ABI 选择条件分支可信度策略。默认路径和状态上限可通过 `Options` 调整；达到上限时会输出 `analysis_limit` 错误，避免把不完整分析误报为通过。
