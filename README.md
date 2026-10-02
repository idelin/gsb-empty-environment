# Piggyback Gossip 成员变动消息分发器 (TypeScript)

SWIM 风格的捎带传播（piggyback gossip）成员事件分发器：节点加入 / 离开 / 疑似 /
存活事件按传播计数队列管理，捎带在常规 Ping/Ack 包头部传输，并限制最大扩散轮数
（`lambda * log2(N)`，默认 `lambda = 3`），实现高带宽利用率的状态传播。

## 结构

- `src/disseminator.ts` — 传播计数队列：去重、按传播次数升序优先捎带、超限丢弃
- `src/membership.ts` — 成员视图表：incarnation 驱动的 join/alive/suspect/leave 合并规则
- `src/node.ts` — 节点：Ping 携带 `getPiggyback()`，Ack 回带对端事件，双向加速扩散
- `src/cluster.ts` — 协议周期仿真：每周期每节点向随机对等节点发一次 Ping
- `tests/` — Node.js 内置 `node:test` 测试（Node >= 23 直接运行 TS）
- `examples/demo.ts` — 100 节点收敛演示

## 运行

```sh
npm test     # node --test
npm run demo # 100 节点 Join 收敛样例
```

## 运行样例（npm run demo）

```
cluster=100 nodes, new member 'node-100' joins via seed node-0
log2(N) = 7 periods budget

period 0: aware=1/100
period 1: aware=3/100
period 2: aware=19/100
period 3: aware=62/100
period 4: aware=96/100
period 5: aware=100/100

converged in 5 periods (<= log2(N)=7)
```

## 测试结果（npm test）

```
✔ piggyback gossip convergence (100 nodes)   # join 在 log2(100)=7 周期内 100/100 感知
✔ Disseminator                                # 计数队列 / 上限轮数 / 去重 / 包大小限制
✔ MembershipTable                             # incarnation 合并规则
✔ retransmitLimit                             # lambda * log2(N)
ℹ tests 12
ℹ pass 12
ℹ fail 0
```

验收边界：新节点 Join 后，变动信息捎带在后续 Ping/Ack 包中，5 个周期
（≤ log2(100) = 7）后全集群 100 个节点均感知并更新成员视图表（`tests/cluster.test.ts`）。
