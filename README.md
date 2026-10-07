# workflow-state

一个 TypeScript 工作流状态库：条件分支 + 汇合屏障，事件驱动、幂等、可取消。

## 语义

- **条件启动**：`branch` 在选择时评估各选项的 `when`，仅启动条件满足的子任务，其余标记为 `skipped`。
- **冻结集合**：分支选择结果（`frozenSet`）在选择时刻快照冻结，之后上下文变化不影响本轮执行。
- **汇合语义**：`join`（`branch` / `all` 的完成屏障）只等待实际启动的集合；未选中的分支永远不会阻塞汇合。汇合最多触发一次。
- **幂等完成**：同一任务的重复 `complete` 事件被忽略（记入 `trace` 的 `duplicate-complete`）。
- **取消与迟到证据**：`cancel` 后迟到的成功事件只记入 `evidence`（`late-success-after-cancel`），不推进流程；对 `skipped` / 未激活 / 未知任务的完成同样只记证据。

## 用法

```ts
import { all, branch, createWorkflow, option, task } from './src/workflow.ts';

const def = all('root', [
  task('setup'),
  branch('b1', [
    option('opt-x', (c: { useX: boolean }) => c.useX, task('x')),
    option('opt-y', () => true, task('y')),
  ]),
]);

const wf = createWorkflow(def, { useX: true });
wf.start();
wf.dispatch({ type: 'complete', taskId: 'setup' });
wf.dispatch({ type: 'complete', taskId: 'x' });
wf.dispatch({ type: 'complete', taskId: 'y' });
wf.isDone(); // true
```

节点类型：`task`（叶子工作单元）、`all`（并行全部，全部完成时汇合）、`branch`（条件选择子集，选中子集全部完成时汇合）。分支选项的节点可以是任意子树，因此分支可任意嵌套。

## 测试

需要 Node >= 22.6（依赖原生类型擦除直接运行 `.ts`）：

```sh
npm test
```

测试用事件排列（permutation）验证：任意完成顺序下每个汇合恰好触发一次、重复事件被忽略、嵌套分支按内到外汇合、取消后迟到成功只记证据。
