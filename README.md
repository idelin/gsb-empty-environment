# 安全登录跳转组件

登录成功后的 `next` 是不可信输入。该组件在解析前拒绝危险原文，在解析后
按 RFC 3986 规范化地址，并由唯一构造器生成一个 `Location` 头；其余响应头
在发送前统一校验名称和值语法。

## 使用

```ts
import { createRedirectResponse, sendRedirect } from "./index.ts";

const policy = {
  baseUrl: "https://login.example.com/",
  allowedOrigins: ["https://account.example.com"],
  fallback: "/",
  status: 302 as const,
};

const redirect = createRedirectResponse("/account/settings", policy);
// {
//   status: 302,
//   headers: { Location: "/account/settings", "Content-Length": "0" },
//   body: ""
// }

sendRedirect(nodeResponse, redirect);
```

也可以使用完整的请求对象：

```ts
createRedirectResponse({
  next: requestUrl.searchParams.get("next"),
  baseUrl: "https://login.example.com/",
  allowlist: ["https://account.example.com"],
  headers: { "Cache-Control": "no-store" },
});
```

Node 服务可直接挂载登录处理器：

```ts
import http from "node:http";
import { createLoginRedirectHandler } from "./index.ts";

const handler = createLoginRedirectHandler({
  baseUrl: "https://login.example.com/",
  allowlist: ["https://account.example.com"],
});

http.createServer((request, response) => handler(request, response)).listen(8080);
```

`allowedOrigins` 和 `allowedHosts` 按来源放行整个站点的绝对地址；
`allowlist` 中的条目如果带有路径，则只放行该路径及其子路径。本站同源绝对
地址始终允许，其他绝对地址必须命中白名单。

## 安全规则

- 相对目标必须是本站路径；`//host`、`\\host` 及编码后的变体一律拒绝。
- 绝对目标只允许 `http`/`https`，且必须同源或在白名单内；拒绝凭据、通配符
  和协议降级/跨主机地址。
- 原始输入出现 C0/C1 控制字符、Unicode 行/段分隔符、反斜杠、裸 `%`、
  编码反斜杠、无效 UTF-8 或多层百分号编码时拒绝。
- 解码后再次检查 CRLF、控制字符、反斜杠、协议相对地址（包括
  `%2f%2fhost`）和第二层 `%XX`。
- 解析后统一小写 scheme/host、去掉默认端口、解码 unreserved 字符、移除
  dot-segment，并重新编码非法字符；规范化结果不得以 `//` 开头。
- 非法目标回落到已验证的 `fallback`（默认 `/`）。需要抛出错误时使用
  `assertSafeRedirectTarget()` / `validateRedirectTarget()`。
- `createRedirectResponse()` 是唯一写入 `Location` 的 API。调用方再次提供
  `Location`（包括大小写不同的重复项）会抛出 `DUPLICATE_LOCATION`。
- `sendRedirect()` 发送前重新校验全部头，并移除响应对象上已有的
  `Location`，确保一次请求只有一个。
- 重定向状态只允许 301/302/303/307/308；`Content-Length` 始终由实际
  响应体计算，不匹配时拒绝发送；同时拒绝 `Transfer-Encoding`，避免
  Content-Length 与分块编码并存造成响应边界歧义。

## API

- `createRedirectResponse(next, options)` / `createRedirectResponse(request)`
- `buildRedirectLocation(next, options)`
- `assertSafeRedirectTarget(next, options)`
- `validateHeaderName(name)` / `validateHeaderValue(name, value)`
- `validateHeaders(headers)`
- `validateRedirectResponse(response)`
- `sendRedirect(response, redirect)`
- `createLoginRedirectHandler(options)`
- `RedirectError`，带稳定错误码（如 `INVALID_TARGET`、`DUPLICATE_LOCATION`）

`next` 缺失或非法时，构造 API 返回安全回退地址；`assertSafeRedirectTarget`
则抛出 `RedirectError`。

## 测试

```bash
npm test
```

测试覆盖 `%0d%0a` 注入、编码/双编码 CRLF、Unicode 换行、协议相对地址、
反斜杠、裸百分号、白名单外主机、同源合法跳转、重复 `Location`、头名称/
值注入以及状态码与响应体一致性。
