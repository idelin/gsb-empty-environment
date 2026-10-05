# hex-lease

有限轴坐标（axial）六边形地图上的**区域租约**系统：多租户预留单元集合，
`replace` 以一笔事务完成"释放旧集合 + 占用新集合"，带持久化意图/决议、
崩溃恢复与单表串行参考核对。纯 TypeScript（仅可擦除语法），零依赖，
不需要外部数据库；Node ≥ 22.6 直接用内置 type-stripping 运行。

## 运行

```bash
npm test                 # node --test（自动挑选 >=22.6 的 node）
bin/hexlease <dir> ...   # CLI
```

## 模型

- 地图：半径 `radius` 的六边形区域（`|q|,|r|,|q+r| 均 <= radius`），单元键为 `"q,r"`。
- 租约：`{ tenantId, leaseId, cells, version, expiresAtTick }`。
- 容量：每单元整数容量（默认 `defaultCapacity`，可逐单元调整，允许降到
  当前用量之下——既有占用被"祖父化"，但任何新的授予仍按当前容量校验）。
- 额度：租户总单元数额度 = 其所有租约的单元数之和（`set-quota`，`none` 表示不限）。
- 时钟：tick 单调不减；每次请求先处理该 tick 的到期（`expiresAtTick <= tick`），
  再处理请求本身——到期永远先于同 tick 的请求。

## replace 事务语义

`replace(tenant, lease, expectedVersion, newCells, ...)` 在一笔事务里只检查**净变化**：

- 新增单元（new \ old）：逐一检查 `usage < capacity`；
- 保留单元（new ∩ old）：不重复计数，即使该单元已满；
- 租户额度：按 `usage - |old| + |new|` 检查；
- 版本不符（`VERSION_MISMATCH`）或租户不符（`TENANT_MISMATCH`）一律拒绝；
- 全部校验通过才提交：同一同步临界区内原子地对查询可见（跨分区亦然），
  任何一步失败都不会先释放旧集合。

幂等性：`create/replace/release` 携带 `requestId`。相同 requestId + 相同内容
（单元集合按集合语义归一化）→ 返回缓存结果；相同 requestId + 不同内容 →
`REQUEST_CONFLICT`。幂等表随日志持久化，重启后仍然有效。

## 持久化与恢复

每个存储目录包含一个协调者日志和每分区一个效果日志（JSONL，逐条 fsync）：

```
coordinator.log:  meta | intent | commit | abort
part-<p>.log:     effect { txId, partition, cells }
```

事务顺序：`intent → 各分区 effect → commit（提交点）→ 应用内存状态`。
恢复时重放协调者日志：只有带 `commit` 的 intent 被应用；`abort` 或没有任何
决议的 intent（崩溃遗留的**未决事务**）一律不应用——其旧租约继续占满全部
单元与额度，绝不会被当作已释放。分区日志用于审计交叉核对（发现撕裂写）。

## 账本与无超配证明

- `ledger`：逐单元（capacity/usage/leases）与逐租户（quota/usage/leases）账本。
- `verify`：从头重放协调者日志，对每一笔已提交事务重新验证——记录的到期
  清扫与重放一致、每个被授予的单元在授予时刻有空位、每个租户在授予时刻
  未超额；并交叉核对分区日志与已提交效果的一致性。全部通过输出 `PROOF OK`。
  当前快照中超容的单元只可能源于容量下调（祖父化），会单独标注。

## 测试

- `test/unit.test.ts`：核心语义（净变化、版本、到期顺序、幂等/冲突、重启）。
- `test/differential.test.ts`：随机工作负载下，日志化事务存储 vs 单表串行
  参考模型（`src/reference.ts`）逐操作比对结果与状态；混入故障注入
  （分区写失败、提交前崩溃）与周期性重启恢复，并定期运行审计。
- `test/scenarios.test.ts`：跨分区迁移的原子可见性、分区归属变更、旧新集合
  重叠、容量减少（祖父化）、部分写失败中止、崩溃恢复后未决事务不释放额度。
- `test/cli.test.ts`：CLI 端到端。

## CLI 速查

```bash
hexlease db init --radius 3 --default-capacity 1 --partitions 4
hexlease db set-quota acme 5
hexlease db create acme L1 --cells 0:0,1:0 --expires 10 --request r1
hexlease db replace acme L1 --version 1 --cells 1:0,2:0 --expires keep --request r2
hexlease db release acme L1 --version 2 --request r3
hexlease db tick 10
hexlease db set-capacity 0 0 2
hexlease db reassign 0 0 3        # 把单元 0,0 划归分区 3
hexlease db ledger [--all] [--json]
hexlease db verify
hexlease db pending               # 崩溃遗留的未决 intent
```

## 代码结构

- `src/axial.ts`：轴坐标、地图边界、单元→分区哈希。
- `src/model.ts`：类型、错误码、请求内容哈希（幂等）。
- `src/engine.ts`：事务存储共享的状态/效果/校验（规范的校验顺序）。
- `src/store.ts`：`LeaseStore`——分区、两阶段日志、恢复、故障注入钩子。
- `src/reference.ts`：独立的单表串行参考实现（差分核对用）。
- `src/journal.ts`：追加式 JSONL 日志（fsync，容忍尾部撕裂写）。
- `src/ledger.ts` / `src/audit.ts`：账本与无超配证明。
- `src/cli.ts` + `bin/hexlease`：命令行。
