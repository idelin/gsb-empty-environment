# Multi-Paxos 共识日志引擎 + 零分配缓存/对象池 (TypeScript)

无单点故障的多数派分布式共识日志协议实现，配合为高频路径设计的零内存分配缓存与对象池，消除 GC 停顿。

## 运行要求

- Node.js ≥ 22.6（利用内置 TypeScript 类型擦除直接运行 `.ts`，无需编译步骤；推荐 ≥ 23.6 默认开启）
- 不依赖任何第三方包（devDependencies 为空）

```bash
npm test    # Node.js 内置测试运行器 (node:test)，33 个用例
npm run demo  # 5 节点网络分区验收场景演示
```

## 架构

| 文件 | 职责 |
| --- | --- |
| `src/paxos-node.ts` | Multi-Paxos 节点：Prepare/Promise + Accept/Accepted 两阶段、领导者租约快速路径、空洞修补、少数派脏日志安全覆写 |
| `src/log-store.ts` | 稀疏复制日志存储：池化 LogEntry、缓存加速读、脏条目原位复用覆写 |
| `src/pool.ts` | `ObjectPool<T>`：自由表复用、双重释放/异物释放检测、`maxOutstanding` 硬上限、泄漏计数器 |
| `src/cache.ts` | `ZeroAllocCache<V>`：直接映射定容缓存，`Int32Array` + 预分配数组，热路径零分配 |
| `src/network.ts` | 确定性模拟网络：分区/愈合、部分投递 (`flushN`)、在途丢包 (`dropPending`) |
| `src/cluster.ts` | 5 节点测试床：逻辑时钟、选举驱动、泄漏断言入口 |
| `demo.ts` | 验收场景端到端演示 |

## 协议要点

- **Phase 1 (Prepare/Promise)**：候选者以单调递增 ballot 发起；接受者授予 promise 的同时授予**定时租约**，并上报所有已接受未提交条目。
- **Phase 2 (Accept/Accepted)**：租约有效期内领导者**跳过 Phase 1**，直接为连续槽位发送 Accept（测试中断言热路径零 Prepare 消息）。
- **空洞修补**：新领导者按各 promise 中最高 ballot 的已选值重提每个未提交槽位；无任何副本的真空洞填 `__noop__`。
- **安全性**：接受者在租约有效期内拒绝外来 Prepare；拒绝低于已承诺 ballot 的 Accept。被隔离的旧领导者无法提交；愈合后高 ballot 的 Accept 安全覆写少数派脏日志；已提交值被覆写会立即抛错（协议违规防线）。
- **追赶**：提交推进遇到日志空洞的跟随者自动发起 `sync-request`，领导者重放日志尾部 + 提交点。

## 零分配设计

- `ZeroAllocCache.get/put`：两次内存读写，无闭包、无装箱、无临时数组；逐出通过构造期注入的 `onEvict` 回调直接归还对象池。
- `ObjectPool.acquire/release`：自由表 pop/push；`release` 先 `reset` 再缓存，杜绝脏状态泄漏。
- `LogStore.accept` 覆写脏条目时**原位复用**同一池化实例——修复路径不创建新对象（测试断言 `totalCreated` 不变）。
- 堆增长断言：20 万次缓存热路径操作后堆增长 < 256KB（`--expose-gc`）。

## 验收场景（demo.ts / paxos.test.ts）

1. 节点 0 当选，租约快速路径提交 slot 0/1。
2. 分区 `{0,1}|{2,3,4}`：slot 2 脏数据仅达少数派；slot 3 达多数派但提交广播丢失。
3. 租约过期，节点 2 在多数派赢得 Phase 1：slot 2 填 noop、slot 3 重提已选值并提交。
4. 愈合后高 ballot Accept 覆写旧领导者脏日志，5 节点日志完全一致，池化条目零泄漏。
