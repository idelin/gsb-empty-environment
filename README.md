# Lease Read Optimizer — 基于租约的多副本强一致读优化器

分布式数据库只读优化技术：主节点在租约有效期内，无需针对每个只读请求向所有从节点
发起网络协商，直接本地读取最新数据，保证**严格线性一致性（strict linearizability）**。

## 一致性原理

1. 主节点通过一轮全量心跳获得法定多数（quorum）从节点承诺：在 `[t, t+ttl]` 内拒绝任何新主当选；
2. 主节点按时钟漂移上界 `ε` 收缩本地有效期：`expiresAt = t + ttl - ε`；
3. 因此剩余租约大于安全窗口（默认 100ms）时，集群中不可能存在更新的主，
   本地已应用全部已提交写 —— 本地读等价于线性化点，零网络开销；
4. 剩余租约不足 100ms 时自动降级：重新发起全量心跳确认并续租后才返回；
   法定多数丢失时主节点拒绝读，绝不返回过期数据。

## 读路径决策

```
read(key):
  if lease.remainingMs() > 100ms:   # 绝对安全窗口
      return localRead              # fast 路径，零网络
  else:                             # 自动降级
      quorumHeartbeatConfirm()      # 全量网络心跳确认 + 续租
      return localRead              # degraded 路径
```

## 运行

需要 Node.js ≥ 22.6（内置 TypeScript 类型擦除与 `node:test`），仓库零依赖。

```bash
nvm use            # 或任意 >= 22.6 的 Node
npm test           # 运行 node:test 测试套件
npm run demo       # 1 万次并发读运行样例
```

## 文件结构

- `src/lease.ts` — 租约管理器（安全窗口判定、时钟漂移收缩）
- `src/primary.ts` — 主节点读优化器（fast/degraded 双路径、quorum 确认）
- `src/follower.ts` — 模拟从节点（网络时延、分区注入、心跳计数）
- `src/clock.ts` — 时钟抽象（测试用可手动推进时钟）
- `src/demo.ts` — 1 万次并发读运行样例
- `test/lease-read.test.ts` — 7 个验收测试
