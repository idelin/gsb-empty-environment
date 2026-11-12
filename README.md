# 浏览器表单目标污染：固定提交计划包

安全表单构造器允许相对 `action`，但页面中的 `<base>` 变化、按钮级覆盖或导航竞争
都不能改变**已批准**的提交目标。本包是零依赖的 TypeScript ESM 库，由宿主在
浏览器/嵌入层中调用。

## 安全模型

- **批准时固定（`createSubmitPlan`）**：以捕获时的 `baseUrl` 解析相对动作，冻结
  来源（origin）、动作绝对 URL、方法（GET/POST）与编码类型；返回深度冻结的
  `SubmitPlan`，之后修改文档属性或 `<base>` 都无法影响该计划。
- **统一决策入口**：普通提交与提交按钮的 `formaction` / `formmethod` /
  `formenctype` 覆盖都只能通过 `createSubmitPlan` 解析；覆盖值与表单属性接受
  同一套方法/编码白名单与目标许可策略检查，不存在绕过路径。每次按钮点击产生新
  计划 ID，不能复用旧计划。
- **提交时复核（`submitPlan` / `verifyPlan`）**：
  - 文档代（`documentGeneration`，导航时递增）必须与批准时一致，否则报
    `STALE_GENERATION`；
  - 实时文档来源必须仍等于批准来源，否则报 `ORIGIN_MISMATCH`；
  - 目标许可策略再次求值，权限在批准后被撤销则报 `PERMISSION_REVOKED`；
  - 传输层回报的实际请求 URL 必须与冻结目标一致，重定向/竞争导致偏移报
    `TARGET_DRIFT`，且不允许传输层自行重解析相对地址。
- 默认策略为同源提交，可通过 `TargetPolicy`（如 `OriginAllowlistPolicy`）替换。

## 模块

- `index.ts`：公共出口。
- `src/types.ts`：类型与 `SubmitError` 错误码。
- `src/url.ts`：http(s) 来源校验、基于捕获 base 的动作解析、表单编码工具。
- `src/snapshot.ts`：方法/编码规范化，按钮覆盖合并（仅在此处合并）。
- `src/plan.ts`：审批入口，生成深度冻结的提交计划。
- `src/request.ts`：按冻结目标构造 GET 查询串、urlencoded、multipart、text/plain 请求。
- `src/submit.ts`：提交时文档代/来源/许可复核与传输漂移检测。
- `src/dom.ts`：供浏览器宿主使用的快照采集、按钮覆盖读取与来源白名单策略。
- `test/`：`node:test` 测试，覆盖 base 变化、多提交按钮与导航竞争下的实际请求目标。

## 用法

```ts
import {
  captureSnapshot,
  createSubmitPlan,
  readButtonOverride,
  submitPlan,
} from "./index.ts";

const plan = createSubmitPlan(captureSnapshot(doc, form), null, {
  policy, // 可选，默认同源
});

// 按钮点击：覆盖项重新进入同一审批决策
const buttonPlan = createSubmitPlan(
  captureSnapshot(doc, form),
  readButtonOverride(clickedButton),
  { policy },
);

await submitPlan(
  buttonPlan,
  () => ({ generation: doc.generation, origin: doc.location.origin, baseUrl: doc.baseURI }),
  transport, // 宿主提供，必须原样发送 PreparedRequest 并回报实际 URL
);
```

## 测试

需要 Node 22.6+（类型剥离）：

```sh
npm test
```
