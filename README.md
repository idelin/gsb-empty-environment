# 方法覆盖头的网关方法判定

网关按 HTTP 方法授权时，如果只信任传输方法，攻击者可以用
`X-HTTP-Method-Override: DELETE` 或表单 `_method=DELETE` 让后端执行删除，
而网关仍把请求当作 `POST` 放行。本模块提供嵌入 TypeScript 网关的可导入
方法判定组件。

## 安全语义

- 方法覆盖默认关闭，只有 `enabled: true` 且来源受信时才生效。
- 受信来源可以是显式 `trustedOrigins`、同源请求，或网关提供的
  `isTrustedSource`/`trusted` 信号；`Sec-Fetch-Site: cross-site` 永远不能
  继承同源信任。
- 覆盖生效时，原始方法和覆盖后的方法都进入授权；任一 `deny` 或
  `challenge` 都覆盖 `allow`，且不会短路另一个方法的检查。
- 请求头与表单字段声明了不同方法时直接拒绝；多个覆盖头互相冲突同样拒绝。
- CSRF、审计主方法和限流桶始终使用原始传输方法，覆盖头不能改变它们。
- 返回值中的 `forwardHeaders`/`forwardBody` 已移除覆盖声明。即使覆盖被
  忽略，后端也不会再次解释该声明。
- 对无法生效的覆盖，默认忽略并剥离；配置
  `unhonoredOverridePolicy: "reject"` 可改为直接拒绝。

## 用法

```ts
import { createMethodOverrideGuard } from "./index.ts";

const guard = createMethodOverrideGuard({
  enabled: true,
  trustedOrigins: ["https://app.example.com"],
  allowSameOrigin: false,
});

const decision = await guard(request, async ({ method, csrfMethod }) => {
  // method 可能是 POST 和 DELETE，两者都要通过同一条授权策略。
  // csrfMethod 永远是原始 POST，不能使用覆盖后的方法。
  return policy.allow(method, request, csrfMethod);
});

if (!decision.allowed) {
  return reject(decision.outcome, decision.reasonCode);
}

const effectiveMethod = decision.effectiveMethod;
const headers = decision.forwardHeaders;
const body = decision.forwardBody;
```

同步解析（不执行授权）：

```ts
import { resolveRequestMethod } from "./index.ts";

const resolution = resolveRequestMethod(request, {
  enabled: true,
  trustedOrigins: ["https://app.example.com"],
});

// resolution.valid === false 时必须直接拒绝；
// resolution.authorizationMethods 是需要全部授权的方法。
```

## 验证

```sh
npm test
```

测试覆盖：默认关闭、覆盖为 `DELETE`、双重冲突覆盖、表单 `_method`、
跨源请求、同源合法覆盖、头体冲突、更严格授权结果、CSRF/审计/限流记录
不变，以及异步授权与异常失败关闭。
