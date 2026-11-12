# SSB 消息规范序列化与分叉判定

面向 Secure Scuttlebutt 日志摄入的 TypeScript 校验包。它只接受逐字节符合规范序列化的消息，防止通过换键序、改空白或重复编码制造不同消息 ID 的重复条目。签名算法不内置，由调用方注入。

## 校验规则

- 消息文本必须严格等于 `JSON.stringify(parsed, null, 2)` 的结果，顶层键序固定为 `previous`、`sequence`、`author`、`timestamp`、`hash`、`content`、`signature`。多空格、制表符、CRLF、行尾换行、额外字段和换键序都会被拒绝。
- 消息 ID 为 `Base64(SHA-256(UTF-8(canonicalText))) + ".sha256"`，其中 `canonicalText` 包含 `signature` 字段。按本题规定，ID 不带 `%` 前缀。
- 日志必须从 `sequence === 1`、`previous === null` 开始；之后 `previous` 必须等于上一条消息 ID，`sequence` 恰好加一。
- `author` 必须等于该 `MessageLog` 的主人。
- 已接受的 `sequence` 再次出现且 ID 不同即为分叉：该作者日志立即永久隔离，保存两条规范文本、ID 和解析结果作为证据，后续该作者消息一律拒绝。
- 完全相同的重复消息是幂等 `duplicate`，不追加第二份，也不触发分叉。

## 用法

```ts
import { MessageLog } from "./index.ts";

const log = new MessageLog({
  author: "@alice.ed25519",
  // 返回 true 表示已用 Alice 的公钥验证了 signingPayload 和 signature。
  verify: ({ signingPayload, signature, message }) =>
    verifyEd25519(message.author, signingPayload, signature),
});

const result = log.ingest(rawMessageText);

if (result.status === "accepted") {
  console.log(result.id);
} else if (result.status === "duplicate") {
  console.log("already ingested", result.id);
} else {
  console.log(result.reason, result.detail);
}
```

注入的 `verify` 收到：

- `canonical`：七字段、两空格缩进的完整规范文本，含签名。
- `signingPayload`：六字段、两空格缩进的签名载荷，不含 `signature` 字段。
- `message` 和 `signature`：解析后的消息及签名字符串。

## 结果与证据

`ingest` 返回 `accepted`、`duplicate` 或 `rejected`。拒绝原因包括 `INVALID_JSON`、`INVALID_MESSAGE`、`NON_CANONICAL`、`INVALID_SIGNATURE`、`WRONG_AUTHOR`、`AUTHOR_FORKED`、`FORK`、`SEQUENCE_GAP`、`SEQUENCE_OUT_OF_ORDER`、`BROKEN_CHAIN`。

分叉证据保存在 `log.forkEvidence`（也可调用 `log.getForkEvidence()`）：

```ts
const evidence = log.forkEvidence[0];
evidence.sequence;                 // 冲突所在的 sequence
evidence.accepted;                 // 先被接受的 StoredMessage
evidence.conflicting;              // 不同 ID 的 StoredMessage
evidence.accepted.canonical;       // 两条原始规范文本
evidence.conflicting.canonical;
```

## 低层函数

- `serializeCanonical(message)`：七字段规范文本。
- `serializeSigningPayload(message)`：不含签名的签名载荷。
- `computeMessageId(message)` / `computeIdFromCanonicalText(text)`：计算消息 ID。
- `inspectMessageText(text, verify)`：解析、严格规范比对、验签并计算 ID。
- `canonicalizeMessage(text | message)`：仅做规范文本转换。
- `isCanonicalMessageText(text)`：布尔检查。

运行验证：

```sh
npm test
```
