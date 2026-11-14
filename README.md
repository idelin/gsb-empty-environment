# WebSocket 握手鉴权组件

修复“消息服务只在登录时校验 Cookie、WebSocket 握手不检查 Origin，攻击者页面可用受害者 Cookie 建立连接并收发消息”的问题。提供可导入的握手鉴权组件与内嵌的消息服务示例，零依赖（仅 Node 内置模块，Node ≥ 22.6 的 `--experimental-strip-types`）。

## 组件

- `src/auth.ts` — 握手鉴权核心：`authenticateHandshake` 校验 Origin 白名单、`SameSite=Strict` 会话 Cookie（`sid`）、`Sec-WebSocket-Protocol` 子协议；任一不符即拒绝升级。另含会话存储（`createSessionStore`）、Cookie 解析与 `Sec-WebSocket-Accept` 计算。
- `src/message-service.ts` — 嵌入组件的消息服务：HTTP 接口（`/login`、`/whoami`、`/admin/set-role`、`/admin/revoke`）与 WebSocket 通道共用同一会话存储做授权；连接绑定会话、来源、角色与代次（generation），权限变更后以 `4001` 关闭旧连接。
- `src/ws.ts` — 最小 WebSocket 帧编解码（文本/关闭/ping/pong，支持掩码与 16/64 位长度）。
- `index.ts` — 公共导出入口。

## 用法

```ts
import { createMessageService } from "./index.ts";

const service = createMessageService({
  allowedOrigins: ["https://chat.example.com"],
  supportedProtocols: ["chat.v1"],
});
const port = await service.listen(8080);
```

也可只嵌入鉴权组件到已有 HTTP 服务：

```ts
import { authenticateHandshake, createSessionStore } from "./index.ts";

server.on("upgrade", (req, socket) => {
  const result = authenticateHandshake(req, {
    allowedOrigins: ["https://chat.example.com"],
    supportedProtocols: ["chat.v1"],
    sessions,
  });
  if (!result.ok) { /* 拒绝升级 */ }
  // result.context: { sessionId, userId, role, generation, origin }
});
```

## 测试

```sh
npm test
```

`test/handshake.test.ts` 覆盖六个场景（沙箱禁止监听端口，测试通过内存 socket 对驱动真实握手与帧收发）：

1. 合法同站连接：101 升级，回显绑定上下文（用户/角色/代次）。
2. 跨站握手：携带受害者有效 Cookie 但 Origin 不在白名单 → 403。
3. 空 Origin：缺失 Origin 头 → 403。
4. 子协议替换：仅提供不受支持的子协议 → 400。
5. Cookie 复用：会话撤销后旧 Cookie 握手与 HTTP 接口均 → 401。
6. 权限撤销：角色变更后旧连接被关闭（4001），HTTP 接口按新角色鉴权（管理接口 403）。
