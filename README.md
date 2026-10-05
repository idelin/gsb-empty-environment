# 因果广播模拟 (causal-broadcast-sim)

有限成员集合上的因果广播模拟。离线副本可能按任意顺序收到消息,
但因果依赖必须先交付; 成员重启切换 epoch, 旧 epoch 经全员确认屏障后退休,
墓碑与去重记录只在共同稳定前缀达成后回收。

## 核心模型

- **消息身份** `(node, epoch, counter)`: 每个 epoch 内 counter 从 1 开始连续编号。
- **连续前缀**: 每个 `(node, epoch)` 只维护交付水位 `upto` 与退休封印 `seal`
  (每 epoch 两个数字, 不可回收); 逐消息的 dedup 记录才是可回收状态。
- **消息依赖**: 发送方把已观察到的各成员连续前缀 (`"node@epoch" -> counter`) 放进 `deps`。

## 交付规则 (`src/node.ts`)

- 仅当 **本节点序号连续** (`counter == upto + 1`, 高序号不填缺口) 且
  **全部携带依赖已交付** 时才交付; 否则进入**有界暂存**, 超额返回明确
  `backpressure` (不静默丢弃)。
- 重复消息幂等 (`duplicate`); 同身份不同内容报 `conflict`;
  已退休 epoch 上超出 `finalCounter` 的消息报 `sealed`。
- dedup 记录被回收后, 迟到重复靠连续前缀水位判定, 仍幂等
  (代价: 回收后无法再检测该身份的冲突, 这是 GC 的标准取舍)。

## 退休与回收 (`src/cluster.ts`)

- `epochSwitch`: 成员以新 epoch 重启 (计数器归 1), 控制面向所有成员通告旧
  epoch 的封印 `finalCounter`, 并建立**墓碑** (退休记录)。
- **确认屏障**: 所有当前成员 (含重启者自身, 天然确认自己的前缀) 的确认前缀
  都覆盖 `finalCounter`, 退休才 complete。
- `checkpoint`: 共同稳定点 = 各成员确认前缀的**逐键最小值**
  (绝不取单节点最大计数)。dedup 记录按稳定前缀回收并输出回收区间;
  墓碑还需屏障完成才被回收。

## 事件与输出

输入事件: `recv` (消息到达) / `ack` (成员确认前缀) / `epochSwitch` / `checkpoint`。
输出: `deliveries()` 交付顺序、`pending()` 待定依赖 (含缺失明细)、
`reclaimed()` 回收区间、`resources()` 资源计数 (暂存/dedup/墓碑/前缀条目)、
`backpressureLog` 背压记录。

## 验证

- `test/broadcast.test.ts`: 单元测试 (乱序交付、缺口不自动推进、幂等/冲突、
  背压、epoch 切换、屏障、min 稳定点、墓碑回收后行为一致)。
- `test/permutations.test.ts`: 枚举短事件排列 (24 / 720 / 24 种),
  逐步对照**独立向量前缀模型** (`src/model.ts`, 仅集合+查表),
  校验每条依赖在前、无重复、无缺口误推进、背压只在暂存满时发生、
  恢复 (GC) 后重复仍幂等、越界旧纪元仍被拒。

## 运行

需要 Node >= 23.6 (原生类型擦除, 无构建步骤、无第三方依赖):

```sh
node --test test/broadcast.test.ts test/permutations.test.ts
# 或
npm test
```
