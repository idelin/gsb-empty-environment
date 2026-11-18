# FedCM 请求校验包（TypeScript）

身份提供方（IdP）的 FedCM 端点处理器在读取 Cookie、返回账户或签发断言之前，应先调用本包。它把 FedCM 的请求来源约束封装成纯函数，不依赖具体 Web 框架。

## 安全规则

| 端点 | 校验 |
| --- | --- |
| `accounts` | 必须是 `GET`；必须且只能有 `Sec-Fetch-Dest: webidentity`；不得有 `Origin`、`Referer`；不得发生重定向 |
| `id_assertion` | 必须是 `POST`；`Origin` 必须属于正文 `client_id` 在 `client_metadata` 中登记的来源；`account_id` 必须属于 Cookie 对应会话的账户列表 |
| `client_metadata` | 不得带 `Cookie`（空值也算携带） |
| 断言签发 | `disclosure_text_shown` 缺失、`false` 或任何非 `true` 值时，断言不得包含 `email` / `email_verified` |

`Origin` 比较会规范化默认端口（如 `https://rp.example:443` 等价于 `https://rp.example`），但会拒绝 `null`、通配符、带路径或凭据的值。

## 使用

校验函数返回可判别的结果，不会把异常抛给正常拒绝路径：

```ts
import {
  createIdAssertionClaimsFromRequest,
  validateAccountsRequest,
  validateClientMetadataRequest,
  validateIdAssertionRequest,
} from "./index.ts";

function handleAccounts(request: Request) {
  const check = validateAccountsRequest({
    method: request.method,
    url: request.url,
    headers: request.headers,
    // 若反向代理跟随过重定向，适配器必须显式传入。
    redirected: false,
  });

  if (!check.ok) {
    return Response.json(
      { error: check.error.code },
      { status: check.error.status },
    );
  }

  return Response.json({ accounts: loadAccountsForCookie(request) });
}
```

`assertAccountsRequest` / `assertIdAssertionRequest` / `assertClientMetadataRequest` 是对应的抛异常版本，适合已有统一错误中间件的处理器。

### id_assertion

正文可以是已解析对象、`URLSearchParams`、`FormData`、JSON 字符串或表单字符串。会话应由处理器根据 Cookie 解析后传入，或传入一个只接收 Cookie 的查找函数：

```ts
async function handleIdAssertion(request: Request) {
  const body = new URLSearchParams(await request.text());

  const check = validateIdAssertionRequest(
    {
      method: request.method,
      url: request.url,
      headers: request.headers,
      body,
    },
    {
      clientMetadata: (clientId) => metadataStore.get(clientId),
      session: (cookie) => sessionStore.get(cookie),
    },
  );

  if (!check.ok) {
    return Response.json(
      { error: check.error.code },
      { status: check.error.status },
    );
  }

  const claims = createIdAssertionClaimsFromRequest(check.value);
  return Response.json({ token: signJwt(claims) });
}
```

如果 JWT 由其他模块构造，可在签名前调用：

```ts
import { assertNoUndisclosedEmail, sanitizeAssertionClaims } from "./index.ts";

const disclosure = body.get("disclosure_text_shown");
const safeClaims = sanitizeAssertionClaims(claims, disclosure);
assertNoUndisclosedEmail(safeClaims, disclosure);
```

### client_metadata

```ts
function handleClientMetadata(request: Request) {
  const check = validateClientMetadataRequest({
    method: request.method,
    url: request.url,
    headers: request.headers,
  });

  if (!check.ok) {
    return Response.json(
      { error: check.error.code },
      { status: check.error.status },
    );
  }

  return Response.json(metadataFor(request.url));
}
```

## 重定向

入站 `Request` 不暴露“代理是否跟随过重定向”。适配器应在发现重定向时传入 `redirected: true` 或 `redirectCount > 0`，`accounts` 校验会拒绝。处理器返回响应前可调用 `assertResponseNotRedirect(status)`，确保不会以 3xx 响应 FedCM 请求。

## API

- `validateAccountsRequest` / `assertAccountsRequest`
- `validateIdAssertionRequest` / `assertIdAssertionRequest`
- `validateClientMetadataRequest` / `assertClientMetadataRequest`
- `validateFedCMRequest` / `assertFedCMRequest`（按 `endpoint` 分发）
- `parseIdAssertionBody`
- `createIdAssertionClaimsFromRequest`
- `buildIdAssertionClaims` / `sanitizeAssertionClaims`
- `assertNoUndisclosedEmail` / `assertionContainsEmail`
- `normalizeDisclosureTextShown` / `disclosureAllowsEmail`
- `assertResponseNotRedirect` / `isRedirectStatus`

所有错误都带 `code` 与 `status`，可直接映射为响应。

## 测试

需要 Node.js >= 22.6（测试使用内置 TypeScript 类型擦除）。

```sh
npm test
```

测试覆盖页面直接 `fetch`（`Sec-Fetch-Dest: empty` + `Origin`/`Referer`）、缺失或错误 destination、重定向、错误 `Origin`、冒用其他依赖方的 `client_id`、不属于当前会话的 `account_id`、带 Cookie 的元数据请求，以及缺失/false 披露标记时断言不含邮箱。
