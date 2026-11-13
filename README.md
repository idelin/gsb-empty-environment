# JWS 密钥来源判定包

JWS 头里的 `jwk`、`jku`、`x5u` 允许令牌自带验证密钥或指向密钥位置，攻击者
可借此让服务用自己的密钥验证伪造令牌。本包供 TypeScript JWT 网关在**验签前**
调用，判定“该用哪把密钥 / 能否抓取”，本身不执行验签。

## 安全语义

- 头里的 `jwk` 与 `x5c` 永远不作为验证密钥：
  - `x5c` 直接忽略；
  - `jwk` 仅用于与登记表中同 `kid` 的公钥比对（RFC 7638 SHA-256 指纹）：
    不同则返回 `forged_jwk` 拒绝；相同也仍只返回登记表里的公钥。
- `kid` 缺失或不在登记表内：拒绝，且不会产生任何网络抓取。
- `alg` 必须在该来源登记的算法集合内，否则拒绝。
- `jku` / `x5u` 仅在以下条件全部满足时才返回 `fetch-url`：
  - 协议为 `https`；
  - 不含 userinfo、查询串、片段，路径不含 `..` 段（含百分号编码形式）；
  - 主机不分大小写地精确等于登记主机，不允许非 443 显式端口；
  - 路径逐字等于登记前缀，或在段边界后继续延伸（`/v1/keys/x` 允许，
    `/v1/keys-evil/x` 拒绝）。
- 抓取（`fetchKeyMaterial`）：`redirect: 'manual'`，任何 3xx/opaqueredirect
  都拒绝；响应体超过 64 KiB 拒绝（流式累计并取消读取）；不携带凭据。
- `jku` 与 `x5u` 同时出现时以 `multiple_key_urls` 拒绝，避免来源歧义。

## 用法

```ts
import { resolveKeySource, fetchKeyMaterial, parseJwkSet } from './index.ts';
import type { KeyRegistry } from './index.ts';

const registry: KeyRegistry = {
  'tenant-a': {
    kid: 'tenant-a',
    publicJwk: { kty: 'EC', crv: 'P-256', x: '...', y: '...' },
    allowedAlgs: ['ES256'],
    allowedHost: 'keys.example.com',
    allowedPathPrefix: '/v1/keys',
  },
};

const decision = await resolveKeySource(protectedHeader, registry);
if (decision.kind === 'reject') {
  // 按伪造/非法令牌拒绝（4xx，不重试），decision.reason 给出原因
  throw new Error(decision.reason);
}

if (decision.kind === 'use-registered-key') {
  const key = await crypto.subtle.importKey('jwk', decision.publicJwk, ...);
  // 用 key 与 decision.alg 验签
} else {
  // decision.kind === 'fetch-url'
  const material = await fetchKeyMaterial(decision.url); // 禁重定向、限 64 KiB
  const jwk = decision.header === 'jku'
    ? parseJwkSet(material, decision.kid)
    : undefined; // x5u 返回原始证书字节，交由调用方的 X.509 校验链处理
}
```

## 模块

- `src/resolver.ts`：`resolveKeySource`，纯判定、无网络请求。
- `src/url-policy.ts`：`checkKeyUrl`，jku/x5u 来源策略。
- `src/thumbprint.ts`：RFC 7638 JWK 公钥指纹。
- `src/fetch.ts`：`fetchKeyMaterial` / `parseJwkSet`，安全抓取与 JWK Set 选取。

## 测试

```sh
npm test
```

覆盖：自带 `jwk`、`jwk` 与登记密钥不同（伪造）、`x5c` 被忽略、未知 `kid`
不抓取、`alg` 门禁、`jku` 指向别的路径、userinfo 绕过、查询串/片段/`..`/
非默认端口/非 https、重定向拒绝、64 KiB 上限等。
