# 数据发布重识别评估组件

面向数据发布流程的重识别风险评估库。它同时覆盖单次发布、跨数据集连接、多次发布的累积交集，以及差分发布场景。

## 能力

- 按任意准标识（QI）组合切分等价类，统计类规模、唯一类、唯一记录和再识别概率。
- 用阈值 `k` 标记等价类规模小于阈值的 QI 组合和具体记录。
- 将发布数据与辅助数据集连接，区分“身份候选唯一”和“整行唯一重识别”两类风险。
- 合并历次发布，按累计发布量评估交集放大效应。
- 识别“单次发布都安全、交集后变唯一”的个体，并输出新增唯一和类规模收缩清单。
- 对已发布数据生成追加泛化建议，并投影泛化后的 k 匿名指标。

## 快速开始

```ts
import {
  assessReidentification,
  assessCumulativeReleases,
  type GeneralizationHierarchy,
  type Release,
} from "./src/index.ts";

const dataset = {
  name: "orders",
  records: [
    { id: "u1", age: 30, zip: "10001", gender: "F" },
    { id: "u2", age: 30, zip: "10001", gender: "F" },
    { id: "u3", age: 31, zip: "10002", gender: "M" },
  ],
};

const single = assessReidentification({
  dataset,
  quasiIdentifierCombinations: [["age", "zip"], ["gender"]],
  threshold: 2,
});

console.log(single.flaggedCombinations.map((item) => item.qis));
// [ [ 'age', 'zip' ] ]

const releases: Release[] = [
  {
    id: "r1",
    qis: ["region", "ageBand"],
    records: [
      { id: "A", region: "North", ageBand: "20-29" },
      { id: "B", region: "North", ageBand: "20-29" },
      { id: "C", region: "South", ageBand: "20-29" },
      { id: "D", region: "South", ageBand: "20-29" },
    ],
  },
  {
    id: "r2",
    qis: ["zone", "zip3"],
    records: [
      { id: "A", zone: "North", zip3: "100" },
      { id: "B", zone: "South", zip3: "200" },
      { id: "C", zone: "North", zip3: "100" },
      { id: "D", zone: "South", zip3: "200" },
    ],
  },
];

const regionHierarchy: GeneralizationHierarchy = {
  column: "region",
  levels: [
    { label: "original", transform: (value) => value },
    { label: "region suppressed", transform: () => "*" },
  ],
};

const cumulative = assessCumulativeReleases(releases, {
  threshold: 2,
  generalizationHierarchies: [regionHierarchy],
});

console.log(cumulative.classSizes);                 // [1, 1, 1, 1]
console.log(cumulative.newlyUniqueRecordIds);       // [ 'A', 'B', 'C', 'D' ]
console.log(cumulative.recommendation.actions);
// [ { column: 'region', level: 1, label: 'region suppressed', ... } ]
console.log(cumulative.recommendation.projected.minClassSize); // 2
```

## 公共 API

| API | 用途 |
| --- | --- |
| `assessReidentification(input)` | 单次发布总入口，支持多个 QI 组合和可选辅助数据集连接。 |
| `assessQuasiIdentifiers(dataset, qis, options)` | 评估单个 QI 组合。 |
| `assessQuasiIdentifierCombinations(dataset, combinations, options)` | 批量评估 QI 组合并汇总被标记组合。 |
| `assessCrossDatasetJoin(target, auxiliary, options)` | 评估连接造成的身份唯一化。 |
| `assessCumulativeReleases(releases, options)` | 评估多次发布的累计交集和差分发布。 |
| `planSingleReleaseGeneralization(dataset, qis, options)` | 为单次发布搜索泛化方案。 |
| `recommendCumulativeGeneralization(...)` | 为累计数据生成追加泛化建议。 |

`assessReidentification` 默认 `threshold = 5`；其余 API 要求显式传入阈值。

## 评估口径

### 等价类

对 QI 组合 `Q`，将所有记录按 `Q` 的取值签名分组。类规模为组内记录数：

- `uniqueClassCount`：规模为 1 的等价类数量。
- `belowThresholdClassCount`：规模小于 `k` 的等价类数量。
- `belowThresholdRecordCount`：落入风险类的记录数。
- `maxReidentificationProbability = 1 / minClassSize`。
- `flagged = belowThresholdClassCount > 0`。

取值使用带类型标签的稳定序列化，因此数字 `30` 与字符串 `"30"` 不相等；`null` 与 `undefined` 统一视为缺失值。缺失值默认作为同一个取值参与分组，也可用 `missingValuePolicy: "exclude"` 排除含缺失 QI 的记录。

### 跨数据集连接

连接按对齐后的 QI 位置取值。辅助数据集的身份字段默认是 `id`，也可用 `auxiliaryRecordIdField` 指定。候选数量按**不同身份数**计算，而不是按重复行数计算。

- `uniqueAuxiliaryCandidateCount`：QI 在辅助数据中只对应一个身份。
- `uniquelyReidentifiedRecordCount`：目标等价类和辅助身份候选都为单例，整行可唯一重识别。
- `narrowedToSingleIdentityCount`：辅助身份已唯一，但目标类仍大于 1，只能缩小身份范围，不能唯一确定目标行。
- `ambiguousTargetRecordCount`：辅助数据中存在多个身份候选。

`riskLevel` 为 `high` 表示存在整行唯一重识别，`medium` 表示存在身份唯一化或候选收窄，`low` 表示未发现唯一身份连接。

### 多次发布与交集放大

每个发布通过稳定的 `recordIdField`（默认 `id`）关联同一体。该字段只用于分析侧合并，不假设攻击者已知。

累计签名由“历次发布的 QI 取值 + 该个体是否出现在该次发布”共同组成。这样处理的原因是：

1. 重叠发布的 QI 维度会取交集，等价类被细分，唯一化被放大。
2. 差分发布的互斥批次具有不同发布覆盖模式，不会被错误合并成同一个等价类。

主要累计指标：

- `totalReleasedRecordCount`：历次发布记录数之和。
- `cumulativeDistinctRecordCount`：去重后的个体数。
- `overlapRecordCount`：多次发布带来的重复记录量。
- `averageReleasesPerIndividual`：平均每人被发布次数。
- `uniqueBeforeCount`：任一单次发布中已经唯一的个体数。
- `uniqueAfterCount`：累计签名下唯一的个体数。
- `newlyUniqueRecordIds`：单次发布均不唯一、累计交集后变唯一的个体。
- `amplifiedRecordIds`：累计类规模小于任一单次发布类规模的个体。

`releases` 保存每次发布的独立评估；`releaseDeltas` 保存相对于此前发布并集的 `added`、`retained` 和 `dropped` 记录。`dropped` 表示该次发布快照中不再出现，并不代表历史数据被撤回。

同一 QI 列在多次发布中取值不同会形成冲突。默认记录到 `conflicts` 并继续评估；`conflictPolicy: "reject"` 会直接报错。

### 追加泛化建议

`GeneralizationHierarchy.levels[0]` 必须是恒等变换，后续层级逐步变粗；没有配置层级的列可回退为抑制值 `*`。

建议生成器会：

- 以累计等价类为优化目标，搜索每个风险列的下一泛化层级。
- 优先降低风险记录数和唯一类数，其次提升最小类规模。
- 对已发布数据给出 `supplemental-generalized-release` 建议，并把动作应用到所有包含该列的既有发布副本。
- 输出 `before`、`projected`、`achievable` 和 `notes`，便于在发布前校验方案。

建议是启发式贪心结果，不保证全局最优。`achievable = false` 时，应抑制相关组合、减少跨发布重叠，或停止继续发布该粒度。

## 测试

```bash
npm test
```

测试覆盖：

- 单次发布的等价类规模、阈值标记、取值类型区分和缺失值策略。
- 跨数据集连接中的整行唯一、身份收窄、歧义匹配和未匹配。
- 两次发布各自安全、但交集后全部唯一的放大场景。
- 互斥差分批次不产生虚假交集，以及保留/删除记录的差分统计。
- 累计泛化建议的可达性、作用范围和投影指标。

## 假设与限制

- 评估基于精确 QI 取值相等；语义层级只用于生成泛化建议，不用于自动判断区间包含关系。
- 发布覆盖模式被视为可观察信息，这是偏保守的假设。
- 连接风险只建模 QI 等值连接，不包含概率匹配、噪声数据或属性推断攻击。
- 本组件评估 k 匿名和连接唯一化，不等价于差分隐私保证。
