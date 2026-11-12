# FedCM 身份提供方请求校验包

供 TypeScript 身份提供方（IdP）的 FedCM 端点处理器直接调用的零依赖校验库，
基于标准 Web Fetch API（`Request` / `Response` / `Headers`），可运行于 Node、Bun、Deno 与边缘运行时；
另附 `node:http` 适配器。

## 威胁模型与校验规则

| 端点 | 规则 | 失败原因码（HTTP 状态） |
| --- | --- | --- |
| `accounts` | 必须带 `Sec-Fetch-Dest: webidentity`，缺失或为其他值一律拒绝 | `not_webidentity` (403) |
| `accounts` | 不得带 `Origin` / `Referer`（页面 fetch 无法伪装成浏览器 FedCM 凭据请求） | `origin_forbidden` / `referer_forbidden` (403) |
| 所有端点 | 不得发生重定向；处理器返回 3xx 会被包装器拦截改写为 500 | `redirect_forbidden` (500) |
| `id_assertion` | 仅接受 POST | `method_not_allowed` (405) |
| `id_assertion` | `Origin` 必须是该 `client_id` 在 client_metadata 登记来源之一（origin 序列化后比较，默认端口归一化） | `origin_not_allowed` (403) |
| `id_assertion` | 正文 `account_id` 必须属于 Cookie 对应会话的账户列表，防止冒用他人账户 | `account_not_in_session` (403) |
| `id_assertion` | Cookie 无法解析出会话 | `session_not_found` (401) |
| `client_metadata` | 请求不得带 Cookie（公开静态配置端点，防会话化查询与缓存投毒） | `cookie_forbidden` (403) |
| 断言签发 | `disclosure_text_shown` 缺失或为 `false` 时，不得发放包含邮箱的断言 | `disclosure_required` (403) |

`Sec-Fetch-Dest`、`Origin` 等属于 Fetch 规范中的禁止头，页面脚本无法自行伪造，
因此 `Sec-Fetch-Dest: webidentity` 是区分“浏览器 FedCM 流程”与“攻击者页面普通 fetch”的核心防线。

## 目录结构

```
index.ts                    统一导出
src/
  errors.ts                 FedcmError 与全部原因码
  headers.ts                头部断言、Cookie 解析、JSON 响应工具
  accounts.ts               accounts 端点校验 + 重定向拦截
  client-metadata.ts        client_metadata 端点校验 + RP 登记表类型
  id-assertion.ts           id_assertion 端点校验（POST/Origin/账户归属）
  disclosure.ts             邮箱披露策略
  handler.ts                createFedcmHandler 包装器（错误映射 + 重定向拦截）
  http-adapter.ts           node:http <-> Web Request/Response 适配
test/                       node:test 测试与内存版演示 IdP
examples/server.ts          node:http 集成示例
```

## 用法

```ts
import {
  createFedcmHandler,
  validateAccountsRequest,
  validateIdAssertionRequest,
  assertEmailDisclosure,
  accountsResponse,
} from "./index.ts";

export const accountsHandler = createFedcmHandler((request: Request) => {
  const { sessionId } = validateAccountsRequest(request, "sid");
  const accounts = sessionId ? lookupAccounts(sessionId) : [];
  return accountsResponse(accounts);
});

export const idAssertionHandler = createFedcmHandler(async (request: Request) => {
  const validated = await validateIdAssertionRequest(request, {
    lookupClient: (clientId) => clientRegistry.get(clientId),
    resolveSession: (cookies) => sessionStore.get(cookies.sid),
  });

  const claims = { sub: validated.account.id, email: validated.account.email };
  assertEmailDisclosure(claims, validated.disclosureTextShown);
  return new Response(JSON.stringify({ token: sign(claims) }), {
    headers: { "content-type": "application/json" },
  });
});
```

若 IdP 希望“未披露时降级为无邮箱断言”而非直接报错，使用 `withEmailClaim(claims, email, shown)`，
它会在 `disclosure_text_shown !== true` 时自动剔除 `email` 字段。

### 错误处理

所有校验失败抛出 `FedcmError`（含 `reason` 与 `status`）；`createFedcmHandler`
统一捕获并输出 `{ "error": { "reason", "message" } }` JSON，未知异常收敛为 500 且不泄漏内部信息。

### node:http 集成

```ts
import { nodeHttpHandler } from "./index.ts";
http.createServer(nodeHttpHandler(accountsHandler)).listen(3000, "127.0.0.1");
```

## 测试

```
npm test
```

33 个用例覆盖任务要求的全部攻击路径：

- 页面直接 `fetch` 调 accounts（缺失/错误的 `Sec-Fetch-Dest`、带 `Origin`/`Referer`）；
- 错误来源与冒用其他依赖方来源调 id_assertion；
- 使用他人 `account_id`（不属于 Cookie 对应会话）；
- 带 Cookie 的 client_metadata 请求；
- 缺失/为 false 的 `disclosure_text_shown` 下申请含邮箱断言；
- 另含重定向拦截、非 POST、非 JSON、未知 client、默认端口归一化与 node:http 适配器用例。
