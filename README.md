# KPTI / SMEP / L1TF 页表符号隔离验证库

Header-only、零外部依赖的 C++17 库，对成对的内核态 / 用户态 CR3 四级页表
（PML4 → PDPT → PD → PT）做精确符号隔离审计。仅依赖 C++ 标准库，可直接
`#include "kpti/audit.hpp"` 嵌入内核安全审计宿主、快照解析器或崩溃转储工具。

## 设计要点

- 不逐页遍历：只递归稀疏页表树（每表 512 项、固定 4 层），以符号
  `[VA, PA, span]` 区间为单位推理，1 GiB / 2 MiB 大页在单步内完成判定，
  正确处理大页与敏感区间的部分重叠。
- 几何参数运行时可配置：`Geometry::x86_64()` 覆盖 48 位规范虚拟地址与
  52 位物理地址（40 位 PFN 窗口，位 12..51）；`Geometry::mini16()` 驱动
  16 位缩微地址空间，复用同一套引擎做全枚举交叉验证。
- 物理内存通过 `IPhysicalMemory` 抽象注入，自带 `FlatPhysicalMemory`
  快照实现，宿主任意后端（EPT/VMCB、转储格式）只需实现一个读取接口。
- 共享子表（多父节点指向同一表）合法；仅当前根到节点路径上的回边才
  判定为页表环并截断，附带 `structuralErrors` 报告。

## 判定规则

1. KPTI 映射隔离：用户态 CR3 下，除 `TrampolinePage` 白名单（要求 VA/PA
   同时精确匹配）外，任何叶子（含 1G/2M 大页的部分重叠）指向敏感物理区
   即报 `FIND_USER_SENSITIVE`；白名单外内核映射带 `U/S=1` 报
   `FIND_USER_BIT`，带 `G=1` 报 `FIND_GLOBAL_BIT`。用户影子根表若仍存在
   上半区（内核半）Present 槽位，报 `FIND_KERNEL_HALF_PRESENT`。
2. L1TF / Foreshadow：对所有 `P=0` 终端表项（任意层级）以及 Present 但
   `PROT_NONE`（R/W=0、U/S=0、NX=1）叶子，解码 PFN 窗口字段；落在
   `[0, maxRAM)` 内即报 `FIND_L1TF`。NX 位于 PFN 窗口之外，不参与解码。
3. SMEP 与 W^X：`U/S=1 且 NX=0` 且命中内核代码区间报 `FIND_SMEP`；
   `W=1 且 NX=0` 且命中敏感区间报 `FIND_WXORX`。

每条 `EntryWitness` 保存最低违例虚拟地址、叶子基准 VA、物理基址、跨度、
层级以及 PML4/PDPT/PD/PT 四级原始表项。`AuditReport::lowest(mask, ...)`
返回任意违例类别的最低虚拟地址与完整见证。

## 构建与测试

```sh
clang++ -std=c++17 -Wall -Wextra -Wconversion -Werror -Iinclude \
  tests/x86_64_tests.cpp -o x86_64_tests
clang++ -std=c++17 -Wall -Wextra -Wconversion -Werror -Iinclude \
  tests/mini16_crosscheck.cpp -o mini16_crosscheck
```

或使用 CMake / CTest：

```sh
cmake -S . -B build && cmake --build build && ctest --test-dir build
```

- `tests/x86_64_tests.cpp`：干净树、敏感泄漏、2 MiB 大页部分重叠、跳板
  白名单（含 PA 不匹配拒绝）、Swap PTE / PFN 取反 / PROT_NONE 的 L1TF、
  SMEP、W^X、全局位、内核半残留、CR3 对齐校验与几何原语。
- `tests/mini16_crosscheck.cpp`：16 位缩微空间（4096 页全枚举），独立
  oracle 逐页分类与符号审计结果逐页比对，并校验各类违例最低 VA；60 个
  随机稀疏树场景零误差，且全部 7 类违例均被覆盖。
