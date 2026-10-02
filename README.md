# Piggyback Gossip 成员变动消息分发器 (TypeScript)

SWIM 风格的捎带传播（Piggyback Gossip）组件：将节点 Join / Leave / Suspect / Alive
等成员变动事件放入按传播计数管理的队列，在常规 Ping/Ack 网络包的头部捎带传输，
并以 `lambda * ceil(log2(N))` 限制每条消息的最大扩散轮数，实现高带宽利用率的状态传播。

零依赖，使用 Node.js (>= 22.6) 原生 TypeScript 类型擦除直接运行。

## 结构

- `src/events.ts` — 变动事件类型与 incarnation 冲突裁决规则（alive 同级驳斥 suspect，leave 为终态）
- `src/disseminator.ts` — 捎带分发器：每节点一条最新事件、按传输次数升序选取（最少传播者优先）、达到最大轮数后淘汰
- `src/memberlist.ts` — 成员视图表，应用事件并忽略过期消息
- `src/node.ts` — 协议节点：Ping/Ack/Join 包生成与处理，收到针对自身的 suspect 时提升 incarnation 并广播 alive 驳斥
- `src/cluster.ts` — 协议周期模拟器（每周期每个活跃节点向随机对等点发 Ping，对方回 Ack，双方均捎带事件）
- `examples/sample.ts` — 100 节点 Join 传播运行样例
- `test/` — Node.js 内置测试（`node:test`）

## 运行

```sh
npm test          # node --test "test/*.test.ts"
npm run sample    # node examples/sample.ts
```

## 运行样例输出

```text
cluster ready: 99 slots, steady state seeded for 99 nodes
max gossip rounds per event = lambda * ceil(log2(N)) = 21

node n100 joined via introducer n001 (informed: 2/100)

sample ping packet with piggyback header:
{
  "kind": "ping",
  "from": "n100",
  "to": "n001",
  "seq": 2,
  "piggyback": [
    { "type": "join", "nodeId": "n100", "incarnation": 0 }
  ]
}

period | informed nodes (of 100)
-------+------------------------
     1 | 12
     2 | 47
     3 | 89
     4 | 100

converged in 4 period(s); ceil(log2(100)) = 7
after max-rounds limit: pending gossip events cluster-wide = 0
```

新节点 n100 的 Join 事件被捎带在后续 Ping 包头部，经 4 个协议周期（优于
log2(100) ≈ 7 的上界）全集群 100 个节点均感知并更新成员视图表；事件达到最大
扩散轮数后，全集群捎带队列清空，带宽占用归零。

## 测试结果

```text
✔ max rounds scale with log2(clusterSize)
✔ piggyback selection respects the byte budget
✔ least-transmitted events are picked first
✔ events are evicted after maxRounds transmissions
✔ a fresher event replaces the queued one and resets its count
✔ stale or duplicate events are not re-queued
✔ alive refutes suspect at the same incarnation
✔ stale events are ignored
✔ leave is terminal and overrides alive/suspect
✔ suspect overrides alive only at same or higher incarnation
✔ acceptance: a new node's join is piggybacked and known by all 100 nodes within log2(N) periods
✔ suspect gossip propagates and the suspect refutes it with a higher incarnation
✔ leave gossip propagates to the whole cluster
✔ a node receiving a suspect about itself bumps its incarnation

ℹ tests 14
ℹ pass 14
ℹ fail 0
```
