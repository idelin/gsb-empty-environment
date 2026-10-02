# View-Change Log Replicator

单进程、离散时间的视图变更（Raft 风格）日志复制器，TypeScript 实现，
零依赖 —— 使用 Node.js（>= 22.6）原生 TypeScript 类型擦除与内置测试器。
不调用网络协议栈：故障注入、分区与心跳全部由测试时钟（`tick`）驱动。

## 运行

```sh
npm test        # node --test test/*.test.ts
npm run demo    # node examples/demo.ts （验收场景运行样例）
```

## 结构

- `src/raft.ts` — 复制状态机节点：预投票、选举、日志复制、安全截断
- `src/sim.ts` — 离散时间模拟器：消息队列、单向/双向分区、时钟推进
- `test/raft.test.ts` — 验收场景 + 安全属性单元测试（node:test）
- `examples/demo.ts` — 验收场景的可运行演示，打印各阶段集群状态

## 安全属性

- **选举期数单调自增**：`currentTerm` 只在观察到更高 term 或赢得预投票后
  前进，绝不回退（`observeTerm`）。
- **预投票（pre-vote）**：超时后先发起预投票，只有多数派认可才真正自增
  term 发起选举 —— 被隔离的节点无法膨胀自己的 term，重新加入后也不会
  扰乱集群。
- **领导者租约**：在选举超时窗口内收到过心跳的节点拒绝预投票，健康
  领导者不会被无谓改选。
- **日志一致性检查**：AppendEntries 携带 `prevLogIndex/prevLogTerm`，
  不匹配则拒绝，领导者递减 `nextIndex` 重试直至一致。
- **安全截断**：冲突的*未提交*后缀被截断覆写；`commitIndex` 及之前的
  已提交条目永不截断（冲突即拒绝）。
- **提交规则**：领导者只把*当前 term*的条目按多数派复制推进
  `commitIndex`，旧 term 条目随之间接提交。

## 验收场景（test/raft.test.ts 与 examples/demo.ts）

1. **term 1**：C 经预投票当选，3 节点集群提交 `e1,e2,e3`。
2. **隔离 C**（旧主）：C 仍自认 leader，本地追加未提交的 `y4,y5`，
   因无多数派无法提交；A/B 由测试时钟驱动超时，B 经预投票当选
   **term 2**，多数派提交 `e4..e7`。
3. **恢复 C**：B 的高 term（2 > 1）心跳使 C 退位为 follower，一致性
   检查定位分歧点，安全截断 `y4,y5` 并覆写为 `e4..e7`；已提交的
   `e1,e2,e3` 原样保留，C 的 `commitIndex` 推进到 7，日志与 B 完全一致。
