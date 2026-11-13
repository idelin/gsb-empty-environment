# WebAuthn PRF 输入校验

浏览器端 TypeScript 校验包，用来阻止两类 WebAuthn PRF 滥用：

- 调用方自选盐直接进入 PRF，成为跨协议 HMAC oracle。
- `evalByCredential` 指向未出现在 `allowCredentials` 中的凭据。

## 用法

```ts
import { validatePrfExtension } from "./index.ts";

const allowCredentials = [{ type: "public-key", id: credentialId }];
const extensions = await validatePrfExtension(
  {
    prf: {
      eval: { first: appSalt },
      evalByCredential: {
        [credentialIdBase64Url]: { first: appSalt },
      },
    },
  },
  allowCredentials,
);

await navigator.credentials.get({ publicKey: { allowCredentials, extensions } });
```

请只把校验函数的返回值传给 WebAuthn。原对象中的盐不会被原地修改。

## 规则

- 所有 `first` / `second` 盐均转换为
  `SHA-256("WebAuthn PRF" || 0x00 || salt)`。
- `evalByCredential` 的每个键必须是规范、无填充的 base64url 字符串，并精确对应
  `allowCredentials` 中的一个凭据 ID。
- `allowCredentials` 为空或未提供时，只要出现 `evalByCredential` 就拒绝。
- 每个 PRF 值对象都必须带 `first`；缺失时拒绝。
- 任一规则失败时抛出 `PrfValidationError`，其 `code` 可用于区分拒绝原因。

## API

- `derivePrfSalt(salt, subtle?)`：异步计算域分离后的 PRF 盐。
- `preparePrfInput(input, allowCredentials?, subtle?)`：校验并返回含哈希盐的新对象。
- `validatePrfExtension(input, allowCredentials?, subtle?)`：接受裸 PRF 输入或
  `{ prf: ... }` 扩展对象，并保持输入外形返回。
- `assertPrfInputShape(...)` / `isValidPrfInput(...)`：同步检查结构、密钥和
  `allowCredentials` 映射，不执行哈希。
