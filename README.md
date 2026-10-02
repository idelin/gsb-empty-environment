# 多版本医疗处方配伍禁忌检查器

处方开具与禁忌规则的**双时态（bitemporal）判定器**：配伍禁忌规则库随临床指南版本演进，
系统既能回答“这张历史处方在**开立当时**是否合规”，也能回答“按**当前最新**医学规则回看，
它是否暴露出追溯风险”。

## 双时态模型

每条禁忌规则记录携带两个时间轴（均为左闭右开区间）：

- **业务时间（valid time）** `[validFrom, validTo)`：规则在临床上生效的时间，用于匹配处方开立时间；
- **知悉时间（transaction time）** `[recordedFrom, recordedTo)`：规则被系统收录/知悉的时间，
  同一 `ruleId` 随指南版本更替时，旧知悉版本被关闭而非删除，历史知悉状态可完整回溯。

## 两个判定入口

- `PrescriptionChecker.checkCompliance(prescription, asOfKnowledge?)`
  开方合规性判定。`asOfKnowledge` 缺省为开方时间，即“按开方当时已知的规则，是否合规”。
  `contraindicated`（禁用）命中判为不合规，`caution`（慎用）命中为合规但带警示。
- `PrescriptionChecker.auditRetroactiveRisk(prescription, asOfKnowledge?)`
  追溯风险审计。`asOfKnowledge` 缺省为规则库最新知悉时间；当前知悉切片中新出现、
  而开方时切片中不存在的命中，即标记为追溯风险。

## 验收边界场景

| 时间 | 事件 |
| --- | --- |
| 业务 10 | 处方 P 开立，含甲药 + 乙药 |
| 知悉 10 | 规则库无甲乙禁忌 |
| 知悉 20 | 指南 v2.0 收录：甲乙列为“慎用” |
| 知悉 25 | 查询开方合规性 => **合格**；风险审计 => **标记追溯风险** |

完整复现见 `examples/demo.ts` 与 `test/checker.test.ts`。

## 运行

要求 Node.js ≥ 23.6（原生 TypeScript 类型擦除 + 内置 `node:test`，零依赖、无需编译）：

```bash
nvm use          # 或任意 >= 23.6 的 Node
npm test         # 运行 node:test 测试
npm run demo     # 运行验收场景样例
```

## 目录结构

- `src/types.ts` — 双时态规则、处方、合规/审计报告等类型定义
- `src/rule-store.ts` — 双时态规则库（收录、版本更替、撤销、时态查询）
- `src/checker.ts` — 合规判定与追溯风险审计
- `test/checker.test.ts` — Node.js 内置测试（验收边界 + 双时态语义，共 9 例）
- `examples/demo.ts` — 验收边界场景运行样例
