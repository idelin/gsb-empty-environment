# 主备租约协商状态机(单机模拟)

TypeScript 实现的离散 tick 租约管理状态机模拟器:受界时钟漂移、多数派心跳续期、
网络分区下旧主主动降级,杜绝双主脑裂写入。零依赖,使用 Node.js 内置测试运行器验证。

## 设计

- **离散 tick**:模拟器逐 tick 推进,每个 tick 依次执行 场景动作 → 消息投递(0 延迟消息同 tick 级联)→ 节点定时器;全部事件带 tick 写入事件日志(`src/simulator.ts`)。
- **受界时钟漂移**:每节点持有 `BoundedClock`,本地时间 = 全局 tick + 偏移,偏移限制在 `±maxClockSkew/2` 内,保证任意两节点时钟差 ≤ `maxClockSkew`(`src/clock.ts`)。
- **租约获取**:候选者广播 `LeaseRequest`,多数派 `LeaseAck` 后成为主,租约 `[grant, grant+ttl]`。
- **续期**:主按 `heartbeatInterval` 发送心跳,只有在降级点之前收到多数派 `HeartbeatAck` 才续期(`LEASE_RENEWED`)。
- **主动降级**:主在本地时钟到达 `expiry - maxClockSkew`(保守降级点)时若未续期,主动降级为候选者 —— 即使自己的时钟偏慢,也不会在真实过期后仍自认为是主。
- **防脑裂**:
  - 备节点仅在租约视图到期后才授予新租约,并提前 `networkDelay` 发起选举,使新租约生效点不早于旧租约过期点;
  - 持有未过期租约视图的节点拒绝其他节点的请求(`LEASE_HELD`),低任期请求一律拒绝(`STALE_TERM`);
  - 写请求仅在 `PRIMARY` 且本地时钟未越过保守降级点时被接受,任意 tick 至多一个可写主(测试中以不变式逐 tick 校验)。
- **网络**:消息按 `deliverTick` 排队投递,支持注入/解除节点级网络黑洞(双向丢弃),丢弃事件同样入日志(`src/network.ts`)。

## 参数(验收场景)

| 参数 | 值 | 含义 |
| --- | --- | --- |
| `nodeIds` | A, B, C | 3 节点,多数派 = 2 |
| `leaseTtl` | 50 | 租约时长(tick) |
| `maxClockSkew` | 5 | 最大时钟漂移(tick) |
| `networkDelay` | 5 | 请求/心跳单向延迟(tick) |
| `heartbeatInterval` | 10 | 心跳间隔(tick) |

## 运行

需要 Node.js ≥ 22.6(原生 TypeScript 类型擦除)。`scripts/node-ts.sh` 会自动在 PATH、nvm、n 中挑选合适的 Node。

```sh
npm run demo   # 运行验收场景并打印完整事件流
npm test       # 运行 node:test 内置测试
```

## 验收场景时间线(运行样例)

`npm run demo` 输出节选:

```text
[tick 100] A   ELECTION_START     term=1 localTick=100
[tick 100] A   MSG_SENT           id=1 LeaseRequest A->B term=1 deliverAt=105
[tick 105] B   LEASE_ACK          to=A term=1 expiryLocal=155
[tick 105] C   LEASE_ACK          to=A term=1 expiryLocal=155
[tick 105] A   LEASE_GRANTED      term=1 majority=[A,B] expiryLocal=155 demoteAtLocal=150
[tick 106] sim BLACKHOLE_ON       node=A
[tick 115] sim MSG_DROPPED        id=5 Heartbeat A->B (blackhole)
[tick 149] A   WRITE_COMMITTED    value=w1 term=1 localTick=149
[tick 150] A   PRIMARY_DEMOTED    reason=lease_expired_without_majority_renewal term=1 localTick=150
[tick 150] B   ELECTION_START     term=2 localTick=150
[tick 151] A   WRITE_REJECTED     value=w2 role=CANDIDATE localTick=151
[tick 155] C   LEASE_ACK          to=B term=2 expiryLocal=205
[tick 155] B   LEASE_GRANTED      term=2 majority=[B,C] expiryLocal=205 demoteAtLocal=200
[tick 156] B   WRITE_COMMITTED    value=w3 term=2 localTick=156
[tick 160] sim BLACKHOLE_OFF      node=A
[tick 165] B   LEASE_REJECT       to=A requestTerm=2 reason=LEASE_HELD(holder=B,expiryLocal=205)
[tick 165] C   LEASE_REJECT       to=A requestTerm=2 reason=LEASE_HELD(holder=B,expiryLocal=205)
[tick 170] A   ROLE_CHANGE        CANDIDATE->FOLLOWER (heartbeat from B)
[tick 170] B   LEASE_RENEWED      term=2 expiryLocal=220 demoteAtLocal=215

=== 终态 ===
A: role=FOLLOWER term=2 writes=[w1@tick149] lease(holder=B,expiryLocal=230)
B: role=PRIMARY term=2 writes=[w3@tick156] lease(holder=B,expiryLocal=230)
C: role=FOLLOWER term=2 writes=[] lease(holder=B,expiryLocal=230)
```

关键路径:100 发起 → 105 多数派确认 → 106 黑洞 → 150 旧主主动降级 → 155 新主多数派当选 → 165 旧主请求被 `LEASE_HELD` 拒绝。

## 测试结果

`npm test`(node:test,8 项全部通过):

```text
ok 1 - 验收: 主节点 tick 100 发起租约, 多数派 tick 105 确认
ok 2 - 验收: 黑洞后旧主 tick 150 降级为候选者, 新主 tick 155 由多数派选出
ok 3 - 验收: 新多数派拒绝旧主请求, 旧主最终转为跟随者
ok 4 - 验收: 杜绝脑裂写入 —— 任意 tick 至多一个可写主
ok 5 - 续期: 多数派心跳应答成功时租约持续续期, 主不降级
ok 6 - 安全: 无多数派应答时不授予租约, 候选者按 term 重试
ok 7 - 时钟漂移受界: 偏移超过 maxSkew/2 被拒绝
ok 8 - 时钟漂移受界: 快慢钟节点下旧主降级仍先于新租约生效, 无脑裂
# tests 8
# pass 8
# fail 0
```

## 目录结构

```text
src/clock.ts      受界漂移时钟
src/types.ts      消息 / 配置 / 事件类型
src/network.ts    离散 tick 网络(延迟 + 黑洞)
src/node.ts       租约状态机(主/备/候选 + 续期 + 降级 + 写入门控)
src/simulator.ts  tick 循环、事件日志、场景动作调度
src/scenario.ts   验收场景定义
src/demo.ts       运行样例入口
test/lease.test.ts  node:test 验收与安全性测试
```
