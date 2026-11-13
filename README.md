# 一键退订校验包（RFC 8058）

零依赖、可直接导入的 TypeScript 包，用于生成和安全校验一键退订链接，并在构造出站邮件前检查 DKIM 头覆盖范围。

## 安全模型

- 只有 `POST` 会调用退订存储；`GET` 仅渲染带确认按钮的 HTML 页面，不改变任何订阅状态，因此邮件安全扫描器预取链接不会替用户退订。
- `POST` 的 `Content-Type` 必须为 `application/x-www-form-urlencoded`，正文必须逐字节等于 `List-Unsubscribe=One-Click`。
- 令牌为 `HMAC-SHA256(secret, recipient || 0x00 || listId || 0x00 || expiresText)` 的小写十六进制结果，`expiresText` 是 URL 中原始的无符号十进制秒数。
- 路径列表 ID、收件人和过期时间都参与签名；任意一项被替换、过期、或令牌长度/编码不合法都会被拒绝。
- 令牌比较使用 `crypto.timingSafeEqual`。收件人、列表 ID 和过期文本均禁止 NUL，避免分隔符歧义。
- 构造出站邮件时，DKIM 签名头集合必须以大小写不敏感方式同时包含 `List-Unsubscribe` 和 `List-Unsubscribe-Post`，否则抛出 `DkimHeaderCoverageError`。

## 快速使用

```ts
import {
  createOneClickUnsubscribeHandler,
  createOneClickUnsubscribeUrl,
  buildOutboundEmail,
} from "./index.ts";

const secret = process.env.UNSUBSCRIBE_HMAC_SECRET!;
const expiresAt = Math.floor(Date.now() / 1000) + 60 * 60 * 24;

const unsubscribeUrl = createOneClickUnsubscribeUrl({
  baseUrl: "https://example.com",
  secret,
  recipient: "alice@example.com",
  listId: "product-updates",
  expiresAt,
});

const email = buildOutboundEmail({
  from: "news@example.com",
  to: "alice@example.com",
  subject: "Newsletter",
  headers: {
    "List-Unsubscribe": `<${unsubscribeUrl}>`,
    "List-Unsubscribe-Post": "List-Unsubscribe=One-Click",
  },
  dkim: {
    signedHeaders: [
      "From",
      "To",
      "Subject",
      "List-Unsubscribe",
      "List-Unsubscribe-Post",
    ],
  },
});
```

处理器接收标准 `Request`，返回标准 `Response`，可直接接入 Fetch、Hono、Cloudflare Workers 等运行时：

```ts
const handleUnsubscribe = createOneClickUnsubscribeHandler({
  secret,
  now: () => Math.floor(Date.now() / 1000),
  onUnsubscribe: async ({ listId, recipient }) => {
    await database.unsubscribe(listId, recipient);
  },
});

// app.all("/unsubscribe/:listId", ({ request }) => handleUnsubscribe(request))
```

## API

- `createUnsubscribeToken({ secret, recipient, listId, expiresAt })`
- `verifyUnsubscribeToken({ secret, recipient, listId, expiresAt, token, now? })`
- `inspectUnsubscribeToken(options)`：返回 `{ ok, code, reason, ... }`
- `createOneClickUnsubscribeUrl({ baseUrl, secret, recipient, listId, expiresAt })`
- `createOneClickUnsubscribeHandler({ secret, onUnsubscribe | unsubscribe | store, now?, pathPrefix? })`
- `assertDkimCoversUnsubscribeHeaders(emailOrSignedHeaders, signedHeaders?)`
- `buildOutboundEmail(email, signedHeaders?)`：校验失败时抛出 `DkimHeaderCoverageError`

`expiresAt` 使用 Unix 秒，也接受十进制文本或 `Date`。过期判断为 `now >= expiresAt`。

## 测试

```sh
npm test
```

测试覆盖 GET 预取无副作用、精确 POST 正文、错误 Content-Type、令牌换列表、过期、收件人篡改，以及 DKIM 未覆盖两个退订头时拒绝构造邮件。
