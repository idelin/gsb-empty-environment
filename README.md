# lease-mutex

带租约隔离机制的分布式互斥锁（TypeScript 实现，零依赖，Node.js 内置测试运行器验证）。

## 设计

- **租约（Lease）**：每次授锁绑定一个 TTL。持有方必须持续续期，租约到期未被续期即由服务端自动回收，从根本上避免持锁者死亡导致的死锁。
- **KeepAlive 心跳**：客户端在后台定时器（`ttlMs / 3`，可配置）中自动续期；节点假死/崩溃后心跳停止，租约在最后一次成功心跳 + TTL 后自然超时。
- **等待队列（FIFO）**：同一 key 的竞争者排队，租约释放或过期时按序授予下一个等待者。
- **隔离令牌（FenceToken）**：每次授锁发放单调递增的令牌；受保护资源的写操作要求令牌属于当前持锁者且大于该资源已见过的最大令牌。崩溃恢复的节点拿着旧令牌写数据会被拒绝，防止脑裂写冲突。

## 结构

- `src/lease-lock-server.ts` — 锁服务端（模拟 etcd/ZooKeeper 一类的强一致存储侧）：租约授予/续期/过期、FIFO 等待队列、栅栏令牌校验的受保护读写。
- `src/lock-client.ts` — 客户端：`LockClient.acquire()` 返回 `LockHandle`，自动后台心跳；`suspend()/resume()` 模拟节点假死与恢复；租约丢失时触发 `lost` 事件。
- `test/lease-lock.test.ts` — 7 个用例，含 5 秒租约超时的验收场景。
- `examples/demo.ts` — 完整演示脚本。

## 运行

要求 Node.js >= 22.6（TypeScript 类型擦除，无需编译、无需安装依赖）。

```bash
npm test    # node --test 运行全部测试
npm run demo  # 运行演示脚本
```

## 运行样例（`npm run demo`）

```text
[+0.0s] node-A acquired "shared-resource" (fenceToken=1, ttl=5000ms), keep-alive every 1s
[+0.0s] node-A wrote "A-initial-data" -> store = "A-initial-data"
[+0.0s] node-B is queued, waiting for the lock...
[+1.2s] node-A CRASHED (suspended) - heartbeats stopped, lease will expire 5s after its last heartbeat
[+6.1s] node-B acquired "shared-resource" (fenceToken=2) after lease timeout
[+6.1s] node-A recovered; lease already lost (lockA.lost=true)
[+6.1s] node-A write REJECTED by fencing token: write to "shared-resource" rejected for fence token 1: token does not belong to the current lock holder
[+6.1s] node-B wrote "B-data" -> store = "B-data"
[+6.1s] node-B released the lock. Demo finished.
```

## 测试结果（`npm test`，Node v23.9.0）

```text
✔ mutual exclusion: second acquirer waits until release (114.996292ms)
✔ keep-alive heartbeats keep the lease alive beyond its TTL (1002.834791ms)
✔ lease expires when heartbeats stop and lock is handed to the queue (506.301625ms)
✔ fencing token rejects writes from a stale holder, even after the new holder releases (201.085833ms)
✔ renew on an expired lease throws LeaseExpiredError (302.548333ms)
✔ queued acquire times out when the holder never releases (202.650625ms)
✔ ACCEPTANCE: A hangs, lease times out after 5s, B acquires, A is fenced out on recovery (6020.108125ms)
ℹ node-A suspended at t=1790924778713
ℹ node-B acquired after waiting 4816ms past A's hang
ℹ tests 7
ℹ pass 7
ℹ fail 0
```

## 验收边界对应

1. 节点 A 获取锁并启动心跳（1s 间隔自动续期，TTL 5s）。
2. 模拟节点 A 假死挂起（`lockA.suspend()`，心跳停止）。
3. 租约在最后一次心跳 5 秒后自然超时（A 挂起后约 4.8s 被回收）。
4. 排队中的节点 B 成功获取锁，获得更大的 FenceToken（2）。
5. A 恢复后尝试写操作，被 FenceToken 校验拒绝（`FencingTokenError`），B 的写入不受影响。
