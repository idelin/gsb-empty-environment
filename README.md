# 授权响应iss参数混淆防护

客户端对接多个授权服务器时，攻击者让用户在恶意服务器上发起授权，再把得到的
授权码交给诚实服务器的令牌端点（混淆攻击 / mix-up attack）。本包实现
[RFC 9207](https://www.rfc-editor.org/rfc/rfc9207) 的授权响应 `iss` 校验，
供 TypeScript 回调路由直接调用，零运行时依赖。

## 安全规则

- 发起授权请求时，把所用授权服务器的 `issuer` 与元数据
  `authorization_response_iss_parameter_supported` 写入会话
  （`rememberAuthorizationServer`）。
- 回调进入后、使用 `code` 之前完成全部 `iss` 校验
  （`verifyAuthorizationCallback` / `handleAuthorizationCallback`）。
- 回调 `iss` 缺失且服务器声明支持该参数（值为 `true`）时拒绝；
  未声明支持时按 RFC 向后兼容放行。
- `iss` 存在时必须与会话记录**逐字节相等**：不做任何 URL 规范化，
  尾部斜杠、大小写差异均视为不匹配。
- `iss` 在查询串中出现多次（即使值相同）一律拒绝。
- 校验失败抛 `IssuerValidationError`，调用方必须立即结束请求；
  通过依赖注入的令牌交换函数在任何失败路径上都不会被调用。
- 错误响应（带 `error` 参数，RFC 9207 §5.2）走同一套 `iss` 校验，
  通过后原样返回错误，同样不触发令牌交换。

## 用法

```ts
import {
  rememberAuthorizationServer,
  IssuerValidationError,
} from './src/iss-validation.ts';
import { handleAuthorizationCallback } from './src/callback.ts';

// 1) 发起授权请求前：会话记录本次授权服务器
rememberAuthorizationServer(req.session, {
  issuer: serverMetadata.issuer,
  authorizationResponseIssParameterSupported:
    serverMetadata.authorization_response_iss_parameter_supported,
});

// 2) 回调路由：先校验 iss，exchange 只在校验通过后执行
try {
  const outcome = await handleAuthorizationCallback(
    req.url,          // 完整 URL / 相对路径 / URL / URLSearchParams
    req.session,      // 含 oauthAuthorizationServer 字段的会话
    async ({ code, record }) => {
      return fetch(`${record.issuer}/token`, { /* 用 code 换令牌 */ });
    },
  );
  // outcome.kind === 'error_response' | 'token'
} catch (err) {
  if (err instanceof IssuerValidationError) {
    // err.code: missing_session | missing_iss | duplicate_iss | iss_mismatch
    res.status(400).send(`invalid authorization response: ${err.code}`);
  }
}
```

完整的 express 风格示例见 `example/callback-route.ts`。

## API

- `rememberAuthorizationServer(session, record)`：授权请求阶段写会话。
- `verifyAuthorizationCallback(callback, session)`：纯校验，返回
  `{ params, isErrorResponse, record }`，不触碰令牌端点。
- `verifyResponseIssuer(params, record)`：RFC 9207 §4.2/§5.2 的核心规则，
  成功响应与错误响应共用。
- `handleAuthorizationCallback(callback, session, exchange)`：路由级封装，
  保证“先校验、后交换”的顺序；错误响应不调用 `exchange`。
- `IssuerValidationError`：统一错误类型，`code` 标识失败原因。

## 测试

```sh
npm test
```

使用 Node 内置测试运行器（`node --test`，type-stripping 直接运行 TS）。
覆盖：`iss` 缺失但已声明支持、`iss` 来自不同服务器、尾部斜杠 / 大小写
差异、重复 `iss`、无会话记录、成功路径只交换一次，以及错误响应的
`iss` 校验；所有失败用例都断言令牌交换函数从未被调用。
