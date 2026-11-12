# JMAP方法调用账户授权与引用

JMAP 把多个方法调用放进同一个请求，攻击者在其中混入对他人 accountId 的调用，或用结果引用把一个调用的输出接进另一个账户的调用。给TypeScript JMAP 服务写供请求分发器调用的授权包，按 RFC 8620：每个调用的 accountId 必须属于当前会话且持有该方法所需的读或写权限，否则只让该调用返回 forbidden 错误并继续后续调用；方法所属能力须在 using 中声明，未声明返回 unknownCapability；调用数超过 maxCallsInRequest 整个请求以 limit 错误拒绝；结果引用须指向同请求内更早且成功的调用，引用解出的 id 再按被调用方的 accountId 重新授权。用混入他人账户、未声明能力、引用失败调用和跨账户引用验证。

## 实现

零依赖 TypeScript ESM 包，入口 `src/index.ts`（根 `index.ts` 转出）：

- `src/types.ts`：会话、方法描述符、授权决策等类型。
- `src/json-pointer.ts`：RFC 6901 JSON Pointer，解析结果引用的 `path`。
- `src/result-reference.ts`：RFC 8620 §3.7 结果引用识别与深拷贝替换。
- `src/authorizer.ts`：请求级预检 `gateRequest` 与逐调用 `RequestAuthorizer`。
- `src/dispatcher.ts`：分发器可直接调用的 `dispatchAuthorized` 助手。

## 授权规则（RFC 8620）

请求级（失败则整请求拒绝，不执行任何调用）：

- 结构非法（非对象、缺 `using`/`methodCalls`）返回 `notRequest`。
- `using` 含会话不支持的能力返回 `unknownCapability`（§3.3）。
- `methodCalls` 数量超过 `maxCallsInRequest`（取 core 能力声明，缺省 16）返回 `limit`（§3.6.1）。

调用级（失败只让该调用返回 `error` 响应，后续调用继续）：

- 方法所属能力未在 `using` 声明：`unknownCapability`（§5.1）。
- `accountId` 不在会话 `accounts` 中、账户不具备该能力、只读账户上执行写方法：`forbidden`（§6.1）。
- 结果引用未指向同请求内更早且成功的调用、`name` 不匹配或 `path` 无法解析：`invalidResultReference`（§3.7）。
- 引用解出的实体 id 通过注入的 `IdAccessChecker` 按被调用方 `accountId` 与读/写级别重新授权；任何 id 不属于该账户即 `forbidden`，阻断跨账户引用。

## 分发器用法

```ts
import { dispatchAuthorized } from "./index.ts";

const outcome = await dispatchAuthorized(
  request,
  {
    session,
    methods: {
      "Email/query": {
        capability: "urn:ietf:params:jmap:mail",
        kind: "read",
        idBindings: [
          { path: "filter.inMailbox", entityType: "Mailbox" },
        ],
      },
    },
    idAccessChecker: async ({ accountId, entityType, entityIds }) =>
      entityIds.filter((id) => belongsTo(accountId, entityType, id)),
  },
  async ({ methodName, args }) => executeMethod(methodName, args),
);

if (outcome.rejected) {
  // 整个请求级错误：outcome.error.type 为 limit/notRequest/unknownCapability
} else {
  // outcome.methodResponses：被拒调用为 ["error", { type, detail }, clientId]
}
```

需要自行控制执行循环时，可改用 `prepareAuthorizedRequest` +
`RequestAuthorizer#authorize` / `recordSuccess`。

## 测试

`npm test`（node:test，18 个用例），覆盖混入他人账户、未声明能力、
引用失败/不存在调用、跨账户结果引用、只读账户、调用上限与 JSON Pointer。
