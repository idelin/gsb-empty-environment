# 地址栏 Unicode 展示决策包

这是一个可导入的 TypeScript 包，用于决定地址栏中的每个标签应以 Unicode
原形展示，还是退回该标签的 ASCII（A-label）形式。它把下面几件事放在同一个
可审计的决策里：

- 两版脚本归类 / 混淆转写表；
- 可独立更新的后缀名表；
- 签过名的品牌别名；
- 有限自动机描述的备选域名；
- UTF-16 安全的标签位置；
- 可独立复核的表摘要、允许范围和逐标签决策。

包的入口是 `src/index.ts`，`package.json` 已导出该入口。运行时要求支持
TypeScript type stripping 的 Node，或由宿主自己的打包器编译。

```bash
npm test
```

## 快速使用

```ts
import {
  dfaFromWords,
  decideDisplay,
  signAlias,
  verifyDecision,
} from "./src/index.ts";
import type { DisplayRequest } from "./src/index.ts";

const aliases = [
  signAlias(
    {
      domain: "аbc.com",          // 西里尔字母 а
      brand: "Example",
      suffix: "com",
      keyId: "brand-key-1",
      tableVersion: "v1",          // 仅在旧表下成立
    },
    "host-secret",
  ),
];

const request = {
  labels: ["аbc", "com"],
  sourceText: "аbc.com",
  protectedDomain: "abc.com",
  brand: "Example",
  versionOrder: ["v2", "v1"],
  suffixVersion: "s1",
  tables: {
    confusables: { v1, v2 },
    scripts: { v1: scriptsV1, v2: scriptsV2 },
    suffixes: { s1: suffixTable },
  },
  aliases,
  keyring: { "brand-key-1": "host-secret" },
  automaton: dfaFromWords(["аbc.com", "αbc.com"]),
} satisfies DisplayRequest;

const decision = decideDisplay(request);

console.log(decision.text);       // 可能是 "аbc.com" 或 "xn--…"
console.log(decision.labels[0]);  // mode / span / scalars / reason

const report = verifyDecision(decision, request);
if (!report.ok) throw new Error(report.errors.join("\n"));
```

## 核心语义

### 1. 转写规则

每条 `ConfusableRule`：

- `consumes`：1–3 个 Unicode 标量；
- `skeleton`：0–3 个骨架符号；
- 匹配采用最长匹配；同长度按声明顺序，先声明者优先；
- 未匹配的标量以自身作为骨架符号；
- 域骨架会把标签用 `.` 连接后整体流式转写，因此规则可以显式消费 `.`。
  `skeletonLabel()` 仍可用于只转写单个标签。

`createConfusableMatcher` 支持流式输入：

```ts
const matcher = createConfusableMatcher(table);
matcher.push("a");     // 可能先不输出，保留最多两个待决标量
matcher.push("bc");    // 跨块完成三标量规则
matcher.end();         // 冲刷待决后缀；不会吞掉未匹配标量
```

`push` 也接受 Unicode 标量数组。字符串块如果正好切在代理对中间，会在下一次
`push` 时拼接；`end()` 遇到孤立代理项会抛错。

### 2. 脚本策略

`ScriptRange.extensions` 表示 Unicode `Script_Extensions`。对于
`singleScript: true` 的策略，每个字符的可选脚本集取交集；例如
`{Latin, Greek}` 的字符与希腊字符可以共同落为 Greek，但
`{Latin, Greek}` 与 Cyrillic 不能落为同一脚本。

`ScriptPolicy` 字段：

- `allowed`：后缀允许的脚本；
- `singleScript`：默认 `true`；
- `disallowed`、`minScripts`、`maxScripts`；
- `allowNeutralOnly`：只有 Common/Inherited 时的结果，默认 `true`。

### 3. 后缀表

后缀表与混淆表分开版本化。`SuffixRule` 支持 PSL 风格的：

- 普通规则：`com`、`co.uk`；
- 通配规则：`ck` + `kind: "wildcard"` 或 `*.ck`；
- 例外规则：`www.ck` + `kind: "exception"` 或 `!www.ck`。

例外规则优先；否则最长匹配。`effectiveSuffix()` 同时返回公共后缀、可注册
标签和该规则适用的脚本策略。

### 4. Unicode 允许条件

对某个版本 `v`，一个标签只有在下面两条同时成立时才展示 Unicode：

1. 该标签满足当前后缀的脚本策略；
2. 在版本 `v` 下，受保护注册域的骨架与备选域集合中每个同骨架允许域，都能
   找到适用于当前后缀和品牌身份的别名证明。

备选域由 `ScalarAutomaton`（确定性有限自动机）描述。决策代码做的是
“自动机 × 流式骨架匹配器 × 别名域 trie”的乘积搜索，不会枚举表内全部名字。
搜索只考虑可能产生目标骨架的标量：规则消费的标量和目标骨架中的标量。
候选长度上限固定为 64 个标量（含标签分隔符）。

自动机是“备选域”集合。如果它包含受保护域本身，那么受保护域也需要别名证明；
如果它只列其他域，则只需证明这些备选域。省略 `automaton` 表示没有备选域。
默认还会把不满足当前后缀脚本策略的候选域排除在“允许域”之外；如果宿主给出的
自动机已经是完整的允许集合，可以设 `filterCandidatesByScriptPolicy: false`。
`protectedDomain`、自动机接受的词和别名 `domain` 应使用同一种文本形式
（统一用 U-label 或统一用 A-label），否则它们不会被视为同一个域。

### 5. 版本选择与 ASCII 回退

- `decideDisplay()` 默认只承认 `versionOrder[0]`（通常是新表）。证明失败时，
  非 ASCII 标签退回 A-label。
- `versionFallback: true` 时才允许按 `versionOrder` 继续尝试旧版本。
- `decideForVersion(request, "v1")` 用于收藏夹等明确绑定到旧表的场景，避免把
  旧站点和新域名混为一谈。
- `decideAllVersions(request)` 返回每个版本的独立决策，便于宿主按收藏来源选择。
- 脚本策略失败只影响该标签；某个标签可以 ASCII 回退，而其他标签仍展示
  Unicode。

### 6. 品牌别名与签名

`SignedBrandAlias` 明确绑定：

- `domain`：别名域（与自动机输出使用同一种文本形式）；
- `brand`：品牌身份；
- `suffix`：适用后缀，`*` 表示任意；
- `tableVersion` / `suffixVersion`：可选，限制证明只在指定表版本成立；
- `notBefore` / `notAfter`：可选时间窗，单位是 Unix epoch 毫秒；
- `keyId`、`alg`、`signature`。

默认实现是 `hmac-sha256`，签名覆盖 `aliasPayload()` 的规范 JSON。

```ts
const verifier = createHmacAliasVerifier({ "brand-key-1": "host-secret" });
const alias = signAlias(draft, "host-secret");
verifyAliasSignature(alias, "host-secret"); // true
```

生产环境可以注入 `verifier` 做平台签名校验。`verified: true` 是给
“宿主已经异步验证过”的受信任别名用的逃生口，不能直接接受不可信输入。

### 7. 输出位置

`LabelDecision` 同时给出：

- `label`：实际标签文本；
- `span`：在宿主文本中的 UTF-16 半开区间；
- `scalars`：每个 Unicode 标量对应的 UTF-16 区间；
- `mode`：`"unicode"` 或 `"ascii"`；
- `displayText`：实际应显示的文本；
- `reason`：`proved`、`script-policy`、`no-proof`、`search-truncated`
  或 `ascii-input`。

扫描用 `for..of` 保留代理对，任何区间边界都不会落在高/低代理项之间。
`span` 始终指向宿主原文中的实际标签，而不是 `decision.text` 里重建的文本。

## API 一览

| 功能 | 入口 |
| --- | --- |
| 主决策 | `decideDisplay`, `decideForVersion`, `decideAllVersions`, `evaluateVersionAttempt` |
| 标签位置 | `resolveLabels`, `scalarSpans`, `toAsciiLabel` |
| 转写 | `createConfusableProgram`, `createConfusableMatcher`, `transcribe`, `skeletonDomain` |
| 脚本 | `scriptsFromRanges`, `scriptPolicyHolds`, `effectiveScripts` |
| 后缀 | `effectiveSuffix`, `parseSuffixRule`, `suffixApplies` |
| 自动机 | `dfaFromWords`, `searchCandidates`, `enumerateAcceptedWords` |
| 别名 | `signAlias`, `verifyAliasSignature`, `createHmacAliasVerifier`, `aliasApplies` |
| 表摘要 | `summarizeTables`, `tableDigestsFor`, `verifyTableSummary` |
| 独立检查 | `verifyDecision`, `checkAllowedRange` |
| 穷举自检 | `runExhaustiveChecks` 及各个 `check*` 函数 |

## 独立检查器

`verifyDecision(decision, request)` 会：

- 重算混淆表、脚本表、后缀表及组合摘要；
- 验证每个标签的 UTF-16 区间、标量区间、不拆代理对；
- 验证 Unicode 标签确实满足脚本策略和证明；
- 重算受保护域骨架、别名域集合和目标版本；
- 对小型自动机额外用 `enumerateAcceptedWords` 做暴力枚举对照；
- 检查 `allowedRange` 的 unicode/ascii 标签集合和摘要。

`checkAllowedRange()` 是只检查允许范围和位置信息的轻量版本。

`verifyTableSummary()` 可以把收藏夹或策略包附带的表摘要与当前加载的表对照。

## 小码点集穷举自检

`runExhaustiveChecks(config)` 开箱检查：

- 最长匹配：与一个独立的逐位置线性参考实现对照；
- 流式：每个测试词的所有分块方式，以及 UTF-16 任意切分；
- 空输出：消费输入但输出零个骨架符号；
- 最长匹配变化：`alternateConfusables` 两版规则逐词对照；
- 脚本扩展集合：与枚举所有脚本赋值的暴力判定对照；
- 后缀例外：通配/例外规则和显式案例；
- 只在旧版成立的别名：新表证明失败、旧表证明成功、单版本失败回退 ASCII；
- 提供 `versionFallback` 时，决策与检查器自洽。

示例：

```ts
const report = runExhaustiveChecks({
  confusables,
  alternateConfusables,
  scripts,
  policies: [latinPolicy, mixedPolicy],
  suffixTable,
  suffixCases: [
    { labels: ["www", "ck"], expectedSuffix: "ck" },
    { labels: ["a", "b", "ck"], expectedSuffix: "b.ck" },
  ],
  versionFallback: {
    request,
    newVersion: "v2",
    oldVersion: "v1",
  },
});
```

## 安全与失败方式

- 证明搜索超过 `maxCandidateStates` 时视为失败并回退 ASCII，不会默认放行。
- 别名验签失败、版本不匹配、后缀不匹配、品牌不匹配或时间窗外都视为无效证明。
- 表摘要只覆盖语义字段；规则声明顺序属于语义，因此顺序变化会改变摘要。
- 输入标签必须是 IDNA 校验后的标签；本包不执行 IDNA 映射。
- 自动机必须确定性；重叠的标量区间会在校验时抛错。
