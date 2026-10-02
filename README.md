# 跨集群租约联邦与全局互斥控制器

主备跨数据中心（跨机房）全局活跃租约控制器：通过部署在独立故障域的轻量级
第三方**法官（Witness）** 维持跨机房全局单点写入租约；主机房整体掉电/失联时，
法官确认旧租约真正过期后向备用机房颁发**接管租约**（epoch 递增的 Fencing Token），
彻底杜绝双机房双写。

## 架构

```
┌─────────┐   申请/续租   ┌──────────┐   申请/续租   ┌─────────┐
│  机房1  │ ◄──────────► │ Witness  │ ◄──────────► │  机房2  │
│ (主 DC) │              │ (法官)   │              │ (备 DC) │
└────┬────┘              └──────────┘              └────┬────┘
     │ 写(epoch 作为 Fencing Token)                     │
     ▼                                                  ▼
┌──────────┐                                      ┌──────────┐
│ FencedStorage (dc1) │                          │ FencedStorage (dc2) │
└──────────┘                                      └──────────┘
```

| 模块 | 文件 | 职责 |
| --- | --- | --- |
| Witness 法官 | `src/witness.ts` | 全局唯一租约颁发/续租/校验；任一时刻至多一份活跃租约；epoch 单调递增 |
| 租约控制器 | `src/controller.ts` | 机房侧申请/续租/降级；只有持未过期租约才可写 |
| Fencing 存储 | `src/storage.ts` | 记录最大已见 epoch，拒绝过期 epoch 的僵尸写 |
| 故障网络 | `src/network.ts` | 模拟光纤挖断/链路恢复 |
| 时钟 | `src/clock.ts` | 可注入时钟，演练用 `ManualClock` 保证确定性 |

## 安全不变式

1. **单点写入**：法官在旧租约 TTL 耗尽且未续租之前，绝不向其他机房颁发新租约。
2. **失联即停写**：控制器与法官失联时无法续租，本地租约到期后 `isWritable()`
   自动为 false —— 因此新租约颁发之前，旧主必然已停写，无双写窗口。
3. **凭证失效即降级**：恢复后续租被法官告知 epoch 已被取代
   （`StaleLeaseCredentialError`）→ 立即降级为只读备机。
4. **存储层 Fencing**：所有写携带 epoch；存储拒绝小于已见最大 epoch 的写入，
   旧主“僵尸复活”也无法落盘。

> 生产部署时控制器应对租约到期判断施加时钟偏移安全余量（本演练共享确定性时钟）。

## 运行

要求 Node.js ≥ 22.6（直接运行 TypeScript，无需编译）：

```bash
npm run demo   # 运行故障转移演练样例
npm test       # 运行 Node 内置测试（node:test）
```

## 运行样例（`npm run demo` 实际输出）

```
=== t=0ms | 阶段1：正常态 —— dc1 申请租约成为主，dc2 申请被拒保持备 ===
  [t=0] dc1: ACQUIRED lease epoch=1 expiresAt=10000 -> primary
  [t=0] dc1: write ok key=order/1 epoch=1
  [t=4000] dc1: renewed lease epoch=1 expiresAt=14000
  [t=4000] dc1: write ok key=order/2 epoch=1
  [t=0] dc2: acquire denied: lease held by dc1 (epoch=1)
  状态: dc1=primary writable=true | dc2=standby writable=false

=== t=4000ms | 阶段2：光纤挖断 —— sever(dc1,witness), sever(dc1,dc2) ===
  [t=8000] dc1: renew failed: witness unreachable; local lease expires at 14000
  dc1 在本地租约到期前仍可写? true（t=8000 < expiresAt=14000）
  t=14000: dc1 租约本地到期，writable=false（失联期间自动停写）

=== t=14000ms | 阶段3：法官确认 dc1 真正失联（租约过期）-> dc2 晋升为主 ===
  [t=14000] dc2: ACQUIRED lease epoch=2 expiresAt=24000 -> primary
  [t=14000] dc2: write ok key=order/3 epoch=2
  状态: dc1 writable=false | dc2=primary writable=true

=== t=14000ms | 阶段4：链路恢复 —— dc1 续租被法官拒绝（凭证失效），自动降级 ===
  [t=14000] dc1: DEMOTED -> read-only standby (witness credential revoked (lease taken over by peer))
  dc1 写入被拒绝：已是只读备机 ✔
  最终状态: dc1=standby writable=false | dc2=primary writable=true
  数据校验: dc2.read('order/3') = {"amount":300}

演练结束：全程任一时刻至多一个可写主节点，无双写。
```

## 测试结果（`npm test`，node:test 内置运行器）

```
✔ 未持租约的备机不可写
✔ 持租约主节点可写，写入携带 fencing token 落盘
✔ 租约本地到期后自动停写，即使没收到任何通知
✔ 失联期间续租失败不 panic，到期后停写；恢复后续租成功恢复可写
✔ 存储层 fencing：过期 epoch 的僵尸写被拒绝
✔ 跨机房故障转移：全程任一时刻至多一个可写主节点   ← 验收边界端到端测试
✔ 法官向首个申请者颁发租约，epoch 从 1 开始
✔ 租约活跃期间拒绝向其他机房颁发（防双主）
✔ 租约过期后法官可向备机房颁发接管租约，epoch 单调递增
✔ 续租延长过期时间；旧凭证（错误 epoch/持有者）续租被拒绝
✔ 接管发生后旧主凭证失效：validate=false 且续租抛 StaleLeaseCredentialError

ℹ tests 11  ℹ pass 11  ℹ fail 0
```

验收边界对应测试：`test/failover.test.ts`（机房1 主 → 光纤挖断 → 法官确认
失联 → 机房2 获晋升租约 → 机房1 恢复后凭证失效自动切只读备机，全程断言无双写）。
