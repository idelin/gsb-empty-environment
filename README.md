# dag-executor

依赖任务执行器（DAG executor），支持**运行中撤销节点**：

- 节点失败或被撤销时，撤销沿依赖边传播：**尚未启动的后继随之撤销，已启动的后继保留自身结果**。
- 每个被撤销节点记录完整**原因链**（`chain`：根因 → … → 本节点）与全部直接原因（`causes`）。
- 多个依赖同时失败时，**按节点编号选主因**（`primaryCause`：数字按数值、纯数字字符串按数值、其余按字典序）。

## 运行环境

Node.js ≥ 22.6（利用原生 TypeScript 类型擦除直接运行 `.ts`，无需编译；推荐 ≥ 23.6）。

```sh
npm test    # node --test tests/*.test.ts
npm run demo
```

## 用法

```ts
import { DependencyExecutor } from './src/executor.ts';

const executor = new DependencyExecutor({ concurrency: 4 });
executor.addNode('a', async () => 'A');
executor.addNode('b', async () => 'B', ['a']);
executor.addNode('c', async () => 'C', ['b']);

const runPromise = executor.run();
executor.revoke('a', 'no longer needed'); // 运行中撤销
const report = await runPromise;
```

## 语义

### 撤销 `revoke(id, message?)`

| 节点状态 | 行为 |
| --- | --- |
| `pending` | 立即撤销（`kind: 'manual'`），并向未启动后继传播 |
| `running` | 标记 `revokeRequested` 并触发 `AbortSignal`；节点跑完并**保留结果**，未启动后继立即撤销 |
| `succeeded` / `failed` / `revoked` | 无操作，返回 `false`（幂等） |

失败（任务抛错）与撤销走同一套传播逻辑。

### 原因链与主因

`RevocationInfo`：

- `causes`：全部“坏”前驱（失败 / 被撤销 / 运行中被请求撤销）。多个依赖同时失败时全部收录，后到的失败会补充进已撤销节点的 `causes` 并重算主因。
- `primaryCause`：`causes` 中节点编号最小者（`compareNodeIds`）。
- `chain`：沿主因一路回溯到根因的路径，如 `['fetch', 'parse', 'validate']`。

### 完成与撤销的竞争

撤销判定是同步的，任务完成在微任务中确认。因此同一 tick 内“先 resolve、后 `revoke`”时，
撤销先生效（后继被撤销），节点自身结果仍在完成时保留；节点进入 `succeeded` 后再 `revoke`
则为无操作。

### 边与图校验

- `addNode` 的 `deps` 中**重复边自动去重**，后继注册与撤销原因均不重复。
- 重复节点 id、未知依赖、依赖环（含自环）均抛错。

## 文件

- `src/executor.ts` — 执行器实现
- `tests/executor.test.ts` — 16 个用例：重复边、菱形依赖、多依赖同时失败选主因、运行中撤销传播、完成/撤销竞争、并发限制等
- `examples/demo.ts` — 运行中撤销的可视化示例
