# 跨分区等待图诊断器 (wait-graph-diagnoser)

全局死锁检测：即使每个分区的局部等待图都无环，协调器仍能发现跨分区全局死锁。

## 结构

- `src/partition.ts` — 分区本地等待图：拒绝产生本地环的边，用分区级 Lamport 时钟为每条边消息（add/remove）发单调递增版本。
- `src/globalGraph.ts` — 协调器全局图：按边维护高水位版本号，只接受更高版本的消息。乱序/重复投递（如 remove@v2 之后迟到的 add@v1）会被丢弃，**旧边永远不会复活**。
- `src/cycle.ts` — BFS 求**最短环**，返回节点序列与每条边的版本。
- `src/detector.ts` — 诊断器：
  - `snapshot()` 生成带 epoch 的快照；
  - `diagnose()` 发现环后重新校验每条边仍以相同版本存在，快照过期则返回 `stale-snapshot`，**绝不给出撤销建议（避免误杀）**；
  - 受害者只在**仍存活**的事务中按代价挑选，全部已结束则不建议撤销。

## 运行

环境离线，构建脚本使用 Cursor 自带的 TypeScript API（见 `build.js`）。

```
npm test    # 类型检查 + 编译 + 11 个测试
npm run demo
```
