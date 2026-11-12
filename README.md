# WebSocket 子协议握手协商包

针对 RFC 6455 §4.2 `Sec-WebSocket-Protocol` 的可导入 TypeScript 协商库，零运行时依赖，防御两类常见滥用：

- 客户端提交服务端不支持的子协议，或在列表里夹带 `bearer.<令牌>` 试图让服务端回显进响应头/访问日志；
- 把协议名当成权限载体，连接建立后发送该子协议不允许的消息类型。

## 协商规则

- 客户端列表按逗号分隔，元素前后的空白（OWS）会被忽略；
- 服务端只在客户端列表与自身列表的**交集**中，按**服务端优先级**选择一个，使用客户端提交的原始拼写（token 大小写敏感）原样回显；
- 没有交集时**不输出** `Sec-WebSocket-Protocol` 响应头，握手继续，结果 `subprotocol` 为 `null`；
- 子协议名必须是 RFC 7230 token 字符；空元素、非 token 名、重复元素（含重复 bearer）一律拒绝握手（HTTP 400）；
- `bearer.<令牌>` 元素只用于提取令牌，**永不**出现在响应头、`audit` 审计对象或任何日志字符串中；多个 bearer 或空令牌拒绝握手；
- 选定的子协议绑定一份消息策略，越权的帧类型或应用层 `type` 一收到即以关闭码 **1008（Policy Violation）**关闭。

## 包结构

- `src/negotiate.ts` — 纯函数协商核心：`negotiateSubprotocol`、`splitProtocolHeader`、`isToken`、`redactBearer`、`safeHeaderForLog`、`BearerToken`
- `src/gate.ts` — 消息类型门控：`decideFrame`、`enforceMessage`、`MessagePolicy`
- `src/server.ts` — 零依赖 RFC 6455 服务端集成层（HTTP Upgrade、分帧、ping/pong/close、1008 接线）
- `index.ts` — 统一导出入口
- `tests/` — 单元测试 + 内存传输端到端测试（不依赖网络端口）
- `examples/usage.ts` — 纯函数接入任意库与内置服务两种用法

## 快速上手

```ts
import { negotiateSubprotocol, enforceMessage } from "ws-subprotocol-negotiation";

const result = negotiateSubprotocol(request.headers["sec-websocket-protocol"], {
  serverProtocols: ["v1.chat", "v1.binary"], // 优先级从高到低
});

if (!result.ok) {
  respond400(result.reason); // duplicate-element / invalid-token / multiple-bearer-tokens ...
  return;
}

// 无交集时返回 {}，即不发送 Sec-WebSocket-Protocol 头
writeResponseHeaders(result.toResponseHeaders());

const token = result.bearerToken?.reveal(); // 仅在需要校验时显式读取
logger.info("ws handshake", result.audit);  // 令牌已脱敏为 bearer.[REDACTED]

// 消息到达时按子协议策略门控，越权即 close(1008)
enforceMessage(ws, "text", data, policies[result.subprotocol ?? ""] ?? denyAllPolicy);
```

消息策略示例：

```ts
const policies = {
  "v1.chat":   { frameKinds: ["text"],   appTypes: ["message", "typing"] },
  "v1.binary": { frameKinds: ["binary"] },
};
```

## 令牌防泄露设计

- `BearerToken` 的原始值保存在私有字段，`toString()` / `toJSON()` / `util.inspect` 均输出 `[REDACTED]`，只有 `reveal()` 能取到原文；
- `audit.offered` 与 `safeHeaderForLog()` 对所有 `bearer.*` 元素统一脱敏，拒绝原因（`reason`）不含任何用户数据；
- `toResponseHeaders()` 只可能包含普通子协议名，bearer 元素不参与选择也无法被回显。

## 测试

```sh
npm test
```

覆盖场景（共 23 个用例）：无交集不回显、重复名 400 拒绝、token 字符校验、bearer 令牌提取与脱敏、响应头/审计无令牌泄露、越权帧类型与越权应用层 `type` 的 1008 关闭、服务端优先级选择、无策略连接的兜底 1008。

> 当前沙箱禁止 TCP 监听，端到端测试通过内存 `Duplex` 管道（`tests/helpers/`）驱动完整的 HTTP Upgrade 与 RFC 6455 分帧栈；在普通环境中 `NegotiatingWebSocketServer.listen(port)` 可直接监听端口。

## 运行环境

- Node.js >= 22.6（使用内置 type stripping 直接运行 `.ts`；接入打包器时也可正常导入源码）
- 无第三方运行时依赖；类型检查需自行安装 `typescript` 后执行 `npm run typecheck`
