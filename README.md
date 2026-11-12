# 嵌套 iframe 操作计划包

面向 TypeScript 浏览器自动化宿主的同步操作计划层。计划在捕获时固定顶层文档代次、每层 iframe 元素实例、每层 frame 实例和文档代次；执行时只用这些固定句柄复核，不会按名称重新解析 frame。

## 核心约束

- `OperationPlan` 绑定 `topDocumentGeneration`、每层 `iframeElementId` / `frameId` / `childDocumentGeneration`，以及目标元素实例。
- 坐标逐层变换：iframe 的 border 与 padding 偏移、父文档滚动、iframe CSS 仿射变换（含 transform-origin），再经过每一层向上复合。
- 执行前逐层复核：文档仍存活且代次一致、iframe 元素仍是同一实例、frame 仍是同一实例、目标仍在同一文档，并且点击点位于该层 viewport、frame content quad 和可见裁剪区内。
- 任一层或目标身份变化时抛出 `OperationPlanRejectedError`，整步拒绝。若 iframe 被同名新元素替换，旧计划仍绑定旧元素句柄，不会转向新 frame。
- `executeOperationPlan` 在 dispatch 前生成状态指纹；宿主必须在点击副作用前调用 `ExecutionGuard.assertUnchanged()`。若期间发生导航、滚动或几何变化，同样整步拒绝并保持零点击。

## 使用方式

```ts
import {
  SimulatedBrowser,
  captureOperationPlan,
  executeOperationPlan,
} from "./index.ts";

const browser = new SimulatedBrowser({ width: 1200, height: 900 });
// 先按业务结构创建 outer / inner iframe 与目标元素。

const captured = captureOperationPlan(browser, {
  framePath: ["outer", "inner"], // 仅在捕获阶段按名称解析
  targetElementId: "target",
});

const receipt = executeOperationPlan(browser, captured.plan);
console.log(receipt.clickedAt); // 顶层文档 viewport 坐标
```

宿主通过 `AutomationHost` 接口接入真实浏览器：提供文档快照、按名称查找（仅捕获阶段）、iframe / 元素快照和 `dispatchClick`。`dispatchClick` 必须在产生点击副作用前同步调用 `request.guard.assertUnchanged()`。

## 坐标模型

iframe 内容点 `p` 到父文档 layout 坐标的变换为：

```text
T(rect.x, rect.y)
  ∘ T(origin)
  ∘ M
  ∘ T(-origin)
  ∘ T(border.left + padding.left, border.top + padding.top)
```

随后减去父文档的 `scrollX` / `scrollY` 得到父 viewport 坐标。目标元素使用相同形式的局部到文档仿射变换，再逐层向内到外复合。

## 测试

```bash
node --experimental-strip-types --test
```

测试覆盖：

- 两层 iframe 同时存在 border、padding、缩放、跨层滚动时的最终点击坐标；
- 90 度仿射旋转与 transform-origin；
- 顶层导航、内层文档导航、同名 iframe 替换、目标元素替换的整步拒绝；
- 目标滚出 viewport、逐层可见裁剪区变化时零点击；
- preflight 与 dispatch 之间发生导航或滚动时的执行竞争，零点击。
