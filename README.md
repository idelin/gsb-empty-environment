# MPK/PKU 沙箱字节码扫描与隔离状态机验证库

零外部依赖、可嵌入的 C++17 header-only 库，用于在加载/审计不可信 x86-64
插件代码时静态验证 Intel MPK/PKU（16 个保护键 `PKEY 0–15`、32 位 `PKRU`）
隔离策略。仅依赖 C++ 标准库，纯字节运算，不包含任何系统调用或 x86 内联
汇编，因此可在任意宿主（含交叉审计/离线分析环境）编译运行。

## 威胁模型与检查项

### 1. 全字节偏移滑动操作码扫描

x86-64 为变长指令集，攻击者可把危险操作码藏在普通指令的立即数/ModR/M
字节中，再通过非对齐跳转落入其中。扫描器对代码段的**每一个字节偏移**
（而非仅解码后的指令首地址）执行模式匹配，并输出精确 VA、段内偏移与
字节见证：

| 指令 | 编码 | 违例类型 |
| --- | --- | --- |
| `WRPKRU` | `0F 01 EF` | `HiddenWrPkru` |
| `XRSTOR`（经 XSTATE 间接覆写 PKRU） | `0F AE /5`（校验 ModR/M reg=5） | `Xrstor` |
| `XRSTORS` | `0F C7 /3`（校验 ModR/M reg=3） | `Xrstors` |
| `SYSCALL`（`pkey_mprotect`/`rt_sigreturn` 通道） | `0F 05` | `Syscall` |
| `INT 0x80`（遗留系统调用通道） | `CD 80` | `Int80` |

落在受信任跳板半开区间 `[start, end)` 内的匹配会被显式白名单排除；区间
外的同样字节仍被硬检出。`/digit` 兄弟编码（如 `0F AE /0` FXSAVE）不会
被误报。

### 2. 页表 PKEY 与权限校验

对每个不可信可见的 `PageMapping`：

- `pkey` 必须落在 `[0, 15]`，否则 `InvalidPkey`；
- 不可信映射不得使用默认 `PKEY 0`（`PageUsesPkey0`）或任何宿主受保护键
  （`PageUsesHostPkey`），保证不可信代码只能落入 `PKEY != 0`；
- 可执行页若同时可写则 `CodePageWritable`，阻断运行时自修改注入
  `WRPKRU`（代码页必须严格 `W=0 & X=1`）。

导出入口表与控制流跳转目标逐个校验：允许跳入跳板白名单；否则目标页必须
已映射、属于声明的代码段、可执行、非 PKEY 0、非宿主键，分别产生
`JumpTargetUnmapped` / `JumpTargetNonExecutable` / `JumpTargetPkey0` /
`JumpTargetHostPage`。

### 3. PKRU 入口/出口状态机

显式维护 `HOST <-> GUEST` 两态：

- `HOST --Entry--> GUEST`：对 `PKEY 0` 与每个宿主受保护键，`AD` 与 `WD`
  必须同时置 1（读写全禁），否则 `BadEntryPkru`；
- `GUEST --Exit--> HOST`：`PKRU` 必须严格等于配置的宿主基线
  `host_baseline_pkru`，否则 `BadExitPkru`；
- 状态不匹配的边界（GUEST 中再次 Entry、HOST 中 Exit）产生
  `TransitionOrdering`。

## 快速使用

```cpp
#include "mpk_verify/mpk_verify.hpp"

mpkverify::VerifyConfig cfg;
cfg.host_protected_pkeys = {2};
cfg.host_baseline_pkru = 0;
cfg.trampolines.push_back({0x40000, 0x40100});
cfg.page_table.push_back({0x10000, true, false, true, 1});  // RX, PKEY 1

mpkverify::CodeSegment seg;
seg.base_address = 0x10000;
seg.bytes = {0x48, 0xB8, 0x0F, 0x01, 0xEF /* 隐藏 WRPKRU */,
             0x90, 0x90, 0x90, 0x90, 0x90, 0xC3};
cfg.segments.push_back(seg);

mpkverify::VerifyResult result = mpkverify::verify_sandbox(cfg);
for (const auto& violation : result.violations) {
    std::cerr << mpkverify::to_string(violation) << '\n';
}
```

## 构建与测试

零依赖，直接包含 `include/` 即可。仓库自带两种严格构建方式：

```sh
make test          # clang++ -std=c++17 -Wall -Wextra -Wconversion -Werror -pedantic
cmake -S . -B build && cmake --build build && ctest --test-dir build
```

测试覆盖（`tests/mpk_verify_tests.cpp`，38 项断言）：

- 指令立即数内部隐藏的 `0F 01 EF` 精确偏移与字节见证；
- 头部对齐 `WRPKRU`、跳板白名单内/边界外行为；
- 隐藏 `XRSTOR /5`、`XRSTORS /3` 与兄弟 `/digit` 编码区分；
- `SYSCALL`、`INT 0x80` 与其他 `INT imm8` 区分；
- 可写可执行代码页注入、`PKEY 0` 泄漏、宿主键泄漏、非法键值；
- 跳转/入口目标落入宿主页、PKEY 0 页、不可执行页、未映射页；
- PKRU 入口掩码、出口基线恢复与状态机排序；
- 端到端复合攻击的归因报告（含 `to_string` 可读输出）。

## 目录结构

- `include/mpk_verify/mpk_verify.hpp`：全部实现，header-only；
- `tests/mpk_verify_tests.cpp`：零依赖断言式测试；
- `Makefile` / `CMakeLists.txt`：严格警告构建入口。
