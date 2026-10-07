# offline-replay

离线编辑操作重放库（TypeScript，Node ≥ 22.6 原生类型擦除运行，零依赖）。

## 问题

离线编辑产生的操作会引用**临时对象 id**（如 `tmp:doc`）。若创建该对象的操作失败，
所有引用它的操作都不可执行；而与之无关的操作不应受牵连。同步时还需把服务器返回的
真实 id 回填给尚未发送的操作，并容忍重复回执与进程重启。

## 设计

- `src/replay.ts` — `ReplayEngine` 核心：
  - **依赖推断**：操作的 `payload` 视为 JSON 树，字符串叶子若等于队列中某操作
    `creates` 的临时 id，即构成依赖边；另支持显式 `dependsOn`。
  - **拓扑执行**：`flush()` 按 Kahn 算法执行，同层按入队顺序，结果确定。
  - **部分失败**：操作失败（回执 `ok:false` 或 transport 抛错）只标记自身 `failed`，
    其传递后继标记 `blocked` 且**不发送**；独立操作继续执行。
  - **id 递归替换**：成功回执携带 `serverIds`（临时 id → 服务器 id），立即深度改写
    所有 `pending` 操作的 payload；链式临时 id（B 引用 A、C 引用 B）逐跳生效。
  - **回执幂等**：`applyReceipt` 对未知或已终态的操作返回 `false`，无副作用；
    重复的 id 映射保留先到的值。
  - **重启恢复**：每次状态变更后将 `{ops, idMap}` 快照写入 `Storage`；
    新实例从快照恢复，已完成操作不重发，服务器重投回执被安全丢弃。
  - **重试**：`requeue(id)` / `requeueAll()` 将 `failed`/`blocked` 重置为 `pending`。
- `src/rewrite.ts` — `rewriteRefs` / `collectRefs`：JSON 树的深度替换与引用收集。
- `src/storage.ts` — `MemoryStorage` 与 `FileStorage`（tmp + rename 原子写入）。

## 用法

```ts
import { ReplayEngine } from './src/replay.ts';
import { FileStorage } from './src/storage.ts';

const engine = new ReplayEngine(
  async (op) => await sendToServer(op),   // 可控 transport，返回 Receipt
  new FileStorage('.replay-queue.json'),
);

engine.enqueue({ id: 'op-1', kind: 'create-doc', payload: {}, creates: 'tmp:doc' });
engine.enqueue({ id: 'op-2', kind: 'insert', payload: { parent: 'tmp:doc' } });

const report = await engine.flush();
// op-1 成功且回执 serverIds: { 'tmp:doc': 'srv-1' } 时，
// op-2 发送前 payload 已被改写为 { parent: 'srv-1' }。
```

## 测试

```sh
npm test    # node --test test/*.test.ts
```

测试用脚本化 transport（可控返回值/抛错）覆盖：拓扑序、部分失败与传递阻断、
链式 id 递归改写、重复回执幂等、重启恢复（含服务器重投回执）、失败后重试。
