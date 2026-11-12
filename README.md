# RPA在弹窗里输入数据前，需要确认标签与输入框确实属于同一表单

# 表单绑定包（RPA 弹窗输入前校验）

RPA 在弹窗里输入数据前，必须确认标签与输入框确实属于同一表单。本包供 TypeScript
自动化宿主调用，零运行时依赖，输入可访问性树、显式 label 关系与可见矩形，输出
每个字段的绑定证据；当最优与次优指派的代价差不足阈值时拒绝执行。

## 入口

```ts
import { bindForms } from './index.ts';

const result = bindForms(nodes, explicitRelations, {
  weights: { distance: 1, wrongDirection: 100, roleMismatch: 50 },
  preferredDirections: ['right', 'below'],
  ambiguityThreshold: 5,
  formRoles: ['form', 'dialog'],
  includeHidden: false,
});

if (!result.accepted) {
  // 存在模糊或无法绑定的字段，禁止录入。
}
```

## 绑定流程

1. **建树与表单分组**：按 `parentId` 建树，沿树记录每个节点最近的 `form`/`dialog`
   祖先（`src/tree.ts`）。表单容器本身不作为候选控件。
2. **显式关联优先**：`for`、`labelledby` 等关系先消费控件；跨表单、隐藏或缺矩形的
   显式边会被拒绝并在证据中说明，标签回落到隐式匹配，控件不会被抢占。
3. **全局最小代价一对一匹配**：剩余标签与可用控件构造代价矩阵，用矩形匈牙利算法
   （`src/hungarian.ts`）求全局最优完整指派，保证一个控件只绑定一个标签。
4. **跨表单禁止**：表单祖先桶不同的边直接置为禁边（有限哨兵值，标记
   `forbidden: true`），表单外节点只能绑定同样无表单祖先的节点。
5. **模糊门限**：逐条封住最优边重跑求解，取严格更优的最小代价作为次优。
   - 全局门限：次优完整指派与最优的差 `globalMargin < threshold` 时整体拒绝；
   - 逐字段门限：单字段替换代价 `margin < threshold` 时该字段标 `ambiguous`；
   - 无替代边时字段保持 `bound`（没有竞争性候选，不算模糊）。

## 代价模型（权重全部由输入给定）

- 方向距离：基于可见矩形的边到边间距与中心偏移（`src/geometry.ts`），
  `cost += weights.distance * rawDistance`；
- 方向罚分：控件不在 `preferredDirections`（默认标签右侧或下方）时
  `cost += weights.wrongDirection`；
- 角色罚分：控件角色不在标签 `expectedControlRoles` 内时
  `cost += weights.roleMismatch`；没有期望角色的标签不罚分。

## 证据结构

每个 `FieldBinding` 都包含：`status`（`bound`/`ambiguous`/`unbound`）、
`source`（`explicit`/`implicit`）、命中控件、代价、次优代价与 margin、
按代价升序的全部候选边（含禁边原因、方向、角色罚分标记）以及人类可读 `reasons`。
顶层 `BindingResult` 还给出 `bestCost`、`secondBestCost`、`globalMargin`、
`threshold` 与 `diagnostics`，整体可 JSON 序列化供宿主审计。

## 验证

- TypeScript 测试（`tests/`，Node 内置 test runner）：
  - `enumerate.test.ts` 在小树上枚举所有一对一指派，断言匈牙利最优/次优与全枚举完全一致；
  - `scenarios.test.ts` 覆盖重复标签、隐藏控件、嵌套弹窗、跨表单、显式优先、
    角色罚分与调用方权重，并校验证据完整性。
  - 运行：`npm test`（需 Node >= 23.9，启用 `--experimental-strip-types`）。
- 枚举校验器（`tools/enumerate/`，Go）：同算法移植与全枚举实现，在当前沙箱
  Node 动态库不可用时提供可执行验证，覆盖与 TS 测试等价的夹具。
  - 运行：`npm run verify:enumeration` 或 `cd tools/enumerate && go run .`。

## 目录

- `src/types.ts` 公共类型与默认权重
- `src/geometry.ts` 可见矩形几何与方向判定
- `src/tree.ts` 可访问性树索引与表单祖先
- `src/cost.ts` 候选边代价与禁边规则
- `src/hungarian.ts` 矩形最小代价一对一指派
- `src/enumerate.ts` 小树全匹配枚举（验证用）
- `src/bind.ts` 两阶段绑定主流程与门限
- `examples/usage.ts` 宿主调用示例
