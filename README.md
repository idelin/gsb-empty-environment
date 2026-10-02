# Chandy-Lamport 一致性快照记录器

在模拟的分布式转账集群上实现 Chandy-Lamport 全局一致性快照算法（TypeScript，零依赖，
直接由 Node.js ≥ 22.6 原生运行）。业务转账全程不暂停，快照通过 Marker 沿 FIFO 信道传播完成。

## 算法要点

- **Marker 发送规则（发起者）**：记录本地状态后，立即向所有出边信道发送 Marker。
- **Marker 接收规则**：进程首次收到某快照的 Marker 时，记录本地状态、向所有出边转发 Marker，
  并开始记录其余入边信道；Marker 到达的那条入边信道记为空。
- **信道记录**：入边信道在记录期间收到的转账计入该信道的在途消息集合，收到该信道 Marker 时停止。
- **一致割**：各进程本地状态 + 各信道在途消息 = 全局一致割；总资产严格守恒。

## 结构

- `src/process.ts` — 进程：余额、转账、快照状态机（记录本地状态 / 信道在途消息）
- `src/simulation.ts` — 离散事件仿真：FIFO 信道（随机时延、保证不超车）、并发转账流量、快照调度
- `src/snapshot.ts` — 快照记录类型与总资产核算
- `src/messages.ts` — 转账 / Marker 消息类型
- `test/chandy-lamport.test.ts` — Node.js 内置测试（`node:test`）
- `examples/sample-run.ts` — 运行样例

## 运行

```sh
npm test            # node --test，5 个测试
npm run example     # 3 节点样例（可传种子：npm run example -- 7）
```

## 验收场景

3 节点集群（A/B/C 各 100，总资产 300）持续并发转账；t=30 时 A 发起快照；
Marker 沿信道流动，各节点记录本地状态与信道在途消息；最终
`Σ本地状态 + Σ在途消息 === 300`，严格守恒（多种子、多集群规模均有测试覆盖）。
