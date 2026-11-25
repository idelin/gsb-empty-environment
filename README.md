# OpenPGP 独立信任验收包

这是一个可直接导入 TypeScript 密钥环宿主的验收库，用来在组织更换 OpenPGP 介绍人后判断：给定用户标识与时刻，还剩多少**彼此独立**的信任；以及某个邮箱域后缀内，哪些邮箱子语言仍满足阈值。

实现是无依赖的 ESM TypeScript，使用 Node 的类型剥离直接运行：

```ts
import {
  evaluateTrust,
  symbolicQuery,
  verifyDomainCertificate,
} from "./src/index.ts";
```

## 核心语义

### 快照

```ts
interface KeyringSnapshot {
  id?: string;
  asOf?: string | number | Date;
  keys: KeyRecord[];                 // 实际密钥材料指纹 + 别名
  userIds: UserIdRecord[];           // 用户 ID / 邮箱 -> 密钥材料
  signatures: TrustSignature[];      // 信任签名边
  revocations?: RevocationCertificate[];
  roots?: string[];                  // 信任锚；缺省用 trustAnchor 或无入边密钥
  evidence?: string[];               // 快照中已有的附加签名/证据 ID
  requiredEvidence?: string[];       // 整个快照必须存在的证据
  missingSignatures?: string[];
  missingEvidence?: string[];
  revocationConflicts?: string[];    // 已知未解决冲突
}
```

`KeyRecord.materialFingerprint` 是实际密钥材料的规范指纹；`aliases` 中的多个指纹都归一到同一材料。若同一个别名指向不同材料，会变成 `unknown`，而不是任取一个。`UserIdRecord.key` 可以写材料指纹或别名，因此同一把密钥的多个用户 ID 不会形成多张票。

### 信任签名

```ts
interface TrustSignature {
  id: string;
  from: string;                      // 发送方材料指纹/别名
  to: string;                        // 接收方材料指纹/别名
  issuedAt?: Instant;                // 签名签发时刻（也是可用起点）
  notBefore?: Instant;
  notAfter?: Instant;                // 排他上界
  depth: number;                     // 仍可再委托的层数
  weight: number;                    // 信任权重
  domains: DomainLanguageSpec;       // 允许邮箱域的正规语言
  adminDomain?: string;              // 该跳的中间管理域
  revocationPredecessors?: string[]; // 可撤销该跳的前置节点
  requires?: string[];               // 该边需要的附加签名/证据
}
```

`domains` 支持字符串正则、`RegExp`、`{ regex, flags }` 或序列化 DFA。默认按大小写不敏感处理域。正则被编译成 DFA，因此交、并、补、包含、空语言和等价判断都是精确的，不依赖抽样。

链上语义：

- 语言：所有边的域语言取**交集**。路径只接受交集中的域。
- 截止：所有边的 `notAfter` 与已知撤销生效时刻取**最早**。
- 权重：整条链取**最弱边** `min(weight)`。
- 深度：每跳递减。根到目标的第一个签名给出剩余额度，后续 `min(上一跳剩余 - 1, 本边 depth)`；非末跳剩余必须至少为 1，末跳可以 depth=0。
- 环：只枚举简单路径（节点不重复），环不会被重复遍历，也不能通过绕圈增加权重。

### 撤销

撤销证书同时带 `issuedAt` 与 `effectiveAt`：

- 查询时刻早于 `issuedAt`：该撤销还不可见，边仍有效。
- `issuedAt <= t < effectiveAt`：撤销已可见但尚未生效，边仍有效，链的截止时间被截到 `effectiveAt`。
- `t >= effectiveAt`：边被截断。
- `status: "unresolved" | "conflict"` 且已可见：结果报告 `unknown`。
- `status: "withdrawn"` 且 `supersedes` 指向某撤销：被指向的撤销不计入。

因此签发时刻和生效时刻都会参与判断；撤销不会穿透到签发前或生效前的查询。

### 独立背书

两条链同时计票必须满足：

- 不共享中间管理域。路径的中间管理域来自非末跳的 `adminDomain`，以及中间密钥的 `KeyRecord.adminDomain`（未显式给出时回退到该中间密钥绑定邮箱的域）；根和目标本身不计入。
- 不共享撤销前置节点。路径的 `revocationPredecessors` 会去掉根与目标，再求交。
- 不重复使用同一条签名边。

同一实际密钥的多个用户 ID 会归一到同一个材料指纹，不能重复计票。`maxIndependentBatchWeight` 会把同一材料的多个邮箱合并为一个目标密钥，再把所有候选简单路径放进同一个冲突图求精确最大权重独立集。

### 最大独立权重

`maxIndependentEndorsements` 对候选路径建立冲突图，按连通分量做精确分支定界/记忆化搜索，返回最大总权重和选中的路径。它不会“先给每个目标找最强路径再删重复域”，因为那样会错过类似下面的情况：

- 最强路径 10 与另一条 9、另一条 9 分别共享域；
- 放弃 10 后，两条 9 与两条 8 可以同时选用，总权重 34。

候选数超过 60 时返回 `overflow`，验收结果为 `unknown`，不会悄悄退化成贪心。

## 未知（unknown）

以下情况不会猜成 `untrusted`，而是 `unknown`：

- 快照缺 `requiredEvidence`、`missingSignatures` / `missingEvidence` 指定的内容；
- 某个相关路径的 `requires` 在快照证据中缺失；
- 相关边上存在已可见但未解决的撤销冲突；
- 密钥别名冲突、未知密钥引用；
- 枚举路径或候选独立集达到上限；
- 目标邮箱同时绑定到多个不同材料指纹。

明确无路径、全部路径过期/撤销/深度不足/语言不匹配，则报告 `untrusted`。

## 公开 API

主要入口：

| API | 作用 |
| --- | --- |
| `evaluateTrust(snapshot, { target, at, threshold?, roots? })` | 单个邮箱/用户 ID/材料指纹的验收结果 |
| `evaluateBatch(snapshot, requests)` | 批量逐项结果 |
| `maxIndependentBatchWeight(snapshot, requests)` | 跨请求的全局最大独立权重；同材料用户 ID 只算一次 |
| `maxIndependentTotalWeight(snapshot, requests)` | 只返回上述总权重（未知为 `null`） |
| `enumerateSimplePaths(snapshot, { targetKey, at, ... })` | 枚举全部简单路径及链证据 |
| `enumerateAllSimplePaths(snapshot, { at, ... })` | 枚举到所有可达密钥的全部简单路径 |
| `enumerateCompatibleEndorsementSets(paths, { maximalOnly? })` | 枚举全部相容背书集合 |
| `maxIndependentEndorsements(paths)` | 求最大权重独立集 |
| `symbolicQuery(snapshot, { suffix, at, threshold? })` | 后缀内子语言查询、路径证据、证书 |
| `verifyDomainCertificate(snapshot, certificate)` | 独立重算并核验单链证书 |
| `verifyIndependenceCertificate(snapshot, certificate)` | 独立重算并核验独立域证书 |
| `compileDomainLanguage` / `dfaIntersection` / `dfaSubset` / `dfaDigest` | 正规语言工具 |

`TrustEvaluation` 的关键字段：

- `status: "trusted" | "untrusted" | "unknown"`
- `maxWeight`: 未知时为 `null`，否则为精确最大独立权重
- `lowerBoundWeight`: 已证明可达到的权重（未知时仍可参考）
- `selected`: 选中的独立路径
- `certificates` / `independenceCertificate`: 可直接交给独立验收器的证书
- `candidates`: 所有枚举出的候选路径（含无效原因）
- `conflicts`: 有效候选之间的冲突及原因
- `unknownReasons`: `unknown` 的具体原因

## 符号查询

```ts
const result = symbolicQuery(snapshot, {
  suffix: "example.com",   // 也接受 "@example.com"
  at: "2026-01-03T00:00:00Z",
  threshold: 18,
});

for (const match of result.qualified) {
  for (const sub of match.sublanguages) {
    console.log(sub.pathId, sub.sampleDomains, sub.languageDigest);
    verifyDomainCertificate(snapshot, sub.certificate);
  }
  verifyIndependenceCertificate(snapshot, match.independenceCertificate!);
}
```

`match.acceptedLanguage` 选中路径语言与后缀语言的并集；`match.coversSuffix` 表示这个并集是否完整覆盖后缀语言。`sublanguages` 中每条子语言都包含：

- 规范化 DFA 与其摘要（正规语言交集）；
- 路径 `edgeIds`；
- 样例域/邮箱；
- 可由独立验收器重算的 `DomainCertificate`。

独立验收器不信任证书字段：它会重新解析快照、重跑链语言交集、深度、撤销截止与独立域集合，再与证书摘要和 DFA 等价性比对。篡改权重、语言、路径或撤销集都会失败。

## 运行

```bash
npm test       # 小图验收测试
npm run accept # 可执行验收场景，输出 JSON 摘要
```

测试覆盖：撤销签发/生效边界、两把别名密钥、正则域交叠、深度与阈值边界、环、共享管理域、共享撤销前置节点、别名用户 ID 不重复计票、贪心反例、离线未知与证书篡改。

实现约定：时间统一为毫秒数（ISO 字符串/Date/number 均可输入）；有效区间是 `[validFrom, notAfter)`；撤销在 `effectiveAt` 当刻生效；域匹配前会转小写并去掉末尾点。路径上限默认 20000，候选独立集精确求解上限 60。
