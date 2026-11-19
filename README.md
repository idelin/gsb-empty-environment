# IRC 能力协商与标签转义注入防护

IRCv3 客户端带的消息标签会被服务器转发给协商了 `message-tags` 的接收者。攻击者可在标签值里放分号、空格或换行，伪造额外标签甚至整条命令。本包提供供 TypeScript IRC 服务直接调用的行解析、安全写出、CAP 协商与转发原语。

## 安全边界

- **整行拒绝**：`parseLine` 与 `LineDecoder` 对任何含 CR、LF、NUL 的行一律抛 `ProtocolError`/计入 `rejected`，绝不解析成命令。
- **标签转义**：标签值仅在 `serializeTags`/`serializeMessage` 写出时按 IRCv3 规范转义（`;` → `\:`、空格 → `\s`、`\` → `\\`、CR → `\r`、LF → `\n`）；解析时 `\;` 不会拆分出新标签，标签值里的分号无法伪造标签。NUL 直接抛 `SerializationError`。
- **转发白名单**：`forwardMessage`/`buildForwardLine` 只转发 `+` 开头的客户端标签，且仅附加给已 `CAP ACK message-tags` 的接收者；未协商接收者收到无 `@` 段的同一消息体。
- **CAP REQ 原子性**：能力列表全部受支持才整体 ACK，任一未知则整条 NAK，不留部分启用状态。
- **注册挂起**：`CAP LS 302` 后 `registrationSuspended = true`，直到 `CAP END`；挂起期间 `RegistrationGate` 对 `PRIVMSG` 等非注册命令返回 451。

## 模块

- `src/tags.ts` — `escapeTagValue` / `unescapeTagValue` / `parseTagString` / `serializeTags` / `selectClientTags`
- `src/parser.ts` — `parseLine`（CR/LF/NUL 拒绝、标签/前缀/参数解析）与 `LineDecoder`（流式分帧）
- `src/serialize.ts` — `serializeMessage` 安全写出（fail-closed）
- `src/cap.ts` — `CapabilityRegistry` / `ConnectionCapState` / `CapNegotiator`
- `src/session.ts` — `RegistrationGate`（挂起期 451 门禁）
- `src/forward.ts` — `buildForwardLine` / `forwardMessage`
- `index.ts` — 统一导出

## 用法

```ts
import {
  LineDecoder, parseLine, CapNegotiator, CapabilityRegistry,
  ConnectionCapState, RegistrationGate, forwardMessage, CAP_MESSAGE_TAGS,
} from './index.ts';

const decoder = new LineDecoder();
const { lines, rejected } = decoder.feed(socketChunk);
for (const raw of rejected) socket.destroy(); // 含 CR/LF/NUL 的帧

const capState = new ConnectionCapState();
const cap = new CapNegotiator(new CapabilityRegistry([CAP_MESSAGE_TAGS]), capState);
const gate = new RegistrationGate(capState);

for (const raw of lines) {
  const msg = parseLine(raw); // 非法字节在此抛 ProtocolError
  const blocked = gate.check(msg);
  if (blocked) continue; // 451：注册挂起期间的 PRIVMSG 等
  if (msg.command === 'CAP') cap.handle(msg.params);
  if (msg.command === 'PRIVMSG') forwardMessage(msg, recipients);
}
```

## 测试

Node ≥ 23（原生 TypeScript type stripping）：

```sh
node --experimental-strip-types --test test/*.test.ts
```

覆盖：含分号与反斜杠的标签值转义/往返、行内 CR/LF/NUL 拒绝、部分未知能力的 REQ 整单 NAK、`CAP LS 302` 挂起与 451、未协商接收者收不到任何标签段。
