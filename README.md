# Farcaster消息类型与签名者有效性

Farcaster hub 收到的消息自带类型、正文和签名者，攻击者可让类型与正文不匹配，或用已被移除的签名者发新消息，还可靠时间戳平局让移除失效。给TypeScript hub 写供消息入库调用的校验包，验签与哈希由调用方注入：消息数据里的类型必须与正文种类对应（如 CAST_ADD 对应 cast 正文），不匹配拒绝；哈希须等于对 data_bytes 计算的结果，且 data_bytes 重新解码后与结构化字段一致；时间戳晚于当前时间 10 分钟以上拒绝；签名者须在该 fid 当前活跃签名者集合中；收到 SignerRemove 时该签名者此前签发的全部消息被逐出，之后它的新消息被拒绝；SignerAdd 与 SignerRemove 时间戳相同时移除胜出。用类型错配、data_bytes 与字段不一致、移除后重放和平局验证。

## 结构

- `src/types.ts`：消息、正文、哈希/签名方案的类型，以及消息类型 → 正文种类映射 `MESSAGE_BODY_KIND`。
- `src/errors.ts`：`ValidationError` 与拒绝原因枚举 `ValidationErrorCode`。
- `src/codec.ts`：`data_bytes` 默认编解码器（键排序的规范 JSON，字节字段用带标签 base64 承载），可整体替换为 protobuf 等实现。
- `src/validator.ts`：有状态的 `MessageValidator`，供消息入库前调用。
- `index.ts`：包入口。
- `test/`：基于 node:test 的 17 个用例，密码学部分用真实 Ed25519 签名与 SHA-256 注入。

## 校验规则（`MessageValidator.validate`）

校验失败抛出带 `code` 的 `ValidationError`，成功则入库并返回 `AcceptResult`：

1. 结构校验：`fid/timestamp/network` 为非负整数，字节字段为 `Uint8Array`，消息类型已知。
2. 类型/正文匹配：`data.type` 对应的正文种类必须等于 `data.body.bodyType`，否则 `TYPE_BODY_MISMATCH`。
3. 哈希绑定：注入的 `hash(dataBytes, hashScheme)` 必须等于 `message.hash`，否则 `HASH_MISMATCH`。
4. 编码一致性：`dataBytes` 重新解码须与结构化 `data` 深度相等（含字节比较），且结构化字段重新编码须与 `dataBytes` 字节一致，否则 `DATA_BYTES_MISMATCH`。
5. 时间戳：`data.timestamp > clock() + 10min` 拒绝（`FUTURE_TIMESTAMP`），恰好 10 分钟边界接受。
6. 签名：注入的 `verifySignature` 对 `hash/signature/signer` 验签，失败为 `INVALID_SIGNATURE`。
7. 签名者状态：
   - 普通消息的 `signer` 必须在该 fid 的活跃集合中，否则 `SIGNER_NOT_ACTIVE`。
   - `SignerAdd/SignerRemove` 必须由该 fid 的托管密钥（`resolveCustodySigner`）签发。
   - `SignerRemove` 把签名者标记为非活跃，并逐出该签名者此前签发的全部普通消息，哈希经 `evictedHashes` 返回；管理消息本身保留。
   - 同时间戳平局移除胜出：与已有 `SignerRemove` 时间戳相同或更旧的 `SignerAdd` 以 `SIGNER_ADD_SUPPRESSED` 拒绝，不能复活签名者；更晚时间戳的 `SignerAdd` 可以重新激活。
   - 相同 `hash` 的消息二次提交以 `DUPLICATE_MESSAGE` 拒绝。

## 调用方注入

验签、哈希、托管密钥解析和时钟都由 `ValidatorDeps` 注入，包本身不绑定具体密码学库；`encode/decode` 也可替换成 hub 实际的二进制编码。

```ts
const validator = new MessageValidator({
  hash: (dataBytes, scheme) => blake3(dataBytes),
  verifySignature: ({ hash, signature, signer }) => ed25519.verify(hash, signature, signer),
  resolveCustodySigner: (fid) => custodyStore.get(fid),
  // clock / encode / decode 可选
});

const { evictedHashes } = validator.validate(message);
```

## 运行测试

```sh
npm test
```

注：Node 的 `--experimental-strip-types` 不支持 `enum` 转译，测试脚本使用 `--experimental-transform-types`（Node 23 内置，无需安装依赖）。
