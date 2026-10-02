# XA Local Resource Manager (TypeScript)

分布式事务的本地参会者资源管理器（RM），遵循 XA 两阶段提交协议：响应协调者的
`Prepare` / `Commit` / `Rollback` 指令。`Prepare` 一旦返回，准备承诺（Prepared
日志 + 资源锁）已 fsync 落盘，进程宕机重启后承诺不丢失、锁依然保持，直到协调者
发出显式 `Commit` / `Rollback` 才释放。

零依赖，仅使用 Node.js 内置模块（`node:fs` / `node:test` 等），TypeScript 代码由
Node 原生类型擦除直接运行。

## 环境要求

- Node.js >= 23.6（原生 TypeScript 类型擦除；本仓库在 v23.9.0 上验证）

## 运行

```bash
npm test    # node --test：单元测试 + 真实 SIGKILL 崩溃恢复验收测试
npm run demo  # 崩溃恢复演示：Prepare -> 杀进程 -> 重启恢复 -> Commit
```

## 协议与 API

```ts
import { ResourceManager } from './src/index.ts';

const rm = ResourceManager.open('./data'); // 打开数据目录并自动从 WAL 恢复
rm.begin('tx-1');                          // 协调者开启事务分支
rm.write('tx-1', 'account:A', { balance: 50 }); // 暂存写 + 加排他锁（严格 2PL）
rm.prepare('tx-1');                        // 投票 YES：Prepared 记录 fsync 落盘，锁保持
// ... 此处即使进程被杀，重启后 ResourceManager.open 仍恢复 PREPARED 状态与锁 ...
rm.commit('tx-1');                         // 协调者决定提交：落盘到 store 并释放锁
// 或 rm.rollback('tx-1');                 // 协调者决定回滚：丢弃暂存写并释放锁
```

错误以 XA 风格错误码报告：`XAER_NOTA`（未知事务）、`XAER_PROTO`（协议状态错误）、
`XAER_LOCK_CONFLICT`（资源被未决事务锁定）。

## 崩溃安全性设计

- **WAL（`wal.log`）**：追加式日志，记录 `begin` / `prepared` / `committed` /
  `rolledback`。每条记录写入后立即 `fsync`，`prepare()` 返回前 Prepared 记录
  （含全部暂存写）已持久化。
- **恢复**：`ResourceManager.open()` 重放 WAL，重建事务状态机；对处于
  `PREPARED` 的未决事务重新加载暂存写并重新加锁，准备承诺跨重启保持。
- **提交落盘（`store.json`）**：`commit` 通过 写临时文件 + fsync + rename +
  fsync(目录) 原子应用暂存写，不会观察到半写文件；重复提交幂等。
- **锁**：内存排他锁表，按资源 key 持有，仅在 `commit` / `rollback` 时释放；
  重启后由恢复逻辑为未决事务重建。

## 目录结构

```
src/rm.ts       ResourceManager：XA 状态机、恢复逻辑
src/wal.ts      追加式 WAL，逐条 fsync
src/store.ts    原子落盘的已提交 KV 存储
src/locks.ts    排他锁表
src/errors.ts   XA 风格错误码
demo/           崩溃恢复演示（真实 SIGKILL 子进程）
test/           node:test 单元测试与崩溃验收测试
```

## 测试结果（Node v23.9.0）

```
✔ prepare promise survives SIGKILL; lock held until explicit commit
✔ rollback after SIGKILL discards the in-doubt transaction
✔ prepare -> commit applies staged writes and releases locks
✔ rollback discards staged writes and releases locks
✔ prepared record is fsynced to the WAL before prepare() returns
✔ restart recovers in-doubt tx: locks held, staged writes visible, commit applies
✔ rollback after restart releases locks without applying writes
✔ protocol violations are rejected with XA-style errors
✔ read-your-writes inside a transaction; committed data readable by others
ℹ tests 9  ℹ pass 9  ℹ fail 0
```

## 演示输出（`npm run demo`）

```
=== 1. Coordinator sends Prepare; RM persists Prepared log and locks the key ===
   worker says: PREPARED tx-1 key=account:A
=== 2. SIGKILL the RM process (simulated crash after prepare) ===
   worker killed; no clean shutdown, no rollback happened
=== 3. Restart RM: state recovered, lock still held, promise intact ===
   status(tx-1) = PREPARED
   locked(account:A) = true holder=tx-1
   staged(tx-1, account:A) = {"balance":50}
   conflict-check: write by tx-intruder rejected (resource "account:A" is locked by transaction "tx-1")
=== 4. Coordinator finally decides: explicit Commit releases lock and persists ===
   COMMITTED tx-1
=== 5. Verify final state in a fresh process ===
   status(tx-1)      = COMMITTED
   locked(account:A) = false
   committed value   = {"balance":50}
```
