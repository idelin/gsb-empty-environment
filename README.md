# ATS 域隔离组件

面向 C++20 虚拟化平台的 PCIe ATS/PRI 隔离库。组件的授权键为
`(VM 域, 根复合体/Requester ID, PASID, 地址空间, IOVA)`，任何一项不匹配都会在查询
缓存或页表之前被拒绝。

## 隔离模型

- 设备通过 `attach_device` 独占绑定到一个 VM 域，未绑定设备无法发起转换。
- 每个设备上的 PASID 通过 `authorize_pasid` 显式绑定到某个地址空间。
- 缓存键包含完整的 Requester ID（根复合体、segment、BDF）、PASID、地址空间和 VM 域。
- 调用方必须从物理拓扑和直通配置生成 `DomainId`/`RequesterId`，不能直接采用 guest 提供的值。
- 每个缓存项额外保存域标签、域代际和 PASID 绑定代际，命中时逐项复核。
- `invalidate_domain` 只清除并失效目标域的缓存，其他 VM 的缓存保持可用。
- 页表遍历与失效并发时，遍历结束后会复核代际；若期间发生失效，则返回
  `Status::stale_translation`，不会写入旧映射。
- 快照只包含设备、PASID、地址空间和映射，不包含 ATS 缓存；导入后缓存为空且映射重建。

## 构建与测试

```sh
cmake -S . -B build -DCMAKE_BUILD_TYPE=Debug
cmake --build build -j
ctest --test-dir build --output-on-failure
```

测试覆盖合法直通、跨 VM 命中、PASID/地址空间伪造、域间失效隔离、确定性失效竞态、
热迁移重建以及不可导出页表提供者的迁移拒绝。

## 基本用法

```cpp
#include "netsec.hpp"

using namespace netsec;

AtsIsolation ats;
const DomainId vm = DomainId::from(1);
const RequesterId gpu = RequesterId::make(1, 0, 0x20, 0, 0);
const Pasid pasid = Pasid::from(7);
const AddressSpaceId as = AddressSpaceId::from(0x100);

ats.register_domain(vm);
ats.attach_device(vm, gpu);
ats.authorize_pasid(vm, gpu, pasid, as);
ats.map_range(vm, gpu, pasid, as, 0x10000, 0x80000000, 0x2000,
              MemoryPermissions{true, true, false, false});

const TranslationResult result =
    ats.translate({vm, gpu, pasid, as, 0x10123, AccessType::read});
```

设备直通的 PASID 页表也可以由平台提供：实现 `TranslationProvider::lookup`，并通过
`authorize_pasid` 的 `shared_ptr<TranslationProvider>` 重载注入。平台直接修改该页表后，
必须调用对应的 `invalidate_pasid`/`invalidate_device`/`invalidate_range`/`invalidate_domain`；
代际复核会阻止修改与失效之间已经开始的旧转换落入缓存。

## 热迁移

```cpp
ats.begin_migration(vm);
const ExportResult snapshot = ats.export_snapshot(vm);

const Status imported = destination.import_snapshot(snapshot.snapshot);

ats.retire_domain(vm);
```

`begin_migration` 会冻结目标域的 ATS/PRI 请求并清空该域缓存。导入端使用新的域代际重建
设备绑定、PASID 授权和页表，旧缓存不会迁移。不可导出的自定义提供者需要在
`import_snapshot` 时传入 `ImportResolver` 重建。
