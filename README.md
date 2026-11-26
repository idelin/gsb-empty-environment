# Cookie 携带决策包（重定向链越权携带防护）

零运行时依赖的 TypeScript 包，为浏览器宿主提供可导入的 Cookie 携带决策引擎与独立
规则核验组件，覆盖 Schemeful Same-Site、SameSite=Lax 与 `LAX-allow-unsafe` 宽限、
CHIPS 分区 Cookie、重定向链跨站闩锁以及 BFCache 冻结恢复。

## 模块结构

- `src/types.ts`：浏览上下文树、祖先来源链、请求/重定向链、Cookie 与决策结果类型。
- `src/merkle.ts`：键排序仅追加 Merkle 树，域分离叶子/内部哈希，包含与缺失证明及独立验证。
- `src/crypto.ts`：SHA-256 适配与 Ed25519 分离签名/验签（基于 `node:crypto`，哈希可注入）。
- `src/psl.ts`：公共后缀规则求值，支持精确、通配（`*.ck`）、例外（`!www.ck`）三类规则。
- `src/rules.ts`：受签 `trustedRulesRoot`、证明束生成器 `RulesProver` 与独立核验器 `RulesVerifier`。
- `src/site.ts`：URL/来源解析、Schemeful 站点键、祖先链同站性与顶层分区键。
- `src/decision.ts`：核心决策引擎 `decideCookieCarry`，含重定向状态机与逐 Cookie 判定。
- `src/bfcache.ts`：BFCache 深冻结快照与原样恢复。
- `src/reference.ts`：无缓存重算参考 `recomputeReference` 与稳定指纹 `decisionFingerprint`。

## 强制安全语义

- **带外根强制**：`trustAnchor` 或证明束缺失即返回 `missing-external-root`，全部 Cookie 拒绝，
  不存在内置规则回退；签名/锚点/叶子总数不符返回 `untrusted-rules-root`。
- **Merkle 证明**：每个主机的候选规则键都必须携带包含或缺失证明，缺失证明含键序邻居与
  `leafCount`，独立验证插入位与空树情形；任何证明失败返回 `invalid-rules-proof`。
- **Schemeful Same-Site**：站点键为 `(scheme, registrableDomain)`，`http` 与 `https` 跨站，
  端口不参与；不透明身份（含无 `allow-same-origin` 的 sandbox）恒按跨站处理。
- **跨站闩锁单调**：链路任一相对初始站点的跨站跳都会置位闩锁，`303` 回到原站点不能洗白，
  也不能重新激活 Lax 资格（`303-reactivation-denied`）。
- **方法演变**：`303` 降级为 `GET`；`307`/`308` 保留方法；`301`/`302` 按历史行为降级。
- **Lax 宽限硬边界**：仅初始请求为顶层导航且不安全方法时进入宽限评估，Cookie 年龄
  `age < 120000ms` 才放行，`120000ms` 整点即 `lax-grace-expired`；子框架请求不享受宽限。
- **CHIPS**：分区 Cookie 必须绑定顶层站点分区键，不透明顶层返回 `opaque-partition`，
  键不符返回 `partition-key-mismatch`。
- **BFCache**：冻结时深拷贝并 `Object.freeze`，恢复时保留原上下文与发起时策略版本；
  暂定导航与策略升版不影响在途请求，受签根策略版本与请求快照不一致即拒绝。

## 宿主接入示例

```ts
import {
  RulesProver, RulesVerifier, decideCookieCarry, generateEd25519TestKey, sha256,
} from './src/index.ts';

const key = generateEd25519TestKey();
const anchor = { anchorId: 'anchor-1', ed25519PublicKey: key.publicKeySpkiDer };
const prover = new RulesProver(['com', 'co.uk', '*.ck', '!www.ck'], sha256);
const signedRoot = prover.signRoot(anchor.anchorId, 'policy-1', Date.now(), key.privateKeyPkcs8Pem);
const bundle = prover.proveBundle(['shop.example', 'evil.test'], signedRoot);

const decision = decideCookieCarry({
  request: {
    initiatedAtMs: 1_000_000,
    method: 'POST',
    url: 'https://shop.example/start',
    ancestorChain: [
      {
        frameId: 'top',
        effectiveOrigin: { opaque: false, scheme: 'https', host: 'shop.example', port: null },
        sandboxed: false,
        allowSameOrigin: false,
      },
    ],
    topLevelNavigation: true,
    hasUserActivation: true,
    redirects: [{ status: 307, location: 'https://evil.test/handoff', atMs: 1_000_100 }],
    policyVersion: 'policy-1',
  },
  cookies: [
    {
      name: 'sid', domain: 'example', path: '/', sameSite: 'lax',
      secure: true, partitioned: false, createdAtMs: 999_900,
    },
  ],
  trustAnchor: anchor,
  proofBundle: bundle,
  nowMs: Date.now(),
});

// 独立核验组件可单独使用，只依赖带外公钥与证明束。
const verifier = new RulesVerifier(anchor, sha256);
const root = verifier.verifyRoot(bundle);
```

## 无缓存重算对照

`recomputeReference(context, secondNowMs)` 以两个不同时刻从零重建验证器与解析缓存，
通过 `decisionFingerprint` 比较除计算时刻外的全部字段。测试矩阵覆盖：

1. 缺失外部根拒绝
2. 303 洗白拦截
3. 跨协议（http→https）降级
4. sandbox 不透明身份与 `allow-same-origin` 切换
5. Lax 宽限 `119999ms / 120000ms / 120001ms` 临界点

## 运行测试

```bash
npm test
```

Node 23 原生执行 TypeScript（`--experimental-transform-types`），无需构建步骤；
`tsconfig.json` 开启 `strict` 与 `noUnusedLocals` 等严格选项，宿主可用 `tsc --noEmit` 复核类型。
