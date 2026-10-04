# 因果广播模拟（有限成员集合）

离线副本可以以任意顺序收到消息，但依赖消息必须先交付。本仓库用 TypeScript
实现一个可测试的因果广播模拟器，并附独立向量前缀模型做交叉验证。

## 模型

- **消息身份**：`(node, epoch, counter)`。每个 epoch 内计数从 1 开始；
  成员重启后切换到新 epoch，计数重新从 1 开始。
- **依赖**：每条消息携带发送者当时已观察到的、每个 `(node, epoch)` 流的
  连续前缀（prefix vector）。
- **交付条件**（两个条件同时满足，缺一不可）：
  1. 本流连续：`counter == 本地该流前缀 + 1`。收到高序号不会自动补缺口，
     缺口消息进入暂存。
  2. 依赖齐备：消息 `deps` 中每个流的前缀都已在本地交付。
- **幂等与冲突**：同一身份重复到达且内容一致 → 幂等丢弃；内容不一致 →
  抛出 `ConflictError`。已回收为墓碑的前缀范围内的迟到消息按重复处理。
- **有界暂存与背压**：缺依赖/有缺口的消息进入每节点有界暂存区
  （`stagingLimit`），满额时抛出 `BackpressureError`（集群层记录为
  背压事件，消息不被接受）。

## Epoch 退休与回收

- `switch` 事件封存旧 epoch（记录最终计数），成员以新 epoch 继续。
- 旧 epoch 的**退休屏障**：所有当前成员都确认了该流的最终前缀后，
  该流才标记为 `retired`。
- `checkpoint` 事件计算**共同稳定前缀**：对每个流取所有当前成员确认前缀
  的**最小值**（绝不是单个节点的最大计数）。只有稳定前缀以内的去重记录
  可回收为墓碑；已退休且稳定前缀覆盖最终计数的流，连墓碑一起回收。

## 输入 / 输出

- 输入事件（`Cluster.apply`）：`broadcast`、`receive`（消息到达）、
  `ack`（确认）、`switch`（epoch 切换）、`checkpoint`。
- 输出：
  - 交付顺序：`cluster.deliveries` / `node.deliveryLog`
  - 待定依赖：`node.pendingDependencies()`（每条暂存消息缺哪些流前缀）
  - 回收区间：`cluster.reclaims`（`{at, stream, from, to, tombstoneDropped}`）
  - 资源计数：`node.resources()`（暂存数、去重记录、墓碑、背压、回收量等）

## 文件

- `src/types.ts` — 消息身份、前缀向量、内容哈希
- `src/node.ts` — 单副本状态机（交付、暂存、背压、回收）
- `src/cluster.ts` — 有限成员集群模拟、确认屏障、checkpoint
- `src/vector-model.ts` — 独立参考模型（不动点闭包 + 交付序列校验）
- `test/causal-broadcast.test.ts` — Node 测试

## 验证内容

- 枚举 4 条消息的全部 24 种到达排列，以及带重复到达的 720 种交错：
  每条依赖先于依赖者交付、无重复交付、无缺口误推进，
  交付集合等于独立模型的可交付闭包。
- epoch 切换与迟到消息的事件排列（重启后行为一致）。
- 离线副本恢复后收敛到与在线副本相同的前缀向量。
- 回收只在共同稳定前缀内发生；资源不变式
  `delivered == dedupRecords + reclaimed`、`staged <= stagingLimit`。

## 运行

需要 Node >= 22.6（推荐 24，直接以类型擦除方式运行 TS）：

```sh
node --test test/causal-broadcast.test.ts
```

如需严格类型检查，安装 `typescript` 与 `@types/node` 后运行
`npm run typecheck`（`tsconfig.json` 已开启 `erasableSyntaxOnly`，
源码不含 enum / 参数属性等不可擦除语法）。
