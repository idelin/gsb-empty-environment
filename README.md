# 员工绩效考评时态指标账本（Temporal KPI Ledger）

跨考评周期的 KPI 目标时态管理组件。目标值与权重随组织战略在考核周期内按版本调整；
支持季度末最终加权得分计算、历史战略快照下的考核推演，以及版本影响与责任人签名留痕。

## 环境要求

- Node.js >= 23.6（原生 TypeScript type-stripping，零依赖，无需编译）

## 时态模型

- **有效时间（valid time）**：每个 KPI 版本携带半开区间 `[validFrom, validTo)`（考评周期内的天数）。
- **事务时间（transaction seq）**：版本按录入顺序分配递增 `seq`，晚录入的版本在重叠区间上覆盖先录入版本，从而支持追溯调整。
- **快照推演**：`computeScore(asOfSeq)` 仅使用 `seq <= asOfSeq` 的版本，重放任一历史战略快照下的考核结果。

## 评分公式

周期按版本边界切分为若干分段，对每个分段 × 指标：

```
达成率 attainment = 分段内实际值均值 / 目标值
得分 points       = 权重 × 达成率 × (分段天数 / 周期天数) × 100
```

季度末最终加权得分为所有分段得分之和。权重合计偏离 1、分段缺少实际值、
实际值落在无生效版本区间等情况会在报告的 `warnings` 中给出提示。

## 版本签名

每个版本在录入时使用 HMAC-SHA256 对 `(seq, kpiId, targetValue, weight, validFrom,
validTo, actor, reason, recordedAtDay)` 签名，记录变更责任人；`verifyVersion()` 可校验
版本是否被篡改。

## API 概览

| 方法 | 说明 |
| --- | --- |
| `recordVersion(input)` | 录入一个 KPI 目标/权重版本，返回带 `seq` 与 `signature` 的版本 |
| `recordActual(kpiId, day, value)` | 录入某日实际绩效值 |
| `computeScore(asOfSeq?)` | 计算季度末加权得分；传 `asOfSeq` 则按历史快照推演 |
| `diffScores(fromSeq, toSeq)` | 输出两个快照间的得分差、分指标差与影响版本（含责任人签名） |
| `listVersions(asOfSeq?)` / `verifyVersion(v)` | 版本查询 / 签名校验 |

## 运行

```bash
npm test     # Node 内置测试运行器
npm run demo # 验收场景运行样例
```

## 验收场景

Q1（第 1–90 天）初始战略：目标 A 权重 0.6 在 `[1, 60)` 有效；第 45 天由责任人 bob
追溯调整为全季度 A=0.4 / B=0.6。账本输出调整前后实际得分差
（95.0222 → 93.0556，Δ = -1.9667），并保留 bob 的版本签名。
