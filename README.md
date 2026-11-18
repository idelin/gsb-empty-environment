# WebSocket 子协议协商与消息类型授权

一个零依赖的 TypeScript 包，按 RFC 6455 第 4.2 节协商
`Sec-WebSocket-Protocol`，并将选定的子协议绑定到连接允许的应用消息类型。

## 安装与导入

包直接以 TypeScript 源码导出，可由 Node.js 22.6+ 的类型剥离能力或 TypeScript
工具链导入：

```ts
import {
  createSubprotocolPolicy,
  negotiateHandshake,
  enforceMessageType,
} from "netnew-20261112-0535";
```

本地仓库测试使用：

```sh
npm test
```

## 握手

服务端规则数组的顺序就是优先级。每条规则声明允许的应用消息类型：

```ts
const policy = createSubprotocolPolicy([
  { name: "chat.v2", messageTypes: ["chat.message", "chat.typing"] },
  { name: "chat.v1", messageTypes: ["chat.message"] },
]);

const result = negotiateHandshake({
  header: request.headers["sec-websocket-protocol"],
  protocols: policy,
  logger: (record) => auditLogger.info(record),
});
```

`result` 的关键字段：

- `accepted`：握手是否可继续。格式错误或重复名称时为 `false`，HTTP 状态为
  `400`。
- `subprotocol`：唯一选中的子协议；没有交集时为 `null`，且
  `responseHeaders` 不包含 `Sec-WebSocket-Protocol`。
- `responseHeaders`：仅在选中时包含
  `"Sec-WebSocket-Protocol": "<选中的子协议>"`。
- `token`：从 `bearer.<令牌>` 元素提取的令牌，供业务认证使用。
- `allowedMessageTypes`：该连接仅允许发送的消息类型。

客户端列表按逗号拆分并去掉首尾空白。服务端按自身规则顺序选择第一个交集，
不会回显客户端未提供的名称。每个元素必须是 RFC 6455 token；任意元素为空、
含非法字符或出现重复名称都会拒绝握手。服务端规则本身也会在启动时校验，
包含非法名称、重复名称或 `bearer.` 命名空间时直接抛出 `TypeError`。

重复的客户端名称按完整元素比较，因此重复的 bearer 元素同样拒绝；多个不同的
bearer 元素因令牌含义不明确也会拒绝。

## bearer 令牌隔离

`bearer.<令牌>` 只被解析到 `result.token`，不会：

- 成为子协议候选；
- 出现在 `responseHeaders` 或握手回显中；
- 出现在内置日志记录中。

记录日志时应使用包生成的 `record`（或 `handshakeLogRecord(result)`），不要直接
记录整个 `result`，因为 `result.token` 是业务方需要读取的敏感字段。

## 消息类型约束

收到消息时调用授权函数；未授权类型立即以关闭码 `1008` 关闭：

```ts
socket.on("message", (data) => {
  const messageType = readMessageType(data);
  if (!enforceMessageType(result, messageType, socket)) {
    return;
  }

  handleAuthorizedMessage(result.subprotocol, data);
});
```

`enforceMessageType` 在允许时返回 `true`；否则调用
`socket.close(1008, "Message type is not permitted")` 并返回 `false`。也可使用
`isMessageTypeAllowed`、`shouldCloseForMessageType` 或
`closeCodeForMessageType` 做无副作用的判断。

## 测试覆盖

`test/handshake.test.ts` 覆盖无交集、服务端优先级、空白处理、重复名称、非法
token、bearer 不回显/不写日志、仅令牌场景、越权消息类型及 `1008` 关闭。
