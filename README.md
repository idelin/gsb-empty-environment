# VT-d Scalable Mode / IRTE 策略符号验证库

面向 C++17 虚拟化安全宿主的**可嵌入、零外部依赖**单头库，用于在装载硬件表之前
静态验证 Intel VT-d Scalable Mode 地址翻译树与中断重映射策略。仅标准库；
以 `-std=c++17 -Wall -Wextra -Wconversion -Werror` 编译通过。

## 文件

- `include/vtd_policy.hpp`：完整库（类型、线格式编解码、验证引擎），include 即用。
- `tests/test_vtd_policy.cpp`：零依赖测试套件（自带微型 CHECK 框架）。
- `CMakeLists.txt`：INTERFACE 库 + 测试目标，已开启严格告警并注册 CTest。

## 设计：符号、稀疏、无逐页枚举

所有硬件表都建模为稀疏映射（`std::map`，缺键即不存在的表项），验证器只遍历
**实际存在且 Present=1 的表项**。Second-Level 走查对 4KB/2MB/1GB 叶子统一以
半开区间 `[HPA, HPA+size)` 处理：

- 受保护区重叠：区间相交，首个相交字节的偏移直接换算为最小违例 `IOVA`；
- 跨机内存：`RegionSet::firstUncoveredOffset` 以区间并集求首个未覆盖字节；
- 1GB 大页与 4KB 页工作量完全相同，不存在 `for (page...)` 枚举。

违例按 `(BDF, PASID, IOVA, IRTE index, kind)` 排序，DMA 类违例天然报告最小
`(BDF, PASID, IOVA)`。

## 检查项

1. **设备/PASID 树**：遍历全部已启用 BDF 与 PASID 表项，硬拒绝
   `PGTT=0b100` Pass-Through、保留/未支持 PGTT、各类表项保留位非零、PASID
   目录未启用、引用缺失表，以及 PASID 表项二级根不在所属 VM 的
   `allowedSlRoots` 集合内（跨虚拟机页表根）。
2. **Second-Level**：支持 4 级与 5 级（`fiveLevelSecondLevel`）；对每个有效
   4KB/2MB/1GB 叶子做受保护区相交与合法物理内存覆盖判定，分别报告
   `SlProtectedRegionOverlap` 与 `SlIllegalPhysicalRange`，并校验 PTE 保留位、
   大页对齐与缺失页表节点。
3. **IRTE**：仅审计 Present 表项，要求 `SVT=0b01` 精确匹配、`SQ=0`（任何更宽
   掩码报 `IrteLooseSourceQualifier`）；`SID` 必须对应已启用且已分配的设备；
   对 `NMI`/`SMI`/`ExtINT` 投递，目标 LAPIC ID 必须在
   `authorizedHighPriorityLapics` 白名单内。

## 用法

```cpp
#include "vtd_policy.hpp"
vtd::PolicyInput in;                       // 填充稀疏树、IRTE、VM 表、区间
in.legalPhysical     = vtd::RegionSet(...);
in.protectedPhysical = vtd::RegionSet(...);
vtd::VerifyResult result = vtd::verify(in);
if (!result.ok())
  for (const auto& v : result.violations)  // 已按最小违例排序
    ;
```

## 构建与测试

```sh
cmake -S . -B build && cmake --build build
ctest --test-dir build --output-on-failure
```

测试覆盖：Pass-Through 逃逸、1GB/2MB 大页部分重叠（含最小 IOVA）、跨机内存、
跨 VM 页表根、5 级走查、宽松 `SQ`、伪造 `NMI/SMI/ExtINT`、未知 `SID`、保留位
与 PASID 目录禁用等场景。
