# SD-JWT 披露摘要与持有者绑定校验

零依赖的 TypeScript SD-JWT（RFC 9812）披露校验包，可直接被验证库导入。
调用方负责先完成签发者 JWT 与 KB-JWT 的**签名验证**，本包只负责基于
“已验签载荷”的披露摘要与持有者绑定声明校验，从而挡住两类攻击：

- 持有者出示签发者从未签过的披露（替换/伪造披露）；
- 持有者改写披露 JSON 里的值（改值会改变摘要）。

## 安装与测试

```sh
npm test          # node --experimental-strip-types --test "test/**/*.test.ts"
```

无第三方依赖，仅使用 Node 内置的 `node:crypto`、`node:test`，Node >= 16 可用。

## 快速开始

```ts
import {
  verifySdJwtPresentation,
  SdJwtVerificationError,
} from "./src/index.ts";

// 1. 调用方先用自己的 JWT 库验签 issuer JWT，解码出 payload。
const signedClaims = verifyIssuerSignature(issuerJwt);

try {
  const result = verifySdJwtPresentation({
    presentation: "eyJ...~WyJzYWx0Iiwic3NuIiwiMTIzIl0~eyJ...kb",
    signedClaims,
    kb: { expectedNonce, expectedAud },
  });
  // result.claims 为还原后的声明；result.kbJwt 仍需调用方独立验签
} catch (error) {
  if (error instanceof SdJwtVerificationError) {
    console.log(error.code); // 机器可读错误码
  }
}
```

只想校验披露、不解析整串组合表示时，可用底层 API：

```ts
import { verifyDisclosures, verifyKbJwt } from "./src/index.ts";

const { claims } = verifyDisclosures(signedClaims, encodedDisclosures);
verifyKbJwt({
  kbJwt,
  presentedSdJwt,            // `<jwt>~<d1>~...~`，sd_hash 的输入
  expectedNonce,
  expectedAud,
});
```

## 校验规则（与任务要求一一对应）

1. 每个披露是 `base64url(JSON [salt, 名, 值])`（数组项披露为 `[salt, 值]`），
   其 SHA-256（base64url）摘要必须能在已验签载荷的 `_sd` 数组（递归含嵌套对象）
   或数组的 `{"...": 摘要}` 标记中找到，否则以 `UNREFERENCED_DISCLOSURE` 拒绝。
2. 同一摘要在已验签载荷中只允许出现一次，重复引用以 `DUPLICATE_SD_DIGEST` 拒绝；
   同一披露在展示中重复出示以 `DUPLICATE_DISCLOSURE_DIGEST` 拒绝。
3. 披露的声明名不得是保留名 `_sd` 或 `...`，否则 `RESERVED_DISCLOSURE_NAME`。
4. 披露名不得与同一对象中的明文声明冲突，否则 `CLAIM_NAME_CONFLICT`。
5. KB-JWT（`typ: "kb+jwt"`）的 `sd_hash` 必须等于所展示 SD-JWT 串
   （issuer JWT + 全部披露、以 `~` 结尾）的 SHA-256 摘要，否则
   `KB_SD_HASH_MISMATCH`；`nonce`、`aud` 不匹配分别以
   `KB_NONCE_MISMATCH`、`KB_AUD_MISMATCH` 拒绝（`aud` 支持字符串或数组）。
6. 仅支持 `_sd_alg` 缺省或 `"sha-256"`，其他值返回 `UNSUPPORTED_SD_ALG`。

披露值内部若还嵌套 `_sd`，会迭代展开到不动点；未出示的 `_sd` 数组与
`...` 标记在还原结果中被剔除。输入载荷使用深拷贝，不会被修改。

## 错误码

`MALFORMED_PRESENTATION`、`UNSUPPORTED_SD_ALG`、`MALFORMED_DISCLOSURE`、
`MALFORMED_SD_ARRAY`、`MALFORMED_ARRAY_MARKER`、`DUPLICATE_SD_DIGEST`、
`DUPLICATE_DISCLOSURE_DIGEST`、`UNREFERENCED_DISCLOSURE`、
`RESERVED_DISCLOSURE_NAME`、`CLAIM_NAME_CONFLICT`、`MALFORMED_KB_JWT`、
`KB_SD_HASH_MISMATCH`、`KB_NONCE_MISMATCH`、`KB_AUD_MISMATCH`。

## 安全边界

- 本包不验证 JWT/KB-JWT 签名，也不校验 `cnf.jwk` 绑定；请在调用方完成，
  否则 KB-JWT 只能证明“持有对应私钥者看到了该展示”，不能证明签发者背书。
- `sd_hash`、`nonce` 的字符串比较使用 `timingSafeEqual` 做常量时间比较。
- 摘要始终对**编码后的披露字符串字节**计算，绝不重新序列化 JSON，
  避免规范化差异导致的摘要不一致。
