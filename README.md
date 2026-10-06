# workflow-join-state

TypeScript 工作流状态库：条件分支 + 汇合（join），事件驱动、可嵌套。

## 语义

- **条件启动**：分支激活时一次性评估各子分支条件，仅启动条件满足的子任务；
  未选中的分支永远保持 `pending`，汇合不会等待它们。
- **冻结集合**：选中集合在分支选择时冻结（`Object.freeze`），之后上下文变化不影响。
- **汇合一次**：汇合只等待实际启动的集合全部到达终态（`completed` / `cancelled`）；
  汇合后分支进入终态，因此 `branch.joined` 对每个分支恰好触发一次。
- **重复忽略**：重复的完成事件被忽略，不产生任何状态变化。
- **取消优先**：取消是终态；取消后迟到的成功只记入 `evidence`（`late-success`），
  不改变状态、不推进流程。取消不覆盖已完成的节点。
- **嵌套分支**：内层分支作为外层分支的子节点参与外层汇合，外层汇合必晚于内层汇合。

## API

```ts
import { Workflow, branch, task, when } from './src/workflow';

const wf = new Workflow(
  branch('root', [
    when(task('a')),                          // 无条件，总是选中
    when(task('b'), (ctx) => ctx.useB),       // 条件选中
    when(branch('inner', [when(task('p'))])), // 嵌套分支
  ]),
  { useB: true },
);

wf.dispatch({ type: 'task.completed', taskId: 'a' });
wf.dispatch({ type: 'cancel', nodeId: 'b' });

wf.emitted;   // 全部已发出事件（task.started / branch.selected / branch.joined / ...）
wf.evidence;  // 迟到成功证据
wf.status;    // 根节点状态
```

## 测试

```sh
bun test
```

测试包含事件全排列验证：3 任务完成的 6 种排列、含重复完成的多重集排列、
取消与迟到成功交错的排列，以及嵌套分支下每层汇合恰好一次且内层先于外层。
