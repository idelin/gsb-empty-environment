# Backup Rule Interpreter

TypeScript 实现的备份规则解释器：按优先级为每个文件选择 `include` / `exclude`
动作，支持静态冲突检查、三值（unknown 保留）语义、增量策略更新与全扫描参考核对。

## 运行环境

需要 Node.js ≥ 22.6（利用原生 TypeScript 类型擦除直接运行 `.ts`，无需编译）。
推荐 Node ≥ 23.6（默认启用）。无需安装任何依赖。

```sh
npm test          # 运行全部测试
npm run demo      # 运行端到端示例
npm run typecheck # 若本地装有 typescript，可做严格类型检查
```

## 规则模型

- 条件（`src/types.ts` 的 `Condition`）：路径 glob、大小区间（含边界）、
  修改版本区间、标签（可否定），以及 `not` / `all` / `any` 组合。
- 规则按 `priority` 降序评估，同优先级按 id 排序保证确定性。
- 条件求值为三值逻辑（`src/truth.ts`）：文件元数据缺失（如标签未拉取、
  大小未知）时条件为 `unknown`，**不会**被当作 false 而默认排除。

## 逐文件解释（`src/interpreter.ts`）

- 首个确定为 true 的最高优先级规则决定动作。
- 若更高优先级的规则求值为 `unknown`，文件决策保持 `unknown` 并记录
  `pendingRuleIds`，等待元数据补齐后重估。
- 所有规则均为 false 时才落到策略默认动作。

## 静态冲突检查（`src/policy.ts`）

不依赖任何文件，基于条件的约束摘要（`src/conditions.ts` 的
`summarizeCondition`）做保守分析，报告：

- `unsatisfiable-rule`：条件永假（如 `min > max`、同一标签既要求又禁止）。
- `ambiguous`：同优先级、条件可能重叠、动作不同。
- `shadowed` / `redundant`：低优先级规则被高优先级规则完全覆盖而不可达。
- `overlap`：跨优先级、动作不同的可能重叠（由优先级裁决，仅提示）。

## 增量更新（`src/incremental.ts`）

策略更新后：

1. `diffPolicies` 找出新增 / 删除 / 修改的规则与默认动作变化。
2. `selectAffectedFiles` 只挑出可能受影响的文件：旧决策依赖了变化的规则、
   或文件可能匹配变化规则的新旧条件（基于约束摘要的保守预过滤，元数据
   未知的文件一律保留重算）。
3. 仅对这些文件重新解释，输出计划：`downloads`（新增下载）、
   `revocations`（撤销）、`becameUnknown` / `resolved`。

## 全扫描参考核对（`src/reference.ts`）

`crossCheck` 用新策略对所有文件做全扫描，从旧快照独立推导计划，与增量
结果逐项比对。测试语料（`test/reference.test.ts`）专门覆盖标签缺失、
边界大小（`size == min/max`）和否定条件等边界情形。

## 项目结构

```
src/types.ts        领域类型与三值 Decision
src/truth.ts        Kleene 三值逻辑
src/glob.ts         glob 匹配与保守的包含/不相交关系
src/conditions.ts   条件求值、约束摘要、重叠/蕴含/预过滤
src/policy.ts       规则排序与静态冲突检查
src/interpreter.ts  逐文件解释与全扫描
src/incremental.ts  策略 diff、受影响文件选择、更新计划
src/reference.ts    全扫描参考与交叉核对
examples/demo.ts    端到端示例
test/               node:test 测试（29 个用例）
```
