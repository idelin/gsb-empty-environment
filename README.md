# 一次性登录链接的预取抵抗与浏览器绑定

这是一个无第三方依赖、可直接导入 TypeScript 认证服务的登录链接兑换包。默认使用进程内存储，适合测试或单实例服务；多实例部署时应把 `linksByTokenHash`、签发限流和会话存储替换为共享的原子存储。

## 安全不变量

- 设备绑定值由 16 字节 CSPRNG 生成，写入 `__Host-device_binding` Cookie，并带 `HttpOnly`、`Secure`、`SameSite=Lax`。
- 登录令牌由 32 字节 CSPRNG 生成；设备值哈希和令牌哈希使用 SHA-256，服务端记录不保存原始值。
- GET 只渲染确认页，不消耗令牌、不设置 Cookie、不清理记录或改变其他状态。
- POST 只有在令牌哈希与设备哈希匹配同一条未过期记录时才成功；设备缺失或不匹配时直接返回验证码兜底提示，记录保持不变。
- 兑换成功后立即删除令牌记录，同一令牌不能重放；过期时间为签发后 15 分钟。
- 同一规范化邮箱在滚动 10 分钟窗口内最多成功签发 5 个链接；第 6 个返回 `rate_limited`，不会使第 5 个失效。
- 每次成功签发新链接都会删除该邮箱此前的有效链接。
- 兑换成功时生成新的会话标识，并撤销调用方传入的旧会话标识。

## 使用

```ts
import { createLoginLinkService } from "./index.ts";

const loginLinks = createLoginLinkService();

const issued = await loginLinks.issueLoginLink("user@example.com");
if (!issued.ok) {
  throw new Error(issued.message);
}

response.setHeader("Set-Cookie", issued.cookie);
sendEmail(`https://example.com/login/verify?token=${issued.token}`);
```

GET 路由只调用确认页：

```ts
app.get("/login/verify", (request, response) => {
  const result = loginLinks.handleGet(request.query.token);
  response.status(result.status).set(result.headers).send(result.body);
});
```

POST 路由从表单读取 `token`，从 `Cookie` 请求头读取设备绑定值，并传入当前会话标识：

```ts
app.post("/login/verify", async (request, response) => {
  const result = await loginLinks.handlePost({
    token: request.body.token,
    cookieHeader: request.headers.cookie,
    currentSessionId: request.cookies.session,
  });
  response.status(result.status).set(result.headers).send(result.body);
});
```

成功响应的 `sessionId` 是新的会话标识，`Set-Cookie` 已包含新的 `__Host-session`。认证服务也可以直接调用底层方法：

```ts
const redeemed = await loginLinks.redeem(
  token,
  deviceBinding,
  currentSessionId,
);
```

`redeem` 的失败结果始终是 `{ ok: false, consumed: false }`。设备失败返回 `code: "device_binding_required"` 和 `fallback: "email_code"`；无效、过期或已使用的令牌返回 `code: "invalid_or_expired_token"`。

## 测试

```sh
npm test
```

测试覆盖扫描器 GET 预取、攻击者链接被受害者浏览器打开、缺失设备绑定、令牌重放、第 6 个令牌、旧链接失效、15 分钟过期、并发兑换与会话轮换。
