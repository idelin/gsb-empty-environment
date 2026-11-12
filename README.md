# 跨来源打开引用策略

隔离宿主允许受信页面打开外站，但不能泄漏带秘密查询的来源地址。写可导入的TypeScript引用策略组件，输入来源URL、目标URL和显式策略，分别处理同源、跨源及安全到不安全降级；先移除凭据和片段，路径与查询是否保留按策略决定。重定向链逐跳收紧不得放宽，用URL组合穷举核对输出。

## 用法

```ts
import {
  computeReferrer,
  resolveRedirectChain,
  combineReferrerPolicy,
} from './index.ts';

// 单次请求：返回 Referer 头取值，空串表示不发送
computeReferrer(
  'https://user:p@trust.example/app?token=secret#frag',
  'https://other.example/go',
  'strict-origin-when-cross-origin',
);
// => 'https://trust.example/'  凭据、片段被移除，秘密查询不外泄

// 重定向链：逐跳计算策略与 Referer，策略只紧不松
resolveRedirectChain(sourceURL, firstTarget, 'origin', [
  { location: 'https://trust.example/b', referrerPolicy: ['same-origin'] },
  { location: 'http://evil.example/c', referrerPolicy: ['unsafe-url'] },
]);
```

## 导出 API（`referrer-policy.ts`）

- `computeReferrer(sourceURL, targetURL, policy)`：按显式策略计算 Referer，空串策略回退到 `strict-origin-when-cross-origin`，非法令牌抛 `TypeError`。
- `sanitizeReferrerURL(url)`：移除 username/password 与 fragment，保留路径和查询，返回新 `URL`。
- `serializeOrigin(url)`：输出以 `/` 结尾的 origin（省略默认端口）。
- `isSameOrigin(a, b)` / `isDowngrade(source, target)`：同源判定与安全到不安全降级判定（https/wss/file → 其他协议）。
- `combineReferrerPolicy(current, responsePolicies)`：按 Fetch 规范组合响应策略令牌，忽略非法令牌，且只接受严格度更高的策略。
- `resolveRedirectChain(sourceURL, initialTarget, initialPolicy, redirects)`：返回每跳的 `requestURL`、生效 `policy`、`referrer` 与内部 `internalReferrer`。同源重定向才更新内部引用地址，跨源重定向保留原引用。

## 策略语义

| 策略 | 同源 | 跨源 | 安全→不安全降级 |
| --- | --- | --- | --- |
| `no-referrer` | 空 | 空 | 空 |
| `no-referrer-when-downgrade` | 完整地址 | 完整地址 | 空 |
| `strict-origin-when-cross-origin`（默认） | 完整地址 | origin | 空 |
| `same-origin` | 完整地址 | 空 | 空（协议不同即跨源） |
| `origin-when-cross-origin` | 完整地址 | origin | origin（不拦截降级） |
| `strict-origin` | origin | origin | 空 |
| `origin` | origin | origin | origin（不拦截降级） |
| `unsafe-url` | 完整地址 | 完整地址 | 完整地址（不拦截降级） |

完整地址始终先经过凭据与片段净化；origin 形态只含协议+主机+端口，天然不含路径与查询。

重定向严格度从宽松到严格为：
`unsafe-url < origin < strict-origin < origin-when-cross-origin < same-origin < strict-origin-when-cross-origin < no-referrer-when-downgrade < no-referrer`。

## 测试

```sh
npm test
# node --experimental-strip-types --test
```

穷举测试对 128 个来源 URL（协议/主机/端口/路径/查询/凭据/片段组合）× 16 个目标 URL × 8 种策略共 16384 种组合，用独立策略预言机核对输出形态，并断言任何输出都不含凭据、片段，秘密查询仅在同源非降级且策略允许完整地址时出现。
