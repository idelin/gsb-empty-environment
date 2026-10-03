# XA 本地资源管理器（Resource Manager）

分布式事务的本地参会者 RM，响应协调者的 **Prepare / Commit / Rollback** 指令。
核心保证：**Prepare 应答即承诺** —— 准备记录先写 WAL 并 `fsync` 落盘后才返回，
即使进程被 `SIGKILL`，重启后准备承诺与资源锁依然保持，直到协调者显式 Commit/Rollback。

## 运行要求

Node.js ≥ 22.6（使用内置类型擦除直接运行 TypeScript，无需编译；≥ 23.6 可去掉 flag）。

## 结构

- `src/resource-manager.ts` — RM 核心：WAL、崩溃恢复、锁管理、KV 存储落盘
- `test/resource-manager.test.ts` — `node:test` 测试（含真实 SIGKILL 验收场景）
- `demo/child.ts` — 单进程生命周期入口（prepare/check/commit/verify）
- `demo/demo.ts` — 端到端演示：Prepare → SIGKILL → 重启恢复 → 显式 Commit

## 使用

```bash
npm test    # 运行全部测试
npm run demo
```

## 设计要点

- **WAL 先行**：`prepare()` 先把 `{xid, state:"prepared", ops}` 追加到 `rm-wal.log`
  并 `fsyncSync`，成功后才持有内存锁并返回 —— 承诺持久化先于应答。
- **崩溃恢复**：启动时重放 WAL；已 commit/rollback 的事务被遗忘，
  prepared 但未完结的事务恢复为 in-doubt（`recoverScan()`，对应 XA RECOVER），
  并重新获取其全部资源锁。
- **Commit**：先持久化 commit 决策，再应用操作到 `rm-store.json`
  （临时文件 + `rename` 原子替换 + fsync），最后释放锁。
- **锁语义**：prepared 事务锁定的 key 拒绝本地写和其他事务的冲突 prepare；
  锁仅由显式 Commit/Rollback 释放。
- **XA 错误码**：`XAER_DUPID`（重复 prepare）、`XAER_NOTA`（未知事务）、`XA_LOCKED`。

## API

```ts
const rm = new ResourceManager('./data'); // 构造即恢复
rm.prepare('tx-1', [{ type: 'put', key: 'account:A', value: 100 }]);
rm.commit('tx-1');      // 或 rm.rollback('tx-1')
rm.isLocked('account:A');
rm.recoverScan();       // in-doubt 事务列表（XA recover）
```
