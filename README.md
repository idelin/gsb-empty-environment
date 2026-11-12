# IRC能力协商与标签转义注入

IRCv3 客户端带的消息标签会被服务器转发给协商了 message-tags 的接收者，攻击者可在标签值里放分号、空格或换行，伪造额外标签甚至整条命令。给TypeScript IRC 服务写供行解析与转发调用的包：整行含 CR、LF、NUL 一律拒绝；标签值按规范转义（\: 为分号，\s 为空格，\\ 为反斜杠，\r 与 \n）后才写出，转发的客户端标签只限 + 开头的标签且仅发给已 CAP ACK message-tags 的接收者；CAP REQ 的多个能力要么全部 ACK 要么整条 NAK；CAP LS 302 之后注册流程挂起到 CAP END，挂起期间的 PRIVMSG 当作未注册命令拒绝。用含分号和反斜杠的标签值、行内换行、部分未知能力的 REQ、未协商接收者验证。

## 模块

- `src/lineFramer.ts` — 有状态 CRLF 拆帧。任何裸 CR、裸 LF 或 NUL 都抛出
  `ProtocolError` 并复位；超过 512 字节的行同样拒绝。
- `src/tags.ts` — 标签解析、按 message-tags 规范的反转义/转义、序列化，以及
  基于接收者能力的标签过滤。
- `src/message.ts` — 单行 IRC 消息解析与回写；回写时统一走标签转义。
- `src/cap.ts` — CAP 协商状态机：原子 REQ、LS 302 注册挂起、CAP END 释放。
- `src/server.ts` — `IrcServer`/`Session`：命令分发、注册流程、频道/单播转发。
- `index.ts` — 公共导出。

## 安全模型

- **行级拒绝**：含 CR、LF、NUL 的整行在拆帧阶段即被拒绝，服务器回 `ERROR`
  并关闭连接，注入的伪造命令没有机会进入分发器。
- **写出前转义**：标签值中的 `;`、空格、`\`、CR、LF 分别编码为 `\:`、`\s`、
  `\\`、`\r`、`\n`，因此标签值无法跳出标签区伪造新标签或新命令。
- **客户端标签最小转发**：只有 `+` 开头（含 `vendor/+name` 形式）的客户端标签
  受 message-tags 限制；未 ACK 该能力的接收者收不到任何 `+` 标签，服务器标签
  不受影响。
- **原子能力协商**：`CAP REQ` 列表中任一能力未知即整条 NAK，不产生部分启用。
- **注册挂起**：`CAP LS 302` 后注册挂起直到 `CAP END`；挂起期间的 `PRIVMSG`
  等命令以 `451 ERR_NOTREGISTERED` 拒绝。

## 用法

```ts
import { IrcServer, type Transport } from "./index.ts";

class SocketTransport implements Transport {
  constructor(private readonly socket: { write(s: string): void; end(): void }) {}
  send(data: string) { this.socket.write(data); }
  close() { this.socket.end(); }
}

const server = new IrcServer();
// 每个新连接：
const session = server.addSession(new SocketTransport(socket));
socket.on("data", (chunk) => session.receive(chunk));
```

## 测试

零依赖，使用 Node 内置测试运行器（Node 22.18+ / 23 原生剥离 TS 类型）：

```sh
npm test
```

覆盖：含分号与反斜杠的标签值往返、行内换行/NUL 拒绝、含未知能力的部分 REQ
整单 NAK、未协商 message-tags 的接收者标签剥离、LS 302 挂起与挂起期 PRIVMSG
返回 451。
