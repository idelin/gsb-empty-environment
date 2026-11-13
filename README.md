# PrivateToken 挑战处理包（RFC 9577）

可导入的 TypeScript 浏览器网络栈组件，用于处理 `WWW-Authenticate: PrivateToken`
挑战，重点防御“嵌入页把挑战转发到别的来源、换取可跨站复用令牌”的来源混淆攻击。

## 安全模型

`TokenChallenge` 按 RFC 9577 定义为四元素 CBOR 数组：

1. `token_type`：CBOR 无符号整数；
2. `issuer_name`：非空文本字符串；
3. `redemption_context`：CBOR 字节串，长度必须为 `0` 或 `32`；
4. `origin_info`：文本字符串数组，每项为 `https` 来源。

来源核对规则（RFC 9577 §2.1）：

- `origin_info` **非空**：当前请求来源必须出现在该列表中，否则拒绝；
- `origin_info` **为空**：只允许顶层导航请求，且请求来源必须与顶层文档同源；
  嵌入（子资源、iframe 等）请求一律拒绝。

只有 `https` 来源被接受；来源比较在 `scheme://host[:port]` 层面进行，
默认端口的显式写法（如 `:443`）会被规范化。

## 字节一致性与摘要

- 解析时强制 **确定性（canonical）CBOR**：拒绝非最短整数编码、不定长编码、
  不支持的主要类型与尾随字节；
- 解析后立即重编码并与原始字节逐字节比较，不一致即拒绝，保证
  `encodeTokenChallenge(parseTokenChallenge(raw))` 与 `raw` 完全相同；
- `challenge_digest = SHA-256(0x0001 || 原始 TokenChallenge 字节)`，
  其中 `0x0001` 是 PrivateToken 协议在 Privacy Pass Token Type 注册表中的上下文字段；
  同时返回原始字节与十六进制字符串。

## 用法

```ts
import {
  handlePrivateTokenChallenge,
  parseTokenChallengeBase64Url,
  computeChallengeDigest,
} from "./index.ts";

const { challenge, digest } = await handlePrivateTokenChallenge(
  wwwAuthenticateHeader,
  {
    requestOrigin: "https://embedded.example",
    topLevelOrigin: "https://top.example",
    isTopLevelNavigation: false,
  },
);

console.log(challenge.tokenType, challenge.issuerName);
console.log(digest.hex);

// 也可分步使用
const parsed = parseTokenChallengeBase64Url(challengeParam);
const digestAgain = await computeChallengeDigest(parsed);
```

## 主要导出

- `handlePrivateTokenChallenge(header, context)`：头解析 → 结构解析 → 来源核对 → 摘要，一体化入口；
- `parsePrivateTokenChallengeHeader` / `parseTokenChallenge` / `parseTokenChallengeBase64Url`：分层解析；
- `verifyChallengeOrigin(originInfo, context)`：单独执行来源策略；
- `encodeTokenChallenge` / `encodeTokenChallengeBase64Url`：确定性重编码；
- `computeChallengeDigest`：计算 `challenge_digest`；
- `parseWwwAuthenticate` / `findPrivateTokenChallenge`：RFC 9110 认证头解析；
- `ChallengeParseError`、`OriginVerificationError`：区分解析失败与来源核对失败。

## 结构

- `src/private-token/cbor.ts`：零依赖确定性 CBOR 读写器；
- `src/private-token/base64url.ts`：无填充 base64url 传输编码；
- `src/private-token/origin.ts`：https 来源解析与跨站核对；
- `src/private-token/header.ts`：`WWW-Authenticate` 词法解析；
- `src/private-token/challenge.ts`：TokenChallenge 解析、重编码与摘要。

## 测试

```sh
npm test
```

使用 Node 内置类型擦除与 `node:test`，无需外部依赖；覆盖编解码往返、
非规范字节拒绝、来源允许/拒绝路径、头词法边界与端到端摘要核对。
