# TDX PAMT / SEPT / TDVMCALL 符号验证库

面向机密计算审计宿主的、可嵌入的 Intel TDX 主机侧状态机符号验证库。
纯 C++17、零外部依赖、header-only，使用
`-Wall -Wextra -Wconversion -Werror -pedantic` 编译通过。

## 建模范围

- **多 TD 隔离域**：`TdxAuditor` 维护多个独立 TD（`TdState`），每个 TD
  拥有独立的 Secure EPT、Shared EPT 与 Epoch/TLB 追踪状态。
- **PAMT 元数据**：`PamtEntry` 记录 HPA 区域的页类型
  （`PT_NDA`/`PT_REG`/`PT_TDR`/`PT_TDCS`/`PT_EPT`）、归属 `owner_tdr`、
  粒度（4KB / 2MB）与 `scrubbed` 清零标志。
- **Secure EPT**：`SeptEntry` 覆盖 `SEPT_PENDING` / `SEPT_MAPPED` /
  `SEPT_BLOCKED` 三态，并记录阻断时的 TLB epoch。
- **Shared EPT**：`SharedEntry` 建模 Shared Bit=1 的共享 GPA 到 NDA 主机帧
  的映射。
- **Epoch/TLB 屏障**：每 TD 维护单调 epoch、当前追踪窗口与每 vCPU 的
  VP.ENTER/VP.FLUSH 退出位图。
- **操作流**：`PAGE.ADD` → `PAGE.AUG` → `TDG.MEM.PAGE.ACCEPT` →
  `SEPT.BLOCK` → `TDH.MEM.TRACK`（全 vCPU 退出）→ `PAGE.REMOVE`，以及
  `TDVMCALL` 缓冲审计。

## 强制的安全不变式

1. **PAMT 唯一归属**：任意 HPA 至多归属一个 TD。跨 TD 双重归属、Secure EPT
   别名、把已归属 HPA 映射进 Shared EPT、以及重用 `scrubbed=false` 的页，
   一律硬拒绝（`PAGE_ALREADY_OWNED` / `PAGE_NOT_SCRUBBED`）。`PAGE.REMOVE`
   后页面回到未清零 NDA 状态，必须显式 `scrub_page` 后才能再分配。
2. **BLOCK→TRACK→REMOVE 顺序与全 vCPU 屏障**：`PAGE.REMOVE` 仅接受
   `SEPT_BLOCKED` 条目，且要求已完成的追踪 epoch 严格新于阻断 epoch；
   未完成追踪（`TRACKING_NOT_COMPLETE`）、窗口未关闭
   （`TRACKING_IN_PROGRESS`）、旧 epoch 重放（`STALE_TRACK_EPOCH`）均被
   拦截。对 `SEPT_PENDING` 页面在 `TDG.MEM.PAGE.ACCEPT` 之前的读写返回
   `SEPT_WRONG_STATE`。
3. **TDVMCALL 共享边界**：交给非可信 VMM 的缓冲必须整体位于 Shared Bit=1
   空间，且每帧都存在于 Shared EPT。私有 GPA（`PRIVATE_GPA_LEAK`）、跨越
   bit47 私有/共享边界（`BOUNDARY_CROSS`/`PRIVATE_GPA_LEAK`）、未映射帧
   （`SHARED_ENTRY_MISSING`）与零长度缓冲均被拒绝。

所有变更操作返回 `Result{Violation, detail}`；违例时模型状态保持不变。

## 集成方式

仅需包含单个头文件，无链接依赖：

```cpp
#include "tdx_audit/tdx_audit.hpp"

tdx_audit::TdxAuditor auditor;
auditor.register_ram(0x10000000ULL, 32ULL * 1024 * 1024);
auditor.create_td(/*td_id=*/1, /*vcpu_count=*/2);
auditor.page_add(1, 0x10000000ULL);
auditor.page_aug(1, 0x0ULL, 0x10000000ULL,
                 tdx_audit::Granularity::k4K);
// PENDING 页面在 ACCEPT 前不可访问
assert(!auditor.guest_private_read(1, 0x0ULL).ok());
auditor.page_accept(1, 0x0ULL, tdx_audit::Granularity::k4K);
assert(auditor.guest_private_read(1, 0x0ULL).ok());
```

## 构建与测试

CMake：

```sh
cmake -S . -B build
cmake --build build
ctest --test-dir build --output-on-failure
```

或直接用编译器（严格标志即本库的验收标准）：

```sh
clang++ -std=c++17 -Wall -Wextra -Wconversion -Werror -pedantic \
  -Iinclude tests/test_tdx_audit.cpp -o tdx_tests && ./tdx_tests
```

测试套件（无外部测试框架，634 项断言）覆盖：

- `test_trackless_reclaim_race`：未 `MEM.TRACK` / 未全 vCPU 刷新即回收竞态；
- `test_cross_td_alias`：同一 HPA 跨 TD Secure EPT 别名与共享映射泄漏；
- `test_unaccepted_access`：未 `ACCEPT` 的 PENDING 页读写、阻断页访问；
- `test_tdvmcall_private_leak`：私有 GPA 与跨边界缓冲交给 VMM；
- 另含正向生命周期、epoch 联锁（TLB 残留重放）、2MB 大页合并/拆分与
  Shared EPT 规则测试。

## 源码布局

- `include/tdx_audit/tdx_audit.hpp`：全部模型与验证逻辑（header-only）；
- `tests/test_tdx_audit.cpp`：自包含测试可执行文件；
- `CMakeLists.txt`：INTERFACE 库目标 `tdx_audit` 与测试目标
  `tdx_audit_tests`。

## 建模约定

- PAMT 以 4KB 帧为基础存储；2MB 区域在 `PAGE.AUG` 时由 512 个 4K `PT_REG`
  帧合并为单条 2M 条目，`PAGE.REMOVE` 时再拆回 512 个未清零 NDA 帧。
- `PAGE.ADD` 负责 PAMT 归属（NDA→PT_REG），`PAGE.AUG` 仅安装 SEPT 条目并
  校验页已由同一 TD 拥有，与真实 TDX 模块职责划分一致。
- Shared EPT 映射只接受 NDA 帧且不取得 PAMT 归属，因此私有页不可能经共享
  EPT 形成别名。
- 该库是安全属性的有限状态符号模型，不模拟缓存、页表层级遍历或具体指令
  编码，聚焦审计所需的归属、状态与屏障不变式。
