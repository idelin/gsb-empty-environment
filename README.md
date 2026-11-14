# 登录成功后会话标识没有轮换

门户在认证前后沿用同一个会话Cookie，攻击者可预先把自己的会话标识发给受害者，等其登录后共用同一已认证会话。请实现嵌入TypeScript会话服务的可导入登录会话组件。认证成功后必须生成新标识并作废旧标识，标识绑定用户、认证时间、设备摘要与权限代次；权限变更、密码重置和登出使旧标识立即失效。会话查找按服务端存储而不是客户端声明，固定标识、缺失标识和并发登录有明确策略。用会话固定、并发登录、权限提升、登出后复用和合法续期验证认证状态只属于新标识。

## 实现

`index.ts` 导出可直接嵌入现有 TypeScript 服务的 `LoginSessionService`
（同时以 `SessionService`、`SessionManager` 和默认导出提供）。服务使用
256 位随机、URL 安全的服务端标识；Cookie 中的值只作为存储键，不包含用户、
权限或其他可被客户端伪造的认证信息。

```ts
import { LoginSessionService, digestDevice } from "./index.ts";

const sessions = new LoginSessionService({
  idleTimeoutMs: 30 * 60 * 1000,
  absoluteTimeoutMs: 12 * 60 * 60 * 1000,
  concurrentLoginPolicy: "single",
});

// 设备摘要必须由服务端根据可信信号计算，不能直接采用客户端声明。
const deviceDigest = digestDevice(
  request.headers["user-agent"] ?? "",
  request.ip,
  request.headers["sec-ch-ua"] ?? "",
);

// 凭据校验完成后调用。presentedSessionId 可以是登录前 Cookie 中的任意值。
const login = sessions.login({
  userId: user.id,
  deviceDigest,
  permissionGeneration: user.permissionGeneration,
  presentedSessionId: request.cookies[SESSION_COOKIE],
});

response.setCookie(SESSION_COOKIE, login.sessionId, {
  httpOnly: true,
  secure: true,
  sameSite: "lax",
  path: "/",
});
```

认证查找使用服务端记录解析身份。请求中额外声明的 `userId`、
`permissionGeneration` 等内容不会参与授权：

```ts
const inspection = sessions.inspect({
  sessionId: request.cookies[SESSION_COOKIE],
  deviceDigest,
});

if (!inspection.authenticated) {
  return response.status(401).json({ reason: inspection.reason });
}

const { userId, authenticatedAt, permissionGeneration } = inspection.session;
```

## 会话策略

- **登录轮换**：每次 `login()` 都生成新标识。传入的 `presentedSessionId`
  如果仍存在于服务端，会先被删除，之后绝不会复用；因此会话固定攻击者
  预先植入的标识在认证后失效。
- **固定标识**：从未由服务端发出的标识返回 `unknown_session_id`，不能
  通过客户端声明“认领”某条会话。标识格式非法时返回
  `malformed_session_id`。
- **缺失标识**：`inspect()` 返回 `missing_session_id`，
  `authenticate()` 返回 `null`；本组件不会为匿名请求创建认证会话。
- **并发登录**：默认 `single`，同一用户的新登录立即撤销其旧会话。也可
  显式选择 `allow`（允许多会话）或 `limit`（配合
  `maxSessionsPerUser` 淘汰最旧会话）。并发场景下不会生成重复标识。
- **权限变更**：`changePermissions(userId[, generation])` 推进权限代次
  并撤销该用户全部会话。登录时更高代次会撤销旧会话，更低代次抛出
  `PermissionGenerationConflictError`；服务端查找还会再次校验代次。
- **密码重置**：`resetPassword(userId)` 推进凭据代次并立即删除全部会话；
  新登录才会获得新代次。
- **登出**：`logout()` 按服务端存储删除当前标识；重复登出或登出后复用
  旧标识均无法通过认证。提供设备摘要时还会校验设备绑定。
- **设备绑定**：会话记录保存登录时的设备摘要。认证、续期和登出传入
  设备摘要时必须匹配，否则分别返回 `device_mismatch` 或 `false`。
- **合法续期**：`renew()` 延长空闲过期时间，但不改变认证时间，也不会
  提升权限代次；绝对生存期是硬上限。默认续期不换标识，设置
  `rotateOnRenew: true` 后每次续期也轮换标识并立即使旧标识失效。

服务端存储通过 `SessionStore` 接口注入。生产环境应将其实现为数据库或
集中缓存，并以 `sessionId` 作为键；`InMemorySessionStore` 仅适合单进程
嵌入场景。所有失效操作都是同步删除，不依赖客户端清除 Cookie。

## 验证

```sh
npm test
```

`session.test.ts` 覆盖会话固定、缺失/固定/伪造标识、并发登录、权限提升、
密码重置、登出后复用、设备不匹配、普通续期和续期轮换。
