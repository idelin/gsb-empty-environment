# Multi-Paxos 共识日志 + 反向补偿事务编排 (TypeScript)

无单点故障的多数派共识日志协议，以及构建在其上的长生命周期事务反向补偿编排引擎。
纯 TypeScript、零依赖，使用 Node.js 内置类型擦除与 `node:test` 运行（Node ≥ 22.6，推荐 23+）。

## 运行

```bash
node --test          # 全部测试（19 个）
node examples/demo.ts   # 端到端演示：选举 → 分区 → 修复 → 补偿 → 收敛
```

## 架构

```
src/types.ts      Ballot / LogEntry / Command / SagaEvent 等共享类型
src/network.ts    可控内存网络：单向链路、分区、消息审计（同步确定性投递）
src/paxos.ts      Multi-Paxos 节点：Prepare/Promise + Accept/Accepted、
                  领导者租约、空洞修补、少数派脏日志安全覆写、无空洞提交
src/cluster.ts    确定性集群驱动（手动时钟、分区/愈合、提交等待器）
src/saga.ts       Saga 状态机（由已提交日志重建）+ 反向补偿编排器 + 幂等日志
```

### Multi-Paxos 核心

- **两阶段**：Phase 1（Prepare/Promise）仅在选主时运行；Phase 2（Accept/Accepted）
  在租约有效期内对连续 LogEntry 直接提交，不再逐条 Prepare。
- **领导者租约**：提交推进与心跳自续租；跟随者在租约内拒绝其他候选者的 Prepare。
  安全性始终由 ballot 保证（租约只是活性优化）：旧 Leader 分区期间可继续
  propose，但少数派永远达不到多数派确认，无法提交。
- **空洞修补**：新 Leader 当选后合并多数派上报的日志（逐槽位取最高 ballot），
  对已提交前缀直接认领；对未提交槽位用最高 ballot 值重提议，无人持有的槽位
  填 no-op；追赶（catch-up）时用当前 ballot 重盖已提交值，安全覆写少数派脏日志。
- **无空洞提交**：提交指针只越过"由当前 Leader ballot 确认"的连续前缀；
  脏条目（旧 ballot）会卡住提交并触发 catch-up，绝不会被误提交。

### 反向补偿编排

- 每个状态迁移（`step-executed` / `step-failed` / `step-compensated` /
  `saga-completed` / `saga-compensated` / `saga-failed`）先提议到共识日志，
  **提交后才生效** —— 编排状态可跨 Leader 故障重建。
- 任一步骤失败 → 按**严格逆序**补偿已执行步骤；补偿有界重试，耗尽后 saga
  标记 `failed`（停在中止态，不静默吞错）。
- **幂等**：`sagaId` 级别提交去重（重复提交返回同一执行）；资源侧
  `Effects.once(key, fn)` 保证副作用恰好一次，跨重试与故障转移不重复。
- **故障恢复**：新 Leader 上的编排器从已提交事件重建状态，跳过已提交的
  步骤，从断点继续。

## 测试覆盖（19 个用例）

- `test/paxos.test.ts`：选举唯一性、租约快速路径（断言零 Prepare）、
  非 Leader/租约过期拒绝、无空洞提交与追赶、旧 ballot 拒绝。
- `test/partition.test.ts`（验收边界）：5 节点分区 {n1,n2}|{n3,n4,n5}，
  旧 Leader 被多数派新 Leader 取代；Phase 1 保留法定人数可见的未提交值、
  未知槽位填 no-op、愈合后安全覆写少数派脏日志；多次换主后已提交前缀不变。
- `test/saga.test.ts`：逆序补偿顺序、首步失败空补偿、幂等重复提交、
  补偿重试恰好一次、补偿永久失败进入 failed、断点恢复不重复执行、
  20 个并发 saga 共享账本最终余额精确且无泄漏、Leader 故障转移中 saga
  跨编排器续跑且副作用恰好一次。
