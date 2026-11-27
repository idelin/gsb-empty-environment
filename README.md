# SMMUv3 流表与两阶段页表隔离验证库

这是一个可嵌入的 C++17 头文件库，用于在 ARM64 虚拟化宿主中静态验证
SMMUv3（ARM IHI 0070）配置是否会向设备暴露宿主机敏感内存或其他虚拟机的
物理内存。库无外部依赖，只使用 C++17 标准库。

## 能力

- 按 `STRTAB_BASE_CFG` 解码 linear / 二级流表、`LOG2SIZE`、`SPLIT`
  和保留位。
- 按 L1STD 的 `Span` 解析 `2^(Span-1)` 个 L2 STE；`Span=0` 视为无效
  L2Ptr，检查 `Span > SPLIT+1` 和保留位。
- 解析 STE `V`、`Config`、`S1FMT`、`S1CDMax`、stage-2 TTB 以及 CD
  主字段；直接检测 Bypass、保留 Config、保留位和 `S1CDMax` 越界 CD 槽位。
- 对 Stage-1 的 4KB/64KB 页表和 Stage-2 的 Block/Page 描述符做符号遍历，
  不会按页枚举。2MB/1GB Block 仅作为一个区间处理。
- 对 Stage-1 与 Stage-2 的映射区间求交，并计算读/写权限交集。
- 检测可达宿主机保护物理区、跨 VM 物理区、只读 VM 区的可写映射，以及
  完全落在所有已知 VM/宿主区间之外的物理地址。
- 返回字典序最小的 `(StreamID, SubstreamID, IOVA)` 反例，并附
  L1STD、STE、CD、Stage-1 和 Stage-2 每一级页表遍历见证。

## 构建与测试

```sh
cmake -S . -B build
cmake --build build -j
ctest --test-dir build --output-on-failure
```

测试目标使用 `-Wall -Wextra -Wconversion -Werror`。测试包括：

- 48 组 16 位缩微地址空间的逐地址全枚举交叉验证；
- Bypass、L1STD Span 越界、S1CDMax/CD 容量不一致；
- 2MB 和 1GB Block 与宿主保护区的部分重叠；
- 跨 VM、只读 VM 区、64KB Stage-1、多级稀疏页表级联；
- StreamID/SubstreamID/IOVA 字典序最小反例选择。

## 使用

```cpp
#include "smmuv3_verify/smmuv3_verify.hpp"

smmuv3::VerificationInput input;

// STRTAB_BASE_CFG: FMT=1 (two-level), SPLIT=8, LOG2SIZE=16.
input.stream_table = smmuv3::StreamTable::from_base_cfg(
    (1U << 16U) | (8U << 6U) | 16U);
input.stream_table.l1.resize(256);

smmuv3::L1StreamDescriptor l1;
l1.valid = true;
l1.span = 9; // 2^(9-1) = 256 个 L2 STE
l1.entries.resize(256);
l1.entries[0x40].valid = true;
l1.entries[0x40].config = 4; // Bypass
input.stream_table.l1[0] = l1;

input.device = smmuv3::DeviceScope::stream_ids_inclusive(0x40, 0x40, 0);
input.vm_regions.push_back(
    smmuv3::VmRegion{0, smmuv3::Range{0x400000, 0x600000}, true});
input.host_protected.push_back(
    smmuv3::ProtectedRegion{smmuv3::Range{0x500000, 0x501000}, false});

const smmuv3::Result result = smmuv3::verify(input);
if (!result.secure && result.violation) {
    const auto& v = *result.violation;
    // v.stream_id, v.substream_id, v.iova
    // v.l1, v.ste, v.cd, v.stage1_path, v.stage2_path
}
```

所有地址区间均为半开区间 `[begin, end)`。StreamID 区间同样为半开区间，
但提供 `DeviceScope::stream_ids_inclusive()` 便捷构造。

## 描述符建模约定

- `Ste::s1cdmax` 表示 SubstreamID 位数；可寻址 CD 槽位数为
  `2^S1CDMax`。`CdTable::capacity` 是实际分配的槽位数。若
  `capacity < 2^S1CDMax`，最小越界槽位 `capacity` 会被报告为
  `CdSlotOutOfBounds`。
- `CdTable::slots` 是稀疏槽位映射。落在容量内但缺失的槽位按 SMMU
  读取到无效 CD 处理，即终止该子流，不会凭空产生映射。
- 流表、L1STD、STE 和 CD 的数组/槽位同样按稀疏输入处理；缺失项视为
  无效描述符，而不是自动跨入相邻表。
- `PageTableEntry.kind=Block` 允许出现在非叶级；`Page` 只能出现在 level 3。
  `PageTable.root_level` 和 `granule` 决定每个描述符索引覆盖的输入区间。
  也可直接填写 `PageTableEntry::explicit_input` 来提供显式区间。
- 页表索引默认使用数组位置。稀疏树中应使用
  `PageTableEntry::table(index, children)` /
  `PageTableEntry::leaf(...)`，它们会设置 `explicit_index`。
- Bypass 无论是否命中具体宿主区间都会失败，因为未翻译 DMA 本身已违反
  隔离策略。
- `Cd::epd0=1` 在嵌套配置中视为 Stage-1 bypass、继续检查 Stage-2；
  在 Stage-1-only 配置中直接作为未翻译 DMA 失败。
- `Ste::from_words()` 会解析已定义的 word-0 字段并检查通用 RES0 位；
  实现相关的 word 1..7 保留位可通过调用方设置 `Ste::reserved_bits`
  一并参与检查。API 中所有描述符也都有显式 `reserved_bits` 字段。

## 符号分析

验证器只遍历输入中实际存在的 L1STD、STE、CD 和页表描述符。每个叶描述符
产生一个映射区间，2MB/1GB Block 不被拆成页。Stage-1 映射与 Stage-2
映射按区间求交；宿主保护区和 VM 物理区同样按区间求交。因此运行时间与
描述符数量及映射边界数量相关，与地址空间页数无关。

对每个可达 STE/CD/映射路径，计算该路径上最小的违例 IOVA；最后按
`(StreamID, SubstreamID, IOVA)` 数值字典序选全局最小反例。见证中的
`stage1_path`/`stage2_path` 保存从根表到命中叶描述符的每一级索引、
描述符、输入区间、输出区间和权限。
