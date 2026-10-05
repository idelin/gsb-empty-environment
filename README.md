# approval-plan

审批计划库（TypeScript）：前后依赖编排、或签 / 会签、运行中加签、计划修订与下一批可执行节点输出。

## 核心概念

- **节点（NodeDef）**：`id`、`label`、`mode`（`any` 或签 / `all` 会签）、`assignees`（审批人）、`deps`（前置依赖）。
- **计划（ApprovalPlan）**：节点构成的 DAG，构造前用 `ApprovalPlan.validate` 校验重复定义、缺失依赖与环路。
- **运行时（ApprovalEngine）**：驱动状态机 `pending -> ready -> approved / rejected / cancelled`。
- **修订（revise）**：对运行中的计划做原子化变更，非法操作整体拒绝并逐条给出中文解释。

## 语义规则

- **前后依赖**：前置节点全部 `approved` 后，后继节点才变为 `ready`；`nextBatch()` 输出当前可执行节点。
- **或签（any）**：任一审批人同意即通过，其余未签审批人的任务自动取消（记入 `cancelledAssignees`）；所有人拒绝才否决。
- **会签（all）**：全员同意才通过；任一拒绝即否决。
- **加签**：`addAssignees` 或修订操作 `addAssignees`，仅允许未结束的节点；会签追加的人必须签署，或签新审批人可直接通过。
- **取消分支**：`cancelNode` 仅允许无签署记录的未开始节点；或签通过后取消未开始分支，不影响外部依赖（见测试「或签…不影响外部依赖」）。
- **计划修订**：
  - 已完成（approved / rejected）节点不可撤回、不可移除、不可加签；
  - 新增依赖若形成环路，或目标节点已完成 / 已开始签署（改变既有执行顺序），拒绝并解释；
  - 整批操作原子生效：任一非法则全部不应用。

## 使用

```bash
npm test     # 构建（类型擦除）并运行测试
npm run demo # 运行示例
```

```ts
import { ApprovalEngine, revise } from './src/index.js';

const engine = ApprovalEngine.fromDefs([
  { id: 'a', label: '申请', mode: 'any', assignees: ['u1'], deps: [] },
  { id: 'b', label: '审批', mode: 'all', assignees: ['u2', 'u3'], deps: ['a'] },
]);
engine.nextBatch();            // ['a']
engine.sign('a', 'u1', 'approve');
engine.nextBatch();            // ['b']
```

## 构建说明

源码在 `src/`（TypeScript）。本环境无网络无法安装 `tsc`，仓库内置零依赖的类型擦除器
`scripts/strip-types.mjs`（`npm run build` 输出到 `build/`）。有网络的环境可直接
`npm i -D typescript && npx tsc -p tsconfig.json` 使用官方编译器。
