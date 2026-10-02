# lease-fenced-lock

带租约隔离机制的分布式互斥锁（TypeScript / Node.js，零依赖）。

## 核心机制

- **TTL 租约**：获取锁即绑定一个 TTL 租约；到期未续期，服务端自动释放锁，从根本上防止持有者崩溃导致的死锁。
- **心跳续期（KeepAlive）**：客户端句柄在后台以 `ttlMs / 3`（可配置）的间隔自动续期，锁的存活时间可远超单次 TTL。
- **崩溃自愈**：持有者假死/网络分区后心跳停止，租约在 TTL 后自然过期，等待队列中的下一个节点被唤醒获锁。
- **Fencing Token 隔离**：每次授权颁发单调递增的 fencing token；受保护资源（`FencedStore`）拒绝携带过期 token 的写操作，隔离“锁已丢失但自己不知道”的僵尸持有者。

## 结构

```
src/
  lock-server.ts   # 锁服务端：租约授予/续期/过期、FIFO 等待队列、fence token 颁发
  lock-client.ts   # 锁客户端：后台心跳续期、suspend()/resume() 模拟假死与恢复
  fenced-store.ts  # 受 fencing token 保护的 KV 存储（模拟下游共享资源）
  errors.ts        # LeaseLostError / StaleFenceTokenError 等
tests/             # node:test 测试（含 5 秒租约超时的验收场景）
examples/demo.ts   # 端到端演示脚本
```

## 运行

要求 Node.js ≥ 22.18（或 ≥ 23.6），直接运行 TypeScript，无需编译、无第三方依赖。

```bash
npm test    # 运行全部测试
npm run demo  # 运行演示场景
```

## 用法示例

```ts
import { LockServer, LockClient, FencedStore } from "./src/index.ts";

const server = new LockServer();
const store = new FencedStore();
const node = new LockClient(server, "node-A");

const handle = await node.acquire("shared-resource", {
  ttlMs: 5000,                // 租约 TTL
  heartbeatIntervalMs: 1500,  // 心跳间隔（默认 ttlMs/3）
});

handle.ensureHeld();                          // 写前确认仍持有锁
store.write(handle.fenceToken, "k", "v");     // 携带 fence token 写共享资源
handle.release();                             // 主动释放（停止心跳）

handle.suspend();  // 模拟假死：停止心跳，租约将在 TTL 后过期
handle.resume();   // 模拟恢复：若租约已过期，句柄进入 lost 状态
handle.on("lost", (err) => { /* 租约丢失通知 */ });
```

## 演示输出（`npm run demo`）

```
[+  0.0s] [A] 获取锁成功 fenceToken=1，TTL=5000ms，心跳已启动
[+  0.0s] [A] 写入 balance=A:100 (fenceToken=1)
[+  0.0s] [B] 请求锁，进入等待队列…
[+  1.5s] [A] 心跳续期成功，租约剩余约 5.0s
[+  3.0s] [A] 心跳续期成功，租约剩余约 5.0s
[+  3.2s] [A] *** 节点假死：心跳停止（模拟崩溃/网络分区） ***
[+  8.0s] [B] A 的租约已超时，获取锁成功 fenceToken=2
[+  8.0s] [A] 租约丢失：lease <uuid> is no longer held
[+  8.0s] [A] 节点恢复，isHeld=false（租约已丢失）
[+  8.0s] [A] 写操作被拒绝 → LeaseLostError: lock "shared-resource" is not held by node-A (state=lost)
[+  8.0s] [A] 绕过客户端直写存储仍被拒绝 → StaleFenceTokenError: stale fence token: provided=1, ...
[+  8.0s] [B] 写入 balance=B:200 (fenceToken=2)，当前值=B:200
[+  8.0s] 演示结束
```

时间线：A 在 +3.2s 假死，最后一次心跳在 +3.0s，租约恰好 5 秒后（+8.0s）自然过期，B 被唤醒获锁；A 恢复后的写操作被两道防线拒绝（客户端 `ensureHeld` + 存储层 fencing token）。

## 测试结果（`npm test`，Node.js v23.9.0）

```
✔ 验收：A 假死 → 租约 5 秒超时 → B 获锁 → A 恢复写操作被 FenceToken 拒绝 (5018.77ms)
✔ 心跳续期使锁存活时间远超原始 TTL (713.78ms)
✔ 假死停止心跳 → 租约过期 → 等待者获锁 → 恢复后句柄标记为 lost (338.22ms)
✔ 快速恢复（租约未过期）则继续保持锁 (157.98ms)
✔ FencedStore 拒绝过期或重复的 fence token (0.87ms)
✔ 主动 release 后等待者获锁，且心跳已停止 (2.66ms)
✔ 锁空闲时立即授权，fence token 单调递增 (96.87ms)
✔ 锁被占用时排队，释放后按 FIFO 顺序授予等待者 (58.98ms)
✔ renew 延长租约；对未知租约续期抛出 LeaseLostError (56.95ms)
✔ 不续期则租约 TTL 到期自动释放，等待者被唤醒 (209.73ms)
✔ 释放未知租约抛出 NotLockHolderError (0.43ms)
✔ AbortSignal 可取消排队中的 acquire (72.67ms)
ℹ tests 12
ℹ pass 12
ℹ fail 0
```

## 说明

- `LockServer` 为进程内实现，用于模拟分布式锁服务端语义（对齐 etcd lease / ZooKeeper ephemeral node）；生产环境中可将其替换为基于 etcd、Redis（Redlock + fencing）或 Consul 的真实后端，客户端与 fencing 语义保持不变。
- fencing token 必须由存储层校验（`FencedStore`），仅客户端自检无法防御“进程暂停后恢复”的僵尸写。
