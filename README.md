# netsec — 策略到 classic BPF 的可嵌入编译器

C++17、无第三方依赖的 seccomp 风格沙箱组件：把声明式策略编译成 classic
BPF（`struct sock_filter`）程序，并内置一个平台无关的 BPF 解释器用于离线
验证。头文件 `netsec.hpp` 给出全部 API，`netsec.cpp` 是实现，可直接加入任意
目标工程一起编译（也提供 CMake 的 INTERFACE 目标 `netsec`）。

## 针对的三类绕过

1. **伪造 ABI 的 arch 值**：i386 等 ABI 的调用号布局不同，若先比调用号再查
   arch，攻击者可用别的 ABI 编号命中“允许”路径。生成的程序第一条就从
   `seccomp_data.arch` 加载并与 `AUDIT_ARCH_X86_64`（`0xC000003E`）比较，
   不匹配立即返回 `SECCOMP_RET_KILL_PROCESS`。
2. **x32 调用号位**：x32 与 x86_64 共用 arch 值，但调用号带 `0x40000000`
   位（`X32_SYSCALL_BIT`），可与原生编号别名。通过 `BPF_JSET` 检测该位，
   命中即杀死进程，因此策略里的原生编号永远不会被 x32 别名命中。
3. **64 位参数只比低 32 位**：每个参数按两个 32 位字读取（小端序，低字在
   偏移 `16+8*i`，高字在 `+4`）。所有比较都先判高字再判低字，高字有决定
   权时不会落到低字判断，杜绝把有效位藏进高字绕过。

## 生成程序的固定结构

```text
LD  arch;  JEQ AUDIT_ARCH_X86_64 -> arch_ok;  RET KILL_PROCESS
arch_ok: LD nr; JSET 0x40000000 -> x32_kill;  (继续)
x32_kill: RET KILL_PROCESS
对每条规则（保持策略顺序，first-match）：
    LD nr; JEQ 规则编号 -> 命中; 否则跳到下一条
    命中后按序 AND 求值各 Condition（每个条件加载高/低两个字）
    全部成立 -> RET 规则动作；任一失败 -> 跳到下一条
RET 默认动作
```

支持的参数算子：`Eq / Ne / Lt / Le / Gt / Ge`，均为无符号 64 位语义。
动作：`Allow / KillProcess / KillThread / Trap / Errno / Log`，`Errno`
携带 16 位 errno 数据。

## 硬性限制

- 程序超过 `MAX_BPF_INSTRUCTIONS`（4096）条时 `compile()` 抛
  `CompileError`；实测 1300 条无条件规则生成 3907 条，1400 条被拒绝。
- classic BPF 条件跳转只有 8 位偏移，所有跳转以符号标签生成、最后回填；
  目标超出 8 位前向范围同样抛 `CompileError`，绝不静默截断。

## 内置解释器与差分验证

`BpfInterpreter` 执行编译器用到的 BPF 子集（`LD_ABS` 取字、`JEQ/JGT/JGE/
JSET/JA` 跳转、`RET`），按小端序解析 64 字节 `seccomp_data`，宿主机无需
Linux/seccomp 即可运行。

`verify()` 对同一输入分别跑解释器和不经过 BPF 的参考策略求值器
`evaluate_policy()`，逐字节比较返回值，并枚举：

- 外部 arch（i386 等）必须被杀；
- `[0, nr_limit)` 全部原生调用号，外加策略引用编号及其相邻编号；
- 带 x32 位的调用号（含被允许编号的 x32 别名）；
- 每条带条件规则的参数边界：`0`、阈值 `V±1`、`UINT64_MAX`，以及只翻转
  高 32 位字的值（`V ± 2^32`），专门暴露“只比低字”的错误实现。

同时校验指令数 ≤ 4096 与所有跳转偏移落在程序范围内。

## 用法

```cpp
#include "netsec.hpp"
netsec::Policy policy;
policy.default_action = netsec::Action::KillProcess;
policy.rules.push_back({2, netsec::Action::Errno, 1, {}}); // open -> EPERM
policy.rules.push_back(
    {1, netsec::Action::Allow, 0,
     {netsec::Condition{0, netsec::Cmp::Le, 2}}}); // write 仅 fd<=2

std::vector<netsec::BpfInsn> filter = netsec::compile(policy);
auto report = netsec::verify(policy); // 离线差分验证
```

`examples/demo.cpp` 演示完整流程与反汇编；产出的 `BpfInsn` 与
`sock_filter` 布局一致，可在 Linux 上拷入 `sock_fprog` 后经
`prctl(PR_SET_SECCOMP, SECCOMP_MODE_FILTER, ...)` 安装（本库不封装系统
调用，保持可移植与可嵌入）。

## 构建与测试

```sh
cmake -S . -B build
cmake --build build
ctest --test-dir build --output-on-failure
./build/netsec_demo
```

测试在 macOS arm64 与 Linux 上均可运行（解释器不依赖宿主机 seccomp），
并已在 `-Wall -Wextra -Wpedantic -Werror -fsanitize=address,undefined`
下通过。
