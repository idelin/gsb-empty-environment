# 请求走私防护：唯一消息边界中间件

这是一个无外部依赖的 TypeScript/HTTP1 消息边界层，用于反向代理在把请求交给后端之前，只做一次 framing（消息边界）判定。

它针对的核心问题是：代理和后端分别选择 `Transfer-Encoding` 或 `Content-Length`，攻击者利用两者差异把第二个请求藏进第一个请求的 body，读走其他用户的响应。

## 保证的不变量

- 每个请求只能得到一种 framing：`none`、`content-length`、`chunked` 或 `upgrade`。
- 同时出现 `Content-Length` 与 `Transfer-Encoding` 直接拒绝。
- 多个 `Transfer-Encoding` 字段、重复 transfer-coding、CSV 形式的多余 coding 直接拒绝。
- 最后一个 transfer-coding 必须是 `chunked`；目前只支持单一 `chunked` coding。
- chunk-size 必须是严格十六进制；chunk 数据后必须是 CRLF；终止块只接受 `0\r\n\r\n`（允许无歧义的 chunk-ext）。
- chunk-ext 只接受无分隔符空白、无重复名、无控制字符的规范 token 或合法 quoted-string；空值、未闭合引号、非法转义等有歧义写法拒绝。
- 非空 trailer 默认拒绝；开启后也禁止 `Content-Length`、`Transfer-Encoding`、`Host` 等边界相关字段。
- `Upgrade: h2c` 默认拒绝；只有显式开启 `allowH2cDowngrade` 才剥离升级头并按 HTTP/1.1 转发。
- 其他 HTTP/1.1 `Upgrade` 默认拒绝；显式开启后进入 opaque tunnel，后续字节不再当作 HTTP 请求解析。
- 转发给后端的头集合已经去掉 hop-by-hop 头，并且至多保留一个规范的 `Content-Length`；不会同时出现 `Transfer-Encoding`。
- chunked 请求会被严格解码后重新以 `Content-Length` 定界。后端拿到的是边界对象加实体 body，不需要、也不应该再次从头部推断 framing。
- 连接复用前缓冲区必须为空；消息边界后还有残留字节时，默认清空缓冲区并关闭连接，残留字节不会被当成隐式请求。
- 超时、半关闭和连接结束只做清理，不会把部分请求“补全”成新请求。

## 使用

```ts
import {
  ProxyConnection,
  createMessageBoundaryMiddleware,
} from "./index.ts";

const middleware = createMessageBoundaryMiddleware({
  // 默认值如下，按需显式放开
  allowH2cDowngrade: false,
  allowGenericUpgrade: false,
  allowTrailers: false,
  allowPipelining: false,
  maxHeaderBytes: 16 * 1024,
  maxContentLength: 64 * 1024 * 1024,
});

const connection = new ProxyConnection({
  middleware,
  backend: async (request) => {
    // request.headers 已规范化
    // request.boundary 是唯一的 framing 决定
    // request.body 恰好等于 boundary.forwarding.contentLength
    await backend.handle(request);
  },
  onReject: (rejection) => {
    // rejection.error.code 是稳定的机器可读错误码
  },
});

await connection.feed(inboundBytes);
await connection.end(); // 或 await connection.timeout()
```

只需要做单次判定时也可以直接使用：

```ts
const middleware = createMessageBoundaryMiddleware();
const result = middleware.parseMessage(buffer);
if (result.status === "complete") {
  console.log(result.value.headers, result.value.boundary, result.value.body);
}
```

`verifyForwardedRequest()` 会校验 `body.length`、`boundary` 和规范化头三者一致；`RecordingBackend` 默认在接收请求时执行该检查。

## 升级策略

`Upgrade: h2c` 不是普通 body framing。默认拒绝，避免代理把 h2c 请求悄悄变成 HTTP/1.1 或反向把 HTTP/1.1 当成 h2。

显式开启 downgrade 后：

- 校验 `Connection: Upgrade`、唯一的 `Upgrade: h2c` 和 base64url 形式的 `HTTP2-Settings`；
- 删除 `Connection`、`Upgrade`、`HTTP2-Settings` 以及 `Connection` 里点名的其他 hop-by-hop 头；
- 按普通 HTTP/1.1 的 CL/TE 规则判断 body，并交给后端。

## 测试

```bash
npm test
```

测试覆盖：

- 合法 `Content-Length` 与合法 chunked（含 chunk-ext、分片到达的终止符）；
- `CL.TE`、`TE.CL`、重复 `Transfer-Encoding`；
- chunk 终止符变体、非法 chunk 数据结束符、非空 trailer；
- `h2c` 默认拒绝与显式降级；
- generic Upgrade 默认拒绝、显式 tunnel；
- 消息边界后的夹带、缓冲残留清理、连接复用；
- 超时、半关闭和 EOF 不产生隐式请求；
- 代理交给后端的 body 长度、规范化头和 `MessageBoundary` 完全一致。
