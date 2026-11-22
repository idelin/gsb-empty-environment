# Effective Policy Resolver

TypeScript host 可直接导入的生效策略解析包。它把多个签名安全基线、受限豁免、
路径范围和资源身份解析为可审计的有效约束，并对一次变更给出按资产语言区域划分的
收紧、放宽、保持、冲突和缺证明未知结果。

运行时使用 Node.js 原生 type stripping，无第三方依赖：

```bash
npm test
```

```ts
import {
  resolveEffectivePolicy,
  analyzePolicyChange,
} from "ddd-2071-20261122-v2";
```

## 身份与证据

`ResourceIdentity` 的等价性由以下内容共同决定：

- 已签名的租户根 `tenantRoot`（含 `tenantId`、`rootId`、公钥与签名）；
- 不可变的 `resourceId`；
- 当前 `path` 与不可变的 `pathVersion`。

资源拆分、改名或旧路径被新资源复用时，会得到新的身份摘要。豁免范围不是一条
路径正则：它是“路径段 DFA + 租户根签名的资产集合 Merkle 根 + 成员证明”。
因此旧豁免不会因为文字或路径相同就自动覆盖新资源。

默认只验证签名自洽；生产宿主应同时用 `TrustOptions` 固定可信租户根和可信策略
来源公钥指纹（`trustedRootFingerprints`、`trustedSourceFingerprints`，并设置
对应的 `requireTrusted*`）。

```ts
import {
  createAssetRoot,
  createMembershipProof,
  createTenantRoot,
  resourceIdentity,
} from "ddd-2071-20261122-v2";

const tenant = createTenantRoot({ tenantId: "tenant-a", issuedAt: 0 });
const asset = resourceIdentity(tenant.root, "asset-1", ["prod", "a"], 1);
const assetRoot = createAssetRoot({
  tenantRoot: tenant.root,
  tenantPrivateKey: tenant.privateKey,
  identities: [asset],
  setId: "release-42",
  issuedAt: 0,
});
const proof = createMembershipProof([asset], asset, "release-42");
```

## 继承与约束

继承图允许任意有向边、多个父节点和有向环。每个节点声明若干约束：

```ts
import {
  createPolicySource,
  generateSigningKeyPair,
} from "ddd-2071-20261122-v2";

const signer = generateSigningKeyPair();
const securitySource = createPolicySource({
  id: "security-baseline",
  signerId: "platform-security",
  keyPair: signer,
  issuedAt: 0,
});
const cryptoSource = createPolicySource({
  id: "crypto-baseline",
  signerId: "platform-crypto",
  keyPair: signer,
  issuedAt: 0,
});

const graph = {
  nodes: [
    { id: "asset", parents: ["security", "crypto"] },
    {
      id: "security",
      declarations: [{
        id: "tls",
        key: "tls.version",
        allowed: ["1.2", "1.3"],
        literal: "TLS >= 1.2",
        source: securitySource,
      }],
    },
    {
      id: "crypto",
      declarations: [{
        id: "tls",
        key: "tls.version",
        allowed: ["1.2"],
        literal: "TLS >= 1.2",
        source: cryptoSource,
      }],
    },
  ],
};

const effective = resolveEffectivePolicy({
  state: { graph, at: 1_760_000_000_000, waivers: [] },
  target: "asset",
  identity: asset,
});
```

同一 `key` 的有效值集合是各来源 `allowed` 集合的交集。交集为空时结果保留为
`status: "conflict"` 且 `allowed: []`，绝不回退到默认值。不存在任何来源的 key
才是“未约束”，与“冲突后取默认”严格区分。

同一文字约束来自两个签名来源时，`origins` 会分别保留，不能合并成一条证据。

## 豁免

豁免由签名来源签发，只能点名取消该来源的一项约束。签发时的 `scope` 固定路径
语言和资产集合承诺；请求或豁免本身必须提供该身份的成员证明。

```ts
import { createWaiver, prefixDFA } from "ddd-2071-20261122-v2";

const waiver = createWaiver({
  id: "tls-exception-17",
  issuer: securitySource,
  issuerPrivateKey: signer.privateKey,
  target: { sourceId: "security-baseline", constraintId: "tls" },
  scope: {
    path: prefixDFA(["prod"]),
    assets: assetRoot,
    proofs: [proof],
  },
  issuedAt: 1_760_000_000_000,
  expiresAt: 1_770_000_000_000,
});
```

时间边界固定为 `[issuedAt, expiresAt)`：签发时刻生效，到期时刻失效。泛型的
`TimeInterval` 也支持显式设置 `startInclusive` 与 `endInclusive`。

如果豁免范围命中候选路径但缺少成员证明，解析结果为
`status: "unknown"`，约束不会被取消；这正是变更分析中的“仅因缺证明而未知”。

## 变更分析

`analyzePolicyChange` 对前后策略逐资产解析，输出互不相交的精确路径 DFA 区域：

- `tightened`：至少一项约束允许集合变小；
- `loosened`：至少一项约束允许集合变大；
- `unchanged`：所有同项约束集合相同；
- `mixed`：同时存在不可比较的收紧和放宽；
- `conflict`：交集为空；
- `unknown`：证据不足，`unknownOnlyForProof` 标明是否全部由缺少成员证明造成。

`deltas` 给出逐项关系；`chains` 只包含已重新逐边验收的简单继承来源链。
`regions[].path` 是该区域的符号语言，`regions[].paths` 可用于小图穷举对照。

```ts
const analysis = analyzePolicyChange({
  before: beforeState,
  after: afterState,
  assets: [{
    id: asset.resourceId,
    before: { identity: asset, target: "asset" },
    after: { identity: renamedAsset, target: "asset" },
  }],
});
```

## 环上的传播

解析器使用单调工作队列求不动点，而不是 DFS 的“节点已访问”剪枝。每次重新计算
节点都会纳入其全部父节点的最新状态，因此：

- 无新增约束的环会稳定在空增量的结果；
- 约束只会收缩，来源支撑只会增加，终止性由有限集合和有限来源保证；
- 同一来源经多条继承路径到达目标时，简单来源链会全部保留并分别验收；
- 回访某个节点不会掩盖另一条仍有效的支撑。

## 主要 API

- 解析：`resolveEffectivePolicy` / `resolvePolicy` / `computeEffectivePolicy`
- 差分：`analyzePolicyChange` / `diffPolicies`
- 继承证据：`validateProvenanceChain`
- 路径语言：`exactPathDFA`、`prefixDFA`、`intersectDFA`、`differenceDFA`、
  `complementDFA`、`enumerateDFA`
- 签名与成员证明：`createTenantRoot`、`createPolicySource`、`createWaiver`、
  `createAssetRoot`、`createMembershipProof`、`verifyMembershipProof`
- 时间边界：`intervalContains`、`intersectIntervals`、`validBetween`
