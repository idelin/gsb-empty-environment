# OpenID4VCI proof JWT 校验包

面向 TypeScript 发行服务的零依赖校验包。凭证端点可用它检查持有者提交的
`openid4vci-proof+jwt`，并阻断跨发行方、旧会话和已消费 nonce 的重放。

## 校验规则

- `typ` 必须严格等于 `openid4vci-proof+jwt`
- `aud` 必须是字符串，且严格等于构造时传入的 `issuer`
- `nonce` 必须存在、由本服务签发、未过期且尚未消费
- `iat` 必须是 NumericDate，不早于 nonce 签发秒，且不晚于当前时间 60 秒
- `jwk` 必须是公钥；包含 `d`（以及 `p`、`q`、`dp`、`dq`、`qi`、`oth`、`k`）直接拒绝
- 使用 header 中的 `jwk` 校验 compact JWS 签名
- 全部检查通过后才消费 nonce，因此失败请求不会烧掉合法 nonce

支持的签名算法：`ES256`、`ES384`、`ES512`、`RS256`、`RS384`、`RS512`、
`PS256`、`PS384`、`PS512`、`EdDSA`。明确拒绝 `none`、HMAC，以及
`jwk`/`alg` 类型不匹配的证明。

## 使用方式

```ts
import { ProofValidator } from "./index.ts";

const issuer = "https://issuer.example.com";
const proofs = new ProofValidator({
  issuer,
  nonceTtlSeconds: 300, // 默认 300 秒
});

// 在 nonce 端点或 token 响应中返回这个值。
const nonce = proofs.issueNonce();

// 在 credential 端点中校验 holder 提交的 proof JWT。
const result = proofs.validate(proofJwt);
if (!result.valid) {
  return { error: "invalid_proof", error_description: result.message };
}

// result.payload 是已验签的 proof claims；成功校验会消费 nonce。
// 需要异常式 API 时可调用 proofs.verify(proofJwt)，失败时抛出
// ProofValidationError（含 code 和 message）。
```

分布式部署时应注入一个共享且原子消费的 `NonceStoreLike`（例如基于 Redis
的实现），并让 `currentTime()` 返回 epoch 毫秒：

```ts
const proofs = new ProofValidator({
  issuer,
  nonceStore: redisNonceStore,
});
```

也可使用函数式入口：

```ts
import {
  NonceStore,
  createProofValidator,
  validateProof,
  verifyProof,
} from "./index.ts";

const nonceStore = new NonceStore({ nonceTtlSeconds: 300 });
const validator = createProofValidator({ issuer, nonceStore });
const nonce = validator.issueNonce();

const result = validateProof(proofJwt, { issuer, nonceStore });
const verified = verifyProof(proofJwt, { issuer, nonceStore }); // 失败时抛错
```

`validateProof` / `validator.validate()` 返回
`{ valid: true, payload, header, publicKeyJwk }`，或
`{ valid: false, code, message, error }`。`verifyProof` /
`validator.verify()` 成功返回 `{ payload, header, publicKeyJwk }`，失败抛出
`ProofValidationError`。

## 测试

```sh
npm test
```
