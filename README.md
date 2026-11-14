# 子域设置的Cookie覆盖了父域会话

企业站把会话放在父域Cookie，攻击者控制的`evil.example.com`子域可设置`Domain=.example.com`同名Cookie，令父域请求带上攻击者的会话或顶掉用户的登录态。请实现嵌入TypeScript Web框架的可导入Cookie签发与接收组件。每个Cookie携带名称、值、Domain、Path、Secure、HttpOnly、SameSite、有效期和签发主体；接收时按前缀规则核对：`__Host-`必须无Domain且Path为`/`并仅Secure，`__Secure-`必须Secure，带Domain的Cookie只能由该域自身或更具体子域签发。同名多Cookie按路径长度与创建时间决定唯一有效值，拒绝无法归属的域Cookie；会话标识轮换后旧值立即失效。用子域覆盖父域、前缀缺失、路径兄弟重叠、端口与主机大小写、合法同域登出及登录轮换验证父域会话不被外部子域替换。

## 实现

`index.ts` 提供零依赖、可直接导入的 `CookieManager` 与 `SessionManager`：

```ts
import { SessionManager } from "./index.ts";

const sessions = new SessionManager({
  secret: process.env.COOKIE_SECRET!,
});

// 登录：默认签发 __Host-session（无 Domain、Path=/、Secure、HttpOnly）
const issued = sessions.login(
  { host: "example.com", path: "/login", secure: true },
);
// issued.header 可直接写入响应头 Set-Cookie

// 接收：签名、来源、前缀、Domain、Path、有效期与活动会话一起核对
const result = sessions.authenticate(
  {
    url: "https://example.com/account",
    headers: { cookie: "__Host-session=<signed-value>" },
  },
);

// 轮换后旧会话标识立即失效；同域登出会删除 Cookie 并撤销会话
const rotated = sessions.rotate({ host: "example.com", path: "/", secure: true });
const cleared = sessions.logout({ host: "example.com", path: "/", secure: true });
```

签发值是一个 HMAC 签名信封，包含名称、值、Domain、Path、Secure、HttpOnly、SameSite、有效期、创建时间和签发主机。接收端因此能区分浏览器实际携带的属性，并拒绝无法用同一密钥验证的同名 Cookie。

同名 Cookie 按 RFC 6265 的顺序仲裁：路径最长者优先，路径相同则创建时间更早者优先。`__Host-` / `__Secure-` 前缀和 Domain 归属在签发、接收两侧都会检查。活动会话保存在 `SessionRegistry` 中，只接受当前会话标识；同一名称与域作用域还绑定签发主机，因此兄弟子域无法覆盖或登出父域会话。

运行验证：

```sh
npm test
```
