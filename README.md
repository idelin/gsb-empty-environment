# Farcaster消息类型与签名者有效性

Farcaster hub 收到的消息自带类型、正文和签名者，攻击者可让类型与正文不匹配，或用已被移除的签名者发新消息，还可靠时间戳平局让移除失效。给TypeScript hub 写供消息入库调用的校验包，验签与哈希由调用方注入：消息数据里的类型必须与正文种类对应（如 CAST_ADD 对应 cast 正文），不匹配拒绝；哈希须等于对 data_bytes 计算的结果，且 data_bytes 重新解码后与结构化字段一致；时间戳晚于当前时间 10 分钟以上拒绝；签名者须在该 fid 当前活跃签名者集合中；收到 SignerRemove 时该签名者此前签发的全部消息被逐出，之后它的新消息被拒绝；SignerAdd 与 SignerRemove 时间戳相同时移除胜出。用类型错配、data_bytes 与字段不一致、移除后重放和平局验证。

## 用法

```ts
import { HubValidator } from "./index.ts";

const validator = new HubValidator({
  hashData,         // (dataBytes) => hash，调用方注入
  decodeData,       // (dataBytes) => MessageData，调用方注入
  verifySignature,  // (message) => boolean，调用方注入
  isCustodySigner,  // (fid, signer) => boolean，调用方注入
  now: () => Math.floor(Date.now() / 1000),
});

const result = validator.validateAndStore(message);
if (!result.ok) {
  // result.code: TYPE_BODY_MISMATCH / INVALID_HASH / DATA_BYTES_MISMATCH /
  // TIMESTAMP_TOO_FAR_FUTURE / INACTIVE_SIGNER / INVALID_SIGNATURE / MALFORMED_DATA_BYTES
}
```

校验顺序：类型与正文匹配 → hash 与 data_bytes 一致且解码后与结构化字段一致 →
时间戳不晚于当前时间 10 分钟 → 验签 → 签名者在该 fid 活跃签名者集合中
（SignerAdd/SignerRemove 也可由 custody 地址签发）。全部通过后入库；
SignerRemove 生效时逐出目标签名者此前签发的全部已入库消息，
平局（add/remove 时间戳相同）时移除胜出。

运行测试：`npm test`（需要支持 `--experimental-strip-types` 的 Node.js）。
