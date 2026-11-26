# 租户迁移广播头部验收包

这是一个可导入的 TypeScript 广播宿主验收库。它不持有、输出或恢复任何密钥，只对
**封装组、设备能力、签名桥接、撤销区间和双来源部分封装**做授权分类与风险判定，
并输出可被独立验收函数重算的分区和证书。

## 安全模型

- 每个封装组绑定永久租户 `tenant`、内容代次 `contentGeneration`、密钥代次
  `keyGeneration`、用途 `purpose` 和 48 位设备区间。
- 设备可以同时保留多个能力（不同密钥代次、不同用途、不同封装组）。
- 直接读取要求租户、内容代次、密钥代次、用途和设备区间全部匹配。
- 桥接声明必须由权威方签名，并且只允许其明确写出的
  `tenant + interval + purpose + keyGeneration` 范围内的旧能力参与目标轮次。
  使用名称、成员列表、通配符或缺失作用域的声明会被拒绝。
- 一条消息可以要求 `threshold` 份来自**不同物理设备**的部分封装。同一物理设备的
  多个别名只计一个来源。
- 撤销使用符号化区间表示，支持 48 位全域加稀疏例外，不需要枚举设备。
- 被撤销设备如果通过直接能力、签名桥接或联合同伴获得读取权，会进入风险判定。
  同一个物理设备的两个别名不会被当成两个联合作者。

## 导入

```ts
import {
  analyzeAcceptance,
  verifyAcceptance,
  signBridge,
  evaluateDomain,
  exhaustiveCheck,
} from "./src/index.ts";
```

也可以从根目录的 `index.ts`、`header.ts`、`acceptance.ts` 或
`header-acceptance.ts` 导入。`package.json` 同时提供 `"."`、`"./header"`、
`"./acceptance"`、`"./header-acceptance"` 子路径。

## 最小示例

```ts
const authority = "authority-signing-secret";

const policy = {
  bridgeSecret: authority,
  groups: [
    {
      groupId: "old",
      tenant: "tenant-a",
      contentGeneration: 1,
      keyGeneration: 0,
      purpose: "content",
      interval: [0, 999],
    },
    {
      groupId: "new",
      tenant: "tenant-a",
      contentGeneration: 1,
      keyGeneration: 1,
      purpose: "content",
      interval: [0, 499],
    },
  ],
  devices: [
    { id: 10, groups: ["old"] },          // 未迁移，只留旧能力
    { id: 75, groups: ["new"] },          // 已迁移，持当前能力
    {
      id: 20,
      physicalId: "phone-20",
      aliases: ["left", "right"],
      groups: ["old"],
    },
  ],
  bridges: [
    signBridge(
      {
        bridgeId: "bridge-old-subset",
        tenant: "tenant-a",
        contentGeneration: 1,
        keyGeneration: 0,
        purpose: "content",
        interval: [0, 99],
      },
      authority,
    ),
  ],
  revoked: { start: 90, end: 94 },
  message: {
    tenant: "tenant-a",
    contentGeneration: 1,
    keyGeneration: 1,
    purpose: "content",
    threshold: 1,
  },
};

const result = analyzeAcceptance(policy);
console.log(result.decisions);      // 每个设备/类别：authorized / bridged / partial / revoked / denied
console.log(result.risk);           // safe / elevated / unsafe + 具体风险码
console.log(result.partitions);     // 覆盖 [0, 2^48) 的规范化分区
console.log(verifyAcceptance(result, policy)); // 独立重算并校验证书
```

## 输入要点

### 设备与能力

- `devices[].id` 是 48 位编号，接受 `bigint`、安全的 `number` 或十进制/`0x` 字符串。
- `aliases` 是同一物理设备的别名；`physicalId` 相同或 `id` 相同会被合并。
- `capabilities` / `keys` 显式声明设备保留的能力。未显式声明时，设备会从覆盖其编号的
  封装组继承能力；可用 `deriveCapabilities: false` 关闭。
- 每个能力都可以携带自己的 `interval`、`tenant`、`contentGeneration`、
  `keyGeneration` 和 `purpose`。

### 桥接

```ts
const declaration = signBridge(
  {
    bridgeId: "b1",
    tenant: "tenant-a",
    contentGeneration: 1,   // 目标轮次；省略会标记为 round-unbound
    keyGeneration: 0,       // 必须是旧代次，且严格小于本轮代次
    purpose: "content",
    interval: [0, 99],      // 必须是具体区间
  },
  secret,
);
```

`signBridge` 使用 HMAC-SHA-256，签名载荷绑定 `bridgeId`、租户、内容代次、密钥代次、
用途和区间。也支持 Ed25519 公钥验证与自定义 `verifyBridgeSignature` 回调。
未签名、签名不匹配、代次不旧、缺少区间/用途/代次、名称/成员捷径和通配符声明都不会
授权，并会在 `certificate.bridges.rejected` 与 `risk.findings` 中留下代码。

### 双来源

消息的 `threshold: 2` 表示需要两个不同物理设备来源。一个设备保留两份能力仍只算
一个来源；同一物理设备的两个别名也只算一个来源。可使用：

```ts
const shares = evaluateShares(policy, [
  { source: "left" },
  { source: "right" },
]);
// shares.distinctSources === 1, shares.canOpen === false
```

### 撤销与稀疏例外

撤销可以写成区间、区间列表或带例外的区间：

```ts
revoked: {
  start: 0,
  end: (1n << 48n) - 1n,
  exceptions: [{ start: 10, end: 19 }],
}
```

所有区间运算都是符号化的，`rangeSize` 用 `bigint` 算术，不展开 48 位空间。

## 输出

- `decisions`：每个物理设备的能力来源计数、是否直接/桥接受权、单独读取结果和
  授权类 `authorized | bridged | partial | revoked | denied`。
- `classes`：按 `deviceClasses` 或设备 `className` 聚合的类别判定。`threshold > 1`
  时，一个类别中的多个不同物理设备可以共同满足消息。
- `coalitions`：显式 `allowedCoalitions` 以及小设备域上的被撤销设备组合判定。
  字段包括 `distinctPhysicalSources`、`eligibleSources`、`newReadRights`、
  `aliasCollapsed`、`revokedRisk`。
- `partitions`：覆盖完整 48 位域的规范化单元，包含撤销状态、直接组、直接能力、
  桥接能力、点设备和授权类。
- `certificate`：分区/决策/风险/策略/桥接摘要，`manifest`（无权威密钥），边界见证，
  覆盖率与桥接接受/拒绝审计。
- `risk`：`safe | elevated | unsafe`，以及 `revokedDirectRead`、
  `revokedCoalitionEscalation`、`revokedBridgeRead`、`bridgeOverreach`、
  `unsignedBridge`、`aliasDoubleCount`、`generationConfusion`、`boundaryLeak`。

输出中不包含签名密钥、桥接密钥、设备密钥字节或任何解密/恢复操作。

## 独立验收

```ts
const check = verifyAcceptance(result, policy); // 重新审计签名、重算分区与风险
const replay = recomputeAcceptance(result);     // 仅使用证书 manifest + 已接受桥接 ID 重算
```

`verifyAcceptance` 会检查：

- 证书 manifest 与 `policyDigest` 一致；
- 分区完整、无重叠/空洞，边界见证与撤销区间一致；
- 分区、决策、风险、桥接摘要可由 manifest 或原始 policy 重算一致；
- 被篡改的分区授权类、证书阈值、策略 manifest 会失败。

独立验收函数只依赖导出类型和证书中的公开 manifest，不需要调用宿主私有状态。

## 小设备域穷举

```ts
const domain = evaluateDomain(policy, { start: 0, end: 63 });
// domain.ids、domain.summary、domain.partitionMismatches

const audit = exhaustiveCheck(policy, {
  start: 0,
  end: 63,
  coalitionMaxSize: 2,
});
// audit.ok、audit.escalationCases
```

`evaluateDomain` 默认最多物化 65536 个编号；对完整 48 位域会拒绝枚举。测试覆盖：

- 正常读取范围；
- 同样区间、不同密钥代次；
- 局部迁移；
- 未签名/未获准的桥接；
- 别名折叠；
- 被撤销方联合能力；
- 恰好在撤销边界的一台设备；
- 分区与证书篡改；
- 输入顺序变化时的证书确定性。

运行：

```sh
npm test
```
