# Multi-Paxos 多数派共识与无空洞日志提交引擎（TypeScript）

无单点故障的分布式共识日志协议实现，配套高性能无锁并发队列与 CAS 加速原语。
全程去除粗粒度全局互斥锁：所有共享状态迁移基于 `compareExchange` 原子操作与
无锁数据结构完成。仅需 Node.js ≥ 23.6（原生 TypeScript 类型擦除），零依赖。

## 架构

```
src/
├── atomic.ts                  # CAS 原语：AtomicCell（无锁算法基座）+ SharedAtomicCounter（跨线程 Atomics）
├── lockfree/
│   ├── lockFreeQueue.ts       # Michael & Scott 无锁 MPMC 队列（无界，链表 + CAS）
│   └── mpmcRingQueue.ts       # Vyukov 有界 MPMC 环形队列（SharedArrayBuffer + Atomics，可跨 worker）
└── paxos/
    ├── ballot.ts              # 选票 (round, node) 字典序全序
    ├── types.ts               # LogEntry / 消息类型 / NOOP 空洞填充值
    ├── network.ts             # 确定性模拟网络：无锁收件箱、分区、CAS 统计
    ├── node.ts                # Multi-Paxos 节点（两阶段 + 租约 + 空洞修补）
    └── cluster.ts             # 确定性集群仿真器（逻辑时钟 + 消息泵）
workers/                       # 并发压测 worker（ringWorker / counterWorker）
test/                          # Node.js 内置测试运行器（node --test）
examples/demo.ts               # 端到端运行样例
```

## 协议要点

- **两阶段共识**：Phase 1 `prepare/promise(-reject)`，Phase 2 `accept/accepted(-reject)`，
  提交点推进后广播 `commit`（携带全部已提交条目，兼作跟随者空洞回填）。
- **领导者租约绕过 Phase 1**：租约有效期内，Leader 对连续槽位直接发起 Phase 2；
  多数派确认即提交并自动续租。稳态提案产生 **0 条 prepare 消息**（测试断言保证）。
- **空洞修补**：新 Leader 当选后汇总多数派 promise 中的已接受条目，逐槽位取最高选票的值
  重提，无条目槽位填 `NOOP`，保证提交前缀严格连续。
- **安全覆写脏日志**：已提交槽位永不覆写；未提交槽位允许被更高选票的 accept/commit
  覆写——少数派旧 Leader 的脏条目在分区恢复后被安全替换。
- **无锁并发**：承诺选票用 `AtomicCell.compareExchange` 更新；消息收发走无锁 MPSC 收件箱；
  网络统计用 CAS 计数器；跨线程压测走 SAB + Atomics 环形队列。无任何互斥锁。

## 运行

```bash
npm test        # node --test，27 个测试
npm run demo    # 端到端样例：选举 → 稳态提交 → 分区 → 换主 → 恢复收敛
```

## 测试结果（node --test）

```
✔ AtomicCell: load/store 基本语义
✔ AtomicCell: compareExchange 成功时交换并返回旧值
✔ AtomicCell: compareExchange 失败时保持不变并返回当前值
✔ AtomicCell: compareExchange 对引用类型使用 Object.is 同一性
✔ AtomicCell: update 以 CAS 重试循环完成读-改-写
✔ SharedAtomicCounter: 单线程 add / compareExchange
✔ SharedAtomicCounter: 多 worker 并发自增结果精确无丢失
✔ 空队列出队返回 undefined，初始即静默状态
✔ FIFO：单线程入队出队保持顺序
✔ 交错入队出队与参考实现（数组）完全一致
✔ 出队后节点引用被断开，无对象泄漏
✔ 百万级压力：全部出队后严格无残留
✔ 同一元素可重复入队，互不影响
✔ 容量必须为 >=2 的 2 的幂
✔ FIFO 顺序与环绕复用
✔ 满队列入队返回 false，空队列出队返回 EMPTY
✔ 非法元素被拒绝
✔ attach 依附到共享缓冲区并保留状态
✔ attach 拒绝非法缓冲区
✔ 多生产者多消费者并发：元素不丢不重，队列严格排空   (4 生产者 × 3 消费者 × 12 万元素)
✔ 网络分区：旧 Leader 被多数派新 Leader 取代，脏日志被安全覆写   【验收场景】
✔ 多数派已接受的未提交值在 Leader 切换后必须保留（安全性）
✔ 竞选产生唯一领导者，租约内提案绕过 Phase 1 直接提交
✔ 租约过期后提案自动重新竞选（重新执行 Phase 1）
✔ 多数派已接受但存在空洞：新领导者 Phase 1 用 NOOP 补洞后连续提交
✔ 低选票的 Prepare 被拒绝（promise-reject），现任领导者不受影响
✔ 分区中旧领导者的过期 Accept 被拒绝（accept-reject）并下台
ℹ tests 27  ℹ pass 27  ℹ fail 0
```

并发安全与无泄漏断言：MPMC 环形队列在 4 生产者 / 3 消费者下校验 12 万元素
**每个恰好被消费一次**（无丢失、无重复），结束时队列严格排空；所有 Paxos 测试
断言全网在途消息为 0、无悬挂提案、无积压客户端请求；进程无任何定时器/句柄残留。

## 运行样例输出（npm run demo）

```
--- 2. 租约内连续提交（绕过 Phase 1） ---
  3 次提案新增 prepare 消息数: 0 (0 = 完全绕过 Phase 1)

== partitioned: old leader dirty ==
  n1 [Leader(lease)]   commitIndex=3 promised=(1,n1) applied=["set x=1","set y=2","set z=3"]
  ...旧 Leader 在少数派提案 dirty-A/dirty-B（无法提交，dropped=6）

== new leader commits in majority ==
  n3 [Leader(lease)]   commitIndex=5 promised=(2,n3) applied=[...,"set a=10","set b=20"]
  n1 [Leader(expired)] commitIndex=3 promised=(1,n1)   <-- 旧 Leader 被取代

== healed & converged ==
  全部 5 节点 commitIndex=6，applied 完全一致，脏槽位被安全覆写
全网日志收敛: OK
在途消息(泄漏检查): 0
```
