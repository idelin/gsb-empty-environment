# 探活模拟器（liveness probe simulator）

基于离散事件模型的节点存活检测仿真：检测器漏收连续心跳后先**怀疑**节点，
收到**当前代次**心跳证据则**恢复**，超过自适应等待窗口仍未收到证据才**移除**。

## 检测语义

1. 每个心跳间隔到期一个检查点；连续 `missThreshold` 个检查点没有收到心跳 → 节点进入 `suspected`。
2. 怀疑期间收到**当前代次**（或更高代次，即重启后的新代次）心跳 → 恢复为 `alive`，取消移除定时器。
3. 怀疑持续超过**等待窗口** → 节点被 `removed`（终态）。
4. **旧代次心跳**（`generation < 当前已知代次`）一律忽略：不重置漏收计数、不取消移除。

## 自适应等待窗口

每个节点维护一个**容量有限**的历史样本缓冲（默认 32 个网络延迟样本，满了逐出最旧样本）：

```
window = clamp(windowBaseIntervals * interval + mean + windowStdMultiplier * stddev, minWindow, maxWindow)
```

历史样本数量有限时均值/方差估计噪声较大，因此窗口同时受 `minWindowMs` / `maxWindowMs` 约束。
可用 `adaptive: false` 切换为固定窗口（对照组）。

## 离散事件模型

事件堆（最小堆，时间相同按入队序）驱动仿真时钟，事件类型：

- `sendTick`：节点按固定间隔发心跳（暂停/崩溃时不发），到达时间附加网络延迟采样；
- `heartbeatArrival`：心跳到达检测器；
- `detectorTimer`：检测器的检查点 / 移除定时器（移除定时器带 token，恢复即失效）；
- `pause` / `crash` / `restart`：场景脚本注入的节点行为（重启会递增代次）。

仿真器掌握节点真实状态（ground truth），用于判定移除是否为误判、计算检测延迟。

## 场景与指标

`npm start` 运行全部场景：健康基线、短暂暂停、长时间暂停、干净崩溃、旧心跳、
长尾延迟（拥塞突发，自适应 vs 固定窗口对照）。

输出指标**各自独立，不合并为单一分数**：

- **误判次数（false positives）**：节点实际存活却被移除的次数；
- **检测延迟（detection latency）**：真实崩溃时刻 → 移除时刻，输出 avg / p95 / max；
- 参考项：怀疑次数、误怀疑（怀疑后恢复）、正确移除、旧心跳忽略、移除后到达的心跳。

## 运行

需要 Node.js >= 22.6（>= 23.6 起原生支持运行 TypeScript，无需编译）：

```sh
npm test     # node --test
npm start    # node src/main.ts
```

随机数使用固定种子（mulberry32），所有场景结果可复现。

## 目录结构

- `src/eventQueue.ts` — 离散事件最小堆
- `src/history.ts` — 有界历史样本（均值/标准差/分位数）
- `src/detector.ts` — 怀疑/恢复/移除状态机 + 自适应窗口
- `src/node.ts` — 被探活节点（代次、暂停、崩溃）
- `src/simulation.ts` — 事件循环与指标归集
- `src/scenarios.ts` / `src/main.ts` — 场景定义与报告输出
- `test/` — node:test 单元与行为测试
