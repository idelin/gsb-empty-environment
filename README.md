# JMAP 请求授权包

供 TypeScript JMAP 服务的请求分发器调用的授权层，零运行时依赖，语义对齐
[RFC 8620](https://www.rfc-editor.org/rfc/rfc8620)（JMAP）。

## 授权规则

按 RFC 8620 §3 的请求处理模型实现，分发器只需注册方法表并把会话、`using`、
`methodCalls` 传入 `authorizeAndDispatch`：

1. **调用数限制（§3.4.1）**：调用数超过会话的 `maxCallsInRequest` 时，整个请求
   抛出请求级 `limit` 错误，不执行任何调用。
2. **using 能力（§3.3）**：`using` 中出现服务端不支持的能力，整个请求抛出请求级
   `unknownCapability`。
3. **逐调用隔离（§3.4.2）**：单个调用授权失败只内联返回该调用的 `error` 响应，
   后续调用继续执行，失败调用仍登记以便引用检查识别。
4. **账户与读写权限（§3.4.2 / §6.1）**：每个调用的 `accountId` 必须属于当前会话，
   且该账户持有方法所属能力；写方法还要求账户非只读，否则该调用返回 `forbidden`。
5. **方法能力声明**：方法所属能力未在 `using` 中声明时，该调用返回
   `unknownCapability`；未注册的方法名返回 `unknownMethod`。
6. **结果引用（§3.6 / §3.7）**：引用必须指向同请求内更早且成功的调用，且 `name`
   与方法名一致、`path` 为可解析的 JSON 指针（支持 `*` 通配），否则该调用返回
   `invalidResultReference`。引用解出的数据携带来源账户，按被调用方的 `accountId`
   重新授权；来源账户与被调用方账户不一致即跨账户访问，该调用返回 `forbidden`。

## 用法

```ts
import {
  authorizeAndDispatch,
  RequestError,
  type MethodDescriptor,
  type Session,
} from "./src/index.ts";

const session: Session = {
  accounts: new Map([
    ["acc-1", { capabilities: new Set(["urn:ietf:params:jmap:mail"]),
      isReadOnly: false }],
  ]),
  capabilities: new Set(["urn:ietf:params:jmap:mail"]),
  maxCallsInRequest: 16,
};

const methods = new Map<string, MethodDescriptor>([
  ["Email/query", {
    capability: "urn:ietf:params:jmap:mail",
    access: "read",
    handler: (args) => ({ accountId: args.accountId, ids: [], queryState: "0" }),
  }],
]);

try {
  const responses = authorizeAndDispatch({
    session,
    using: request.using,
    methodCalls: request.methodCalls,
    methods,
  });
  // responses 为方法响应三元组数组，逐调用 error 已内联其中。
} catch (error) {
  if (error instanceof RequestError) {
    // error.type: "limit" | "unknownCapability" —— 整个请求被拒绝。
  }
}
```

## 模块结构

- `src/types.ts`：会话、方法描述符、调用/响应三元组与错误类型。
- `src/references.ts`：结果引用识别、JSON 指针解析（RFC 6901 转义与 `*` 通配）、
  递归引用替换及来源账户追踪。
- `src/authorize.ts`：请求级限制检查与逐调用授权、引用重授权、分发与错误隔离。
- `src/index.ts`：公共导出。
- `test/authorize.test.ts`：基于 `node:test` 的 16 个用例。

## 测试

```sh
npm test
```

覆盖四类攻击/异常场景：混入他人账户（含只读写拒绝、账户缺能力）、未声明能力、
引用失败/不存在/方法名不符/坏路径的调用、以及同会话与他人账户的跨账户引用；
另含同账户引用正例、`*` 通配路径、调用数上限边界与逐调用错误隔离验证。
