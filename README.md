# cBPF seccomp 静态与符号验证库

这是一个 C++17、零外部依赖、可嵌入宿主进程的经典 BPF（cBPF）
`seccomp_filter` 验证库。它先按内核加载语义检查指令流，再对控制流图执行
路径敏感的位向量/区间符号分析，并为策略违规生成具体的
`(arch, nr, args[6])` 见证输入。

## 能检测的绕过

- 非目标架构（默认 `AUDIT_ARCH_X86_64`）输入到达 `SECCOMP_RET_ALLOW`，例如
  `int 0x80` 使用的 `AUDIT_ARCH_I386`。
- `nr | 0x40000000` x32 ABI 标志未屏蔽时到达 `SECCOMP_RET_ALLOW`。
- 禁用系统调用到达 `SECCOMP_RET_ALLOW`。
- 64 位参数只检查低 32 位，导致非零高 32 位绕过；例如 `clone` flags 或
  `openat` 参数。
- 策略规定的任意 64 位参数禁止条件 `(arg & mask) == value` 被放行。

每条违规结果都带有可重放的具体 `Witness`，包含 `arch`、`nr`、六个 64 位
参数，以及 `instruction_pointer`（用于过滤器读取 `ip` 时保持见证可重放）。

## 构建与测试

```sh
cmake -S . -B build
cmake --build build --parallel
ctest --test-dir build --output-on-failure
```

库自身和测试均使用以下选项编译：

```text
-Wall -Wextra -Wconversion -Werror
```

## 快速使用

```cpp
#include "cbpf/verifier.hpp"

#include <cstdint>

using cbpf::SockFilter;

constexpr std::uint16_t kLdWAbs  = 0x20;
constexpr std::uint16_t kJeqK    = 0x15;
constexpr std::uint16_t kJsetK   = 0x45;
constexpr std::uint16_t kRetK    = 0x06;

int main() {
    cbpf::Program filter = {
        SockFilter{kLdWAbs, 0, 0, 4},                       // load arch
        SockFilter{kJeqK, 0, 4, cbpf::kAuditArchX86_64},    // foreign -> kill
        SockFilter{kLdWAbs, 0, 0, 0},                       // load nr
        SockFilter{kJsetK, 2, 0, cbpf::kX32SyscallBit},     // x32 -> kill
        SockFilter{kJeqK, 1, 0, 59},                        // execve -> kill
        SockFilter{kRetK, 0, 0, cbpf::kSeccompRetAllow},
        SockFilter{kRetK, 0, 0, 0},
    };

    cbpf::Policy policy;
    policy.denied_syscalls.push_back(cbpf::DeniedSyscallRule{59, true, {}});

    cbpf::ValidationResult validation = cbpf::validate(filter);
    if (!validation.valid()) {
        // 每个错误包含 pc、错误类别和描述。
        return 1;
    }

    cbpf::AnalysisResult result = cbpf::analyze(filter, policy);
    if (!result.secure()) {
        for (const cbpf::Finding& finding : result.findings) {
            // finding.witness.arch / nr / args 即逃逸见证。
            (void)finding;
        }
    }
}
```

`cbpf::sock_filter` 是 `cbpf::SockFilter` 的兼容别名，结构与 Linux
`struct sock_filter` 的四个字段一致，可直接从宿主程序复制构造。

## 校验规则

`validate()` 会拒绝：

- 空程序或超过 4096 条指令的程序。
- 未知/保留操作码、非法操作数、非法 scratch 索引。
- 越界或向后跳转，以及从末尾直接落出的路径。
- 最终指令不是 `BPF_RET`，或可达路径无法到达 `BPF_RET`。
- 读取未初始化的 A/X 寄存器或 scratch 槽。
- 非 32 位、未对齐或越过 64 字节 `seccomp_data` 的绝对加载。

支持的 cBPF 子集覆盖 seccomp 常用指令：32 位
`LD/LDX IMM/ABS/MEM`、`ST/STX`、`TAX/TXA`、算术/移位/位运算、
`JA/JEQ/JGT/JGE/JSET`、`RET K/A`。`seccomp_data` 按 x86_64 小端布局解释：
`nr@0`、`arch@4`、`ip@8`、`args[i]@16+8*i`。`LD ABS` 以 32 位字读取。

## 策略与结果语义

```cpp
struct ArgumentRule {
    std::uint8_t argument;              // 0..5
    std::uint64_t forbidden_mask;
    std::uint64_t forbidden_value;
    bool require_upper_half_check;      // 默认 true
};

struct DeniedSyscallRule {
    std::uint32_t number;
    bool deny_number;                   // true 时该 nr 无条件禁用
    std::vector<ArgumentRule> arguments;
};
```

- `DeniedSyscallRule` 的 `arguments` 使用 `(arg & mask) == value` 的禁止条件。
  当 `value` 含掩码外位时，该条件不可满足。同一规则内的多个参数条件按 AND
  组合；需要 OR 语义时拆成多条 `DeniedSyscallRule`。
- `deny_number == true` 报告 `denied_syscall`；参数条件命中报告
  `denied_argument`。只想约束参数、不整体禁用 syscall 时设
  `deny_number = false`。
- `require_upper_half_check == true` 时，额外寻找“参数条件成立且高 32 位非零”
  的可达 `ALLOW` 路径，报告 `upper_half_ignored`。
- `foreign_architecture`、`x32_abi` 和参数分析都以实际 CFG 路径约束为前提，
  不会把被前置检查杀死的输入误报为逃逸。

分析器使用 SSA 风格的 32 位位向量表达式构建控制流路径，通过内置 CNF/DPLL
位爆破求解器附加架构、syscall、参数和高半掩码约束，再从 SAT 模型解码见证。
因此不仅判断风险，还能给出满足过滤器的具体输入。

## 测试覆盖

`tests/verifier_tests.cpp` 包含：

- 长度、跳转、未初始化寄存器/scratch、`seccomp_data` 越界与缺失返回的静态拒绝。
- x32 掩码绕过。
- 32 位兼容态 `int 0x80` 逃逸。
- 64 位参数高半截断及具体见证。
- `openat` 第三个参数等非零参数索引的 64 位分析。
- 正确检查 arch、x32、syscall 白名单及参数高半的合规过滤器。
- 区间跳转（`JGE`）的路径敏感正向/反向判定。
- `RET A`、scratch、TAX/TXA 的符号返回值。
- 为每个报告项独立回放见证，确认其真实执行到 `SECCOMP_RET_ALLOW`。
