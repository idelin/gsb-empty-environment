# OpenID Federation 信任链校验包

为 TypeScript 信任方（Relying Party）提供可导入的 OpenID Federation 1.0
信任链校验能力，防御攻击者伪造从属声明、借合法信任链为自己背书的场景。

零运行时依赖，签名验证通过依赖注入接入，默认提供基于 `node:crypto` 的实现。

## 信任链模型

校验输入为叶子在前、信任锚在后的 JWT 数组：

1. 叶子实体配置（Entity Configuration），自签：`iss === sub`；
2. 零或多条从属声明（Subordinate Statement），每条由上一级签发，且
   当前条的 `iss` 等于下一条的 `sub`；
3. 信任锚实体配置，自签：`iss === sub`。

签名密钥解析遵循规范：语句 `ES[j]` 的签名用 `ES[j+1].jwks` 中的公钥验证；
叶子与信任锚的实体配置用各自内嵌 `jwks` 自验。

## 校验规则

- **结构与链接**：首尾必须自签；中间不得出现自签语句；相邻语句
  `iss/sub` 必须互锁，任何伪造的 `sub` 或越权签发者都触发 `BROKEN_CHAIN`。
- **有效期**：逐条校验 `iat`/`exp` 窗口，并取所有 `exp` 的最小值作为整条链
  的有效截止时间（结果的 `exp`）。支持注入 `now` 与 `clockSkewSeconds`。
- **路径长度**：每条从属声明的 `max_path_length` 限制它与叶子之间允许的
  从属声明数量；`0` 表示它下方不允许任何中间语句，超长触发 `PATH_TOO_LONG`。
- **metadata_policy 只能收窄**：自锚向下合并，下级的 `subset_of` 必须是上级
  运行集合的子集；`one_of`、`value`、`add`、`default` 也不得逃逸上级
  `subset_of`，违规触发 `POLICY_LOOSENED`。
- **签名验证注入**：调用方提供 `SignatureVerifier`，可对接 HSM/KMS 或任意
  JOSE 库；签名失败统一包装为 `INVALID_SIGNATURE`。

## 安装与导入

```ts
import {
  verifyTrustChain,
  nodeCryptoVerifier,
  TrustChainError,
} from './index.ts';
```

## 用法

```ts
import { verifyTrustChain, nodeCryptoVerifier } from './index.ts';

try {
  const result = await verifyTrustChain(chain, {
    verifier: nodeCryptoVerifier, // 或注入自定义 KMS 验证器
    trustAnchor: 'https://anchor.example',
    leafEntityId: 'https://leaf.example',
    now: Date.now(),
    clockSkewSeconds: 30,
  });

  result.leaf; // 叶子实体标识
  result.trustAnchor; // 信任锚标识
  result.exp; // 全链最小 exp（秒）
  result.combinedMetadataPolicy; // 自锚向下收窄后的合并策略
  result.pathLength; // 从属声明数量
} catch (error) {
  if (error instanceof TrustChainError) {
    console.error(error.code); // 稳定错误码，见 src/types.ts
  }
}
```

### 注入自定义签名验证器

```ts
import type { SignatureVerifier } from './index.ts';

const kmsVerifier: SignatureVerifier = async (context) => {
  // context.jwks 已由校验器按规范解析为正确的签发方密钥集合
  // context.header / context.payload / context.jwt 用于验签
  await myKms.verify(context.jwt, context.jwks);
};
```

## 错误码

`TrustChainError.code` 取值：`EMPTY_CHAIN`、`MALFORMED_JWT`、
`NOT_ENTITY_CONFIGURATION`、`NOT_SUBORDINATE_STATEMENT`、`BROKEN_CHAIN`、
`MISSING_JWKS`、`EXPIRED`、`NOT_YET_VALID`、`PATH_TOO_LONG`、
`POLICY_LOOSENED`、`TRUST_ANCHOR_MISMATCH`、`LEAF_MISMATCH`、
`INVALID_SIGNATURE`。

## 目录

- `src/types.ts`：联邦语句、策略、验证器与结果类型；
- `src/jwt.ts`：紧凑 JWT 解析与 `jwks` 提取；
- `src/errors.ts`：带稳定错误码的异常类型；
- `src/metadata-policy.ts`：`subset_of` 子集判定与策略收窄合并；
- `src/crypto-verifier.ts`：基于 `node:crypto` 的默认验签器（RS/PS/ES/EdDSA）；
- `src/verifier.ts`：`verifyTrustChain` 主编排；
- `test/`：断链、超长、放宽策略、过期等攻击面与正常链测试。

## 测试

```sh
npm test
```

使用 Node 内置 test runner 与 `--experimental-strip-types` 直接运行 TypeScript，
无需编译步骤。覆盖断链、路径超长、策略放宽、过期、签名篡改、密钥注入攻击等
30 个用例，包含 Ed25519 / ES256 / ES384 真实加密验签。
