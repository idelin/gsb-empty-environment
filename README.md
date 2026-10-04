# 审批计划库 (approval-plan)

TypeScript 实现的审批计划引擎：前后依赖调度、或签/会签、运行中加签、计划修订。

## 核心概念

| 概念 | 说明 |
| --- | --- |
| 节点 (Node) | 一个审批环节，含审批人列表与签署模式 |
| 或签 (`mode: 'any'`) | 任一审批人通过即节点通过，其余待办自动清空 |
| 会签 (`mode: 'all'`) | 全体审批人通过才节点通过 |
| 前后依赖 (`dependsOn`) | 节点/或签组 id 列表，全部满足后节点才可执行 |
| 或签组 (`group`) | 多条分支共享组 id，任一分支通过即整组通过；其余**未开始**分支自动取消，组外依赖不受影响 |
| 逻辑时钟 (`seq`) | 每次状态变更递增，用于刻画并校验"既有执行顺序" |

## API 概览

```ts
import { ApprovalPlan } from './src/approval-plan.ts';

const plan = new ApprovalPlan({
  nodes: [
    { id: 'A', mode: 'all', approvers: ['u1', 'u2'] },          // 会签
    { id: 'B1', mode: 'any', approvers: ['u3'], dependsOn: ['A'], group: 'G' },
    { id: 'B2', mode: 'any', approvers: ['u4'], dependsOn: ['A'], group: 'G' }, // 或签组分支
    { id: 'C', mode: 'any', approvers: ['u5'], dependsOn: ['G'] }, // 依赖或签组
  ],
});

plan.getNextExecutable();        // 下一批可执行节点 → [A]
plan.approve('A', 'u1');
plan.approve('A', 'u2');         // A 通过 → [B1, B2] 可执行
plan.approve('B1', 'u3');        // 组 G 通过，未开始的 B2 自动取消
plan.getNextExecutable();        // → [C]，B2 的取消不影响外部依赖
```

### 运行中加签

- `insertAfter(anchorId, spec)`：在 anchor 后插入加签节点，原依赖 anchor 的未开始节点改挂到新节点之后；若后续节点已开始则拒绝并解释（会改变既有顺序）。
- `addApprover(nodeId, approverId)`：为未完成节点追加审批人（会签下新人也须通过）。

### 计划修订

`revise(revision)` 支持 `addNodes / removeNodes / addDependencies / removeDependencies`，
逐条应用并返回 `{ applied, rejected }` 报告：

- **已完成节点不可撤回**：`approved`/`rejected` 节点的移除与 `withdraw()` 均被拒绝；
- **新增依赖不得改变既有顺序**：若 `from` 已开始/完成，而新前置 `to` 在其启动前并未完成，拒绝并说明原因；成环依赖同样拒绝并给出解释。

## 运行测试

需要 Node ≥ 23（原生类型擦除，无需编译；Node 22.6+ 可加 `--experimental-strip-types`）：

```sh
node --test 'test/*.test.ts'
# 或已安装 typescript 时：tsc -p tsconfig.json 做类型检查
```
