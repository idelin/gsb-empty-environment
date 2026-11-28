# iommu_guard

`iommu_guard` 是一个不依赖第三方库的 C++17 静态库，用于在裸金属启动早期独立核验：

- 原始 ACPI DMAR 二进制表；
- PCIe 设备到 DRHD 的归属关系及桥/Phantom Function 别名冲突；
- Intel VT-d 传统模式 Root/Context 和 Scalable Mode Root/Context/PASID 结构；
- 4 级（AW=48）或 3 级（AW=39）Second-Level Page Table；
- 内核/安全监控内存保护区、敏感 MMIO、RMRR identity 映射和 DID 隔离策略。

所有接口都在 `include/iommu_guard/iommu_guard.hpp` 中。返回值通过 `Issue` 给出错误码、BDF、PASID、DID、冲突 GPA/HPA 区间以及从 Root 到叶子表项的物理地址。

## 布局

```text
include/iommu_guard/iommu_guard.hpp   公共 API、错误码、VT-d 位域常量
src/core.cpp                          区间、BDF、错误串、公共内存寻址辅助
src/dmar.cpp                          ACPI DMAR 原始二进制解析与一致性校验
src/pci.cpp                           DRHD 归属、桥范围、Phantom 别名审计
src/vtd.cpp                           Root/Context/PASID/SLPT 解析与遍历
src/audit.cpp                         保护区、MMIO、RMRR、DID 策略引擎
tests/iommu_guard_tests.cpp           自包含攻防测试
```

## 关键语义

- `AddressRange` 使用半开区间 `[begin, end)`。
- ACPI RMRR 的 `limit` 是闭区间上界；解析器要求 `base` 4 KiB 对齐、`limit[11:0] == 0xfff`、`limit >= base` 且 `limit != UINT64_MAX`，因此 `limit + 1` 不会回绕。
- DMAR `HAW` 是最高物理地址位号，实际物理地址宽度是 `HAW + 1` 位；所有 HPA/表指针都按此上限核验。
- 传统 Context 仅接受 `TT=00b/01b`（二级翻译）；`TT=10b` 被识别为 Pass-Through 绕过，`TT=11b` 被拒绝。
- Scalable Context Entry 按 32 字节解析，校验 PDTS、RID_PASID 和 reserved 字段；第二级地址宽度（AW）与 DID 从 64 字节 PASID Table Entry 读取。
- Scalable Mode 仅接受 `PGTT=0010b`（Second-Level）或 `0011b`（Nested）；`0100b` Pass-Through 被拒绝，其他 PGTT 也不允许用于 DMA 隔离。
- SLPT 叶子项支持 4 KiB、2 MiB、1 GiB 页；大页要求 HPA 按页大小对齐，并按实际覆盖区间与保护/MMIO 区间求交。
- 每级表项的保留位、权限组合、表指针范围和叶子 HPA/GPA 范围都会被检查。非叶子项只允许 Present 与 4 KiB 对齐的下级表指针。
- RMRR 只有在对应设备的 SLPT 上存在覆盖整个 RMRR、逐字节 `HPA == GPA` 的映射时才算满足；同时 RMRR 与内核保护区重叠会单独报告。
- 同一 `DID` 被两个不同 `SecurityDomainId` 使用会产生 `kDidSharedAcrossSecurityDomains`。

## API 示例

```cpp
#include "iommu_guard/iommu_guard.hpp"

using namespace iommu_guard;

auto parsed = parse_dmar(dmar_bytes.data(), dmar_bytes.size());
if (!parsed.ok) {
    for (const Issue& issue : parsed.issues) {
        // issue.code / issue.table_offset / issue.message
    }
}

MemoryRead read = [&](PhysicalAddress address, void* out, std::size_t size) {
    return host_phys_read(address, out, size);
};

TranslationRoot root;
root.root_table_phys = vt_d_root_table;
root.host_address_width = parsed.table.host_address_width;
root.scalable_mode = true;

auto walk = walk_device_translation(root, bdf, 0x42U, read);
if (!walk.issues.empty() || walk.pass_through) {
    // 启动早期 fail-closed
}

IsolationPolicy policy;
policy.protected_memory.push_back(
    {AddressRange{secure_base, secure_end}, "hypervisor"});
policy.sensitive_mmio.push_back(
    {AddressRange{iommu_mmio_base, iommu_mmio_end}, "iommu-mmio", {}});

auto report = audit_isolation(
    parsed.table, topology, root, read, policy,
    assignments_from_topology(topology, root.scalable_mode));
if (!report.ok) {
    // report.issues 中有精确 BDF/PASID/DID/区间/表项地址
}
```

`audit_isolation` 会先执行全量 PCI 归属和 Phantom 审计，再对每个给定设备（或
`assignments_from_topology` 生成的全量已配置设备）执行页表遍历和策略核验。

## 构建与测试

```sh
cmake -S . -B build -DCMAKE_BUILD_TYPE=Release
cmake --build build -j
ctest --test-dir build --output-on-failure
```

库和测试目标均使用：

```text
-std=c++17 -Wall -Wextra -Wconversion -Werror
```

测试覆盖正常 4 KiB/2 MiB/1 GiB 映射、AW 39/48、DMAR 校验和与截断篡改、
INCLUDE_PCI_ALL 冲突、RMRR 对齐/`limit+1` 溢出、桥和 Phantom 别名冲突、
Scalable PGTT Pass-Through、PASID 正常 Second-Level/Nested、保留位非零、
大页跨保护区、未授权 MMIO、RMRR 非 identity 及跨租户 DID 复用。
