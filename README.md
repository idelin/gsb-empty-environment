# 可租约租赁协商状态机（Lease Negotiation State Machine）

单机模拟的主备租约管理状态机，TypeScript 实现，Node.js 原生类型剥离运行（>= 23.6），
零依赖，使用 Node 内置测试运行器验证。全部事件记录在离散 tick 上。

## 设计

三节点集群（A/B/C），多数派 = 2。核心参数见 `src/config.ts`：

| 参数 | 值 | 说明 |
| --- | --- | --- |
| `leaseDuration` | 45 tick | 租约时长 |
| `forwardLatency` / `ackLatency` | 2 / 3 tick | 请求/应答单向网络延迟 |
| `maxClockDrift` | 2 tick | 时钟漂移上界（各节点偏移须在此界内） |
| `heartbeatInterval` | 10 tick | 主节点心跳间隔 |
| `electionTimeouts` | 45/45/50 tick | 各节点选举超时（确定性退避） |

状态机规则（`src/node.ts`）：

- **租约获取**：候选者广播 `RequestLease`，多数派 `GrantLease` 后成为主节点。
- **租约续期**：主节点周期心跳，仅当某轮心跳收到多数派 `HeartbeatAck`（含自身）才延长租约。
- **主动降级**：本地时钟到达 `leaseExpiry` 且未获多数派续期时，主节点自动降级为候选者，
  杜绝过期主继续服务。
- **写栅栏（防脑裂）**：仅当 `localNow < leaseExpiry - maxClockDrift` 时主节点才接受写，
  漂移界内提前停写；持有未过期租约的主节点绝不投票，从节点在 `promisedUntil` 前拒绝投票，
  保证任意 tick 至多一个可写主节点。
- **拒绝旧主**：新主任期内，旧主的过期 `RequestLease`/`Heartbeat`（epoch 不更高）被拒绝。

模拟器（`src/simulation.ts`）：每 tick 依次投递到期消息、执行调度动作、运行节点定时器、
快照各节点写能力；支持注入网络黑洞（按投递时刻对指定链路丢包）。

## 运行

```bash
npm run demo   # 打印验收场景 + 对照场景的完整事件时间线
npm test       # node --test，9 项断言
```

## 验收场景时间线（`npm run demo` 摘要）

```
tick  node  event               detail
100   A     campaign            epoch 1, requesting lease [105, 150)
102   B/C   vote-granted        granted vote to A for epoch 1, promised until 150
105   A     lease-acquired      epoch 1, majority {A,B}, lease [105, 150)
106   -     blackhole-injected  links A<->B, A<->C down for ticks [106, 153)
107..147    message-dropped     Heartbeat A->B / A->C (network blackhole)
147   A     write-accepted      x=1 (lease until 150, drift fence 148)
149   A     write-rejected      x=2: drift fence (local 149 >= 148)
150   A     demoted             lease expired at 150 without majority renewal
150   A/B   campaign            epoch 2, requesting lease [155, 200)
152   C     vote-granted        granted vote to B for epoch 2, promised until 200
155   B     lease-acquired      epoch 2, majority {B,C}, lease [155, 200)
156   A     write-rejected      x=3: not primary (role=candidate)
156   B     write-accepted      y=1 (lease until 200, drift fence 198)
157   B/C   request-rejected    stale RequestLease from A (epoch 2)
157   A     step-down           recognized primary B for epoch 2, lease until 200
160   B     lease-renewed       majority heartbeat acks (round 155), lease extended to 205
```

对照场景（无黑洞）：A 在 110/120/130/… 持续凭多数派心跳应答续期至 205。

## 测试结果

```
✔ tick 100：主节点发起租约申请
✔ tick 105：多数派确认，A 获得租约 [105, 150)
✔ 网络黑洞期间：A 的心跳与续期全部丢失，无 lease-renewed
✔ tick 150：旧主 A 租约到期自动降级为候选者
✔ tick 155：B 由多数派选为新主，租约 [155, 200)
✔ tick 155 之后：新主与多数派拒绝旧主 A 的请求
✔ 写栅栏：时钟漂移受界，租约边界处拒绝写入
✔ 不变量：任意 tick 至多一个具备写能力的主节点（无双主脑裂写）
✔ 对照场景：多数派心跳应答驱动租约续期
ℹ pass 9 / fail 0
```
