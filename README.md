# OPAQUE 登录密钥句柄

TypeScript 客户端 SDK 的 OPAQUE 登录完成回调可以调用 `deriveLoginKeyHandles`
把 `session_key` 和 `export_key` 一次性转换为用途绑定的句柄。函数返回
`{ session, export }`，不会返回原始密钥或任何可导出的子密钥字节。

```ts
import {
  assertExportSubkey,
  assertSessionSubkey,
  deriveLoginKeyHandles,
} from "./index.ts";

const handles = await deriveLoginKeyHandles({
  sessionKey: loginResult.session_key,
  exportKey: loginResult.export_key,
  clientIdentity: "alice@example.com",
  serverIdentity: "api.example.com",
  transcriptHash,
});

assertSessionSubkey(handles.session);
assertExportSubkey(handles.export);

const ciphertext = await handles.session.withKey((key) =>
  crypto.subtle.encrypt({ name: "AES-GCM", iv }, key, plaintext),
);
```

入口也直接接受 OPAQUE 常见的 snake_case 字段：`session_key`、`export_key`、
`client_identity`、`server_identity` 和 `transcript_hash`。等价的 camelCase 与
snake_case 字段二选一即可。

## 派生规范

- 会话句柄以 `sessionKey` 为 HKDF 输入，标签为
  `opaque-client-sdk/session-subkey/v1`。
- 导出句柄以 `exportKey` 为 HKDF 输入，标签为
  `opaque-client-sdk/export-subkey/v1`。
- HKDF 使用 SHA-256，`salt` 为登录转录哈希；`info` 依次是长度前缀编码的
  域分隔符、用途标签、客户端身份、服务端身份和转录哈希。
- 输出为 256 位 AES-GCM 密钥，并以 `extractable: false` 导入，只能经
  `withKey` 临时交给 WebCrypto 使用。
- 相同登录输入产生稳定结果；不同标签、身份或转录哈希产生互不兼容的密钥。

## 防误用与序列化

- `session` 和 `export` 是不同的泛型句柄类型，并有运行时
  `assertSessionSubkey` / `assertExportSubkey` 检查。
- 原始会话/导出密钥在派生结束（包括校验失败）后清零。
- 两种原始密钥相同会直接拒绝，避免把导出密钥同时当作会话密钥。
- 句柄不暴露 `key`、`raw` 等属性；`JSON.stringify` 和字符串强制转换会抛出
  错误，`util.inspect` 仅显示用途、算法和销毁状态。
- `withKey` 提供的是不可导出的 `CryptoKey`；`subtle.exportKey("raw", key)`
  会失败。即使句柄被 `structuredClone` 降级为普通对象，也只剩非秘密元数据，
  不包含密钥和方法。

## 验证

```sh
npm test
```

测试用同一登录派生两类子密钥，验证跨用途/跨身份/跨转录哈希解密失败、
不可导出性、原始密钥清零，以及 JSON、日志、请求体和结构化克隆的序列化尝试。
