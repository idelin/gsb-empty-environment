# pKVM 页所有权与 Stage-2 一致性审计库

这是一个面向 ARM64 Protected KVM（pKVM）安全审计的 C++17 符号验证库。它不依赖任何第三方库，也不执行真实内存访问；库以 Host、Hyp 和多个隔离 pVM 的页所有权元数据、稀疏 Stage-2 区间映射为状态，对 HVC 序列做事务式校验，并在每一步后搜索隔离性、借用唯一性和机密页擦除违约。

## 快速集成

库是 header-only 的，入口为 `include/pkvm/pkvm.hpp`：

```cpp
#include "pkvm/pkvm.hpp"

pkvm::MachineConfig config{};
config.page_size = pkvm::kDefaultPageSize;
config.entities = {
    {pkvm::kHostEntity, pkvm::EntityKind::Host, {0x100000, 0x100000}, true, "host"},
    {pkvm::kHypEntity, pkvm::EntityKind::Hyp, {0x100000, 0x100000}, true, "hyp"},
    {2U, pkvm::EntityKind::Guest, {0x100000, 0x100000}, true, "pvm-1"},
    {3U, pkvm::EntityKind::Guest, {0x100000, 0x100000}, true, "pvm-2"},
};
config.pages = {{0x100U, pkvm::kHostEntity, false, true, true}};

pkvm::Machine machine(config);
const pkvm::HvcResult result =
    machine.execute(pkvm::host_donate_guest(0x100U, 2U));
if (!result.ok()) {
    // result.witness->step / pfn / code 是最小见证。
}
```

使用 CMake 时链接：

```cmake
add_subdirectory(path/to/pkvm-audit)
target_link_libraries(your_target PRIVATE pkvm::audit)
```

构建和测试：

```sh
cmake -S . -B build -DPKVM_AUDIT_STRICT_WARNINGS=ON
cmake --build build
ctest --test-dir build --output-on-failure
```

严格模式使用 `-Wall -Wextra -Wconversion -Werror`。核心库不需要链接源文件或外部依赖。

## 建模语义

- Host 固定为实体 `0`，Hyp 固定为实体 `1`，隔离 pVM 使用 `>= 2` 的实体号。
- 每个实体拥有一个合法的 Stage-2 物理地址区间；页帧必须完整落在参与操作的每个实体区间内。
- Stage-2 表使用不重叠、页对齐的稀疏 `{phys_base, size, access}` 区间表示；权限仅为 `None`、`ReadOnly`、`ReadWrite`。
- 页的规范状态为 `PKVM_NOPAGE`、`PKVM_PAGE_OWNED`、`PKVM_PAGE_SHARED_OWNED` 或 `PKVM_PAGE_SHARED_BORROWED`。
- `state_for(entity, pfn)` 返回实体视角状态：共享拥有者看到 `SHARED_OWNED`，借用者看到 `SHARED_BORROWED`，其他实体看到 `NOPAGE`。

所有 HVC 都是事务式的：前置条件失败或后置不变式失败时，页元数据和 Stage-2 表回滚，且计入同一个递增审计步号。

| HVC | 前置条件与效果 |
| --- | --- |
| `HOST_SHARE_HYP` | Host 独占拥有页；Host 有映射；安装唯一 Hyp 只读映射；所有者保持 Host |
| `HOST_DONATE_HYP` | Host 独占拥有页；撤销 Host 映射；Hyp 获得唯一 RW 所有权 |
| `HOST_SHARE_GUEST` | Host 独占拥有页且目标为隔离 pVM；安装唯一只读借用映射 |
| `HOST_DONATE_GUEST` | Host 独占拥有页；撤销 Host 映射；目标 pVM 获得唯一 RW 所有权 |
| `GUEST_UNSHARE_HOST` | 调用者必须是该 Host 共享页的唯一 guest borrower；移除借用映射后才恢复 Host 独占 |
| `RECLAIM` | 页面由非 Host 实体独占、无 borrower；机密页必须先擦除；撤销当前所有者并恢复 Host RW |

非 Host 调用 share/donate/reclaim 会被 `InvalidCaller` 拒绝。调用者合法但页的实际 owner 不是自己时会被 `NotOwner` 拒绝，因此已捐给某个 pVM 的页不能由 Host 再次捐赠或共享给第三方。

## 全局不变式

每次 HVC 成功后都会扫描全部已登记页帧：

1. `PKVM_PAGE_OWNED` 页只允许 owner 存在映射；Host、Hyp、其他 pVM 的表项必须为 `NOPAGE`。
2. `PKVM_PAGE_SHARED_OWNED` 页必须恰有唯一 owner 和唯一 borrower；owner 保留至多 RW，borrower 至多只读；任何第三个实体的映射都是隔离违例。
3. `PKVM_NOPAGE` 页不允许被任何实体映射。
4. 所有映射必须页对齐并位于所属实体的合法 Stage-2 范围。
5. 机密页在 guest 获得可访问映射时清除 `scrubbed` 标记；`GUEST_UNSHARE_HOST` 与 `RECLAIM` 在机密页未擦除时返回 `NotScrubbed`。`HvcRequest::scrub=true` 表示该过渡原子地完成擦除。

`verify()` 返回当前状态的最小 `(step, pfn)` 见证。`first_violation()` 返回整个已审计历史中最小违例，包括被拒绝的恶意 HVC 尝试和显式注入的非法表项。`hvc_history()` 保留成功与被拒绝的全部 HVC 尝试。

## 对抗性测试夹具

只通过 HVC 无法构造“恶意 Host 在捐赠后偷偷保留映射”这类底层损坏，因此库提供：

```cpp
machine.simulate_stage2_map_page(pkvm::kHostEntity, 0x100U,
                                 pkvm::Access::ReadWrite);
machine.simulate_stage2_unmap_page(2U, 0x100U);
machine.set_scrubbed(0x100U, false);
```

这些调用也占用审计步号，并立即运行全局验证。`simulate_stage2_*` 会保留注入后的非法映射供后续取证，不像 HVC 那样自动回滚；调用方可在测试结束时恢复夹具。正常协议验证应只使用 `execute()`、`scrub()` 和 `verify()`。

## 测试覆盖

`tests/test_pkvm.cpp` 覆盖：

- 同一 Host 页向两个隔离 pVM 双重借出时在第二步硬拒绝，并报告步号/PFN；
- Host 捐赠给 pVM 后宿主机映射立即消失，人为注入残留 Host 映射时被隔离不变式检出；
- 机密页未擦除时 unshare/reclaim 被拒绝，擦除标记后成功回收；
- guest 的额外借用映射存在时 unshare 被拒绝；
- 共享页 borrower 被提升为 RW 时产生权限违例；
- 单页捐赠会从跨页 Host 稀疏映射中只切掉目标页，并保留相邻页映射；
- `share guest`、`unshare`、`share Hyp`、`donate Hyp`、`reclaim`、`donate guest` 的完整合法生命周期始终通过全局不变式。

## 范围说明

该库建模 pKVM 页所有权协议和安全不变式，不模拟真实 ARM Stage-2 页表格式、内存内容、TLB/ATS 失效时序或硬件并发。`scrubbed=true` 是符号化的擦除完成证明；集成到真实审计器时，应由实际擦除通道或可验证内存清零证据驱动该标记。
