# Signed SSE continuation

框架无关的 TypeScript SSE 续传工具。它把浏览器重连时提交的游标当作不可信输入，只接受服务端签发的、绑定当前租户和流的游标。

```ts
import {
  SseResume,
  extractLastEventIdRequest,
  formatResetEvent,
  formatSseEvent,
} from "netnew-20261112-0340";
```

## 事件 ID

服务端签发的事件 ID 严格采用：

```text
流名:序号:HMAC-SHA256前16字节的32位十六进制
```

HMAC 输入是 `[tenant, stream, sequence]` 的 JSON 元组，因此租户、流名和序号都被绑定。序号从 `0` 开始，必须是安全整数。

```ts
const id = resume.issueEventId(
  { tenant: "tenant-a", stream: "orders" },
  42,
);
// orders:42:4c6f...
```

也可直接使用 `issueEventId({ tenant, stream, sequence, secret })`（别名 `signEventId`）。

## 续传决策

`resolveResume` / `SseResume.resolve` 不抛业务异常，返回四类决策：

| 决策 | 含义 |
| --- | --- |
| `start` | 没有游标，从 `latestSequence + 1` 开始，不发 reset |
| `resume` | 游标有效且在窗口内，从 `nextSequence` 开始 |
| `reset` | 游标无效、跨租户、跨流或已淘汰；先发 reset，再从当前尾部 `latestSequence + 1` 开始 |
| `reject` | 传输值非法或头/查询参数冲突；HTTP 层应返回通用 400 |

`reset` 决策包含一个 `resetEvent`，内容只有 `event: reset` 和空 data，不包含签名、序号或失败原因。HTTP 400 响应也应保持通用，不要回传解析细节。

### 安全规则

- `Last-Event-ID` 与 `lastEventId` 同时出现时必须逐字节相同，否则 `reject`。
- 同一来源重复出现多个游标值也按歧义输入 `reject`。
- 游标含 `CR`、`LF`、`NUL`，或 UTF-8 编码超过 200 字节，返回 `reject`。
- 签名不符、签名属于别的租户或流、序号超出最近 1000 条窗口，均返回 `reset`，不暴露具体原因。
- 序号大于当前最新序号同样返回 `reset`。
- 没有游标的首次连接返回 `start`，不会误发 reset。

### HTTP 接入

```ts
const resume = new SseResume({ secret: process.env.SSE_CURSOR_SECRET! });

app.get("/events/:stream", (req, res) => {
  const subscription = {
    tenant: req.user.tenant,
    stream: req.params.stream,
  };
  const latestSequence = store.latestSequence(subscription);
  const cursorRequest = extractLastEventIdRequest({
    headers: req.headers,
    url: req.originalUrl,
  });
  const decision = resume.resolve(subscription, cursorRequest, latestSequence);

  if (decision.action === "reject") {
    res.status(400).end();
    return;
  }

  res.setHeader("Content-Type", "text/event-stream");
  res.setHeader("Cache-Control", "no-cache");

  if (decision.action === "reset") {
    res.write(formatResetEvent());
  }

  for (const event of store.readFrom(subscription, decision.nextSequence)) {
    res.write(
      formatSseEvent({
        id: resume.issueEventId(subscription, event.sequence),
        event: event.name,
        data: event.data,
      }),
    );
  }
});
```

`extractLastEventIdRequest` 支持 Fetch `Headers`、Node/Express 风格的小写 header 对象，以及字符串或 `URL`。查询参数由 `URLSearchParams` 解码，不会把游标拼进 SQL 或任意查询表达式。

## SSE 序列化

`formatSseEvent(event)` 只读取 `id`、`event`、`data`、`retry` 四个字段。字符串 data 的 `CR`、`LF`、`CRLF` 会归一化并逐行写成 `data: ...`；id 中的换行同样逐行写成 `id: ...`。因此 data 中的 `event:`、`id:`、`retry:` 或空行文本不会变成额外 SSE 字段。对象 data 使用 `JSON.stringify`。

```ts
formatSseEvent({
  id: "safe\nretry: 0",
  data: "line 1\n\nevent: forged",
});
// id: safe
// id: retry: 0
// data: line 1
// data:
// data: event: forged
```

## 其他 API

- `parseEventId(raw)`：只解析传输格式；格式非法时抛出通用 `InvalidLastEventIdError`。
- `verifyEventId(raw, { tenant, stream, secret })`：校验 HMAC 与当前订阅。
- `resolveLastEventId(options)`：`resolveResume` 的别名。
- `serializeSseEvent` / `serializeResetEvent`：序列化函数的别名。
- `MAX_LAST_EVENT_ID_BYTES`、`DEFAULT_RETENTION_WINDOW`、`SIGNATURE_HEX_LENGTH`：安全常量。

## 测试

```sh
npm test
```

测试覆盖跨租户游标、跨流游标、伪造签名、头/查询歧义、CR/LF/NUL、200 字节边界、1000 条保留窗口、reset 不含原因，以及 data/id 的换行注入。
