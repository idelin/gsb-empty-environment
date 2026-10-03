# 员工绩效考评时态指标账本（temporal-kpi-ledger）

跨考评周期的 KPI 目标时态管理组件：目标值与权重随组织战略在考核周期内按版本调整，
支持季度末最终加权得分计算、历史战略快照下的考核推演，以及带责任人签名的版本影响记录。

## 环境要求

- Node.js >= 22.6（使用内置 `--experimental-strip-types` 直接运行 TypeScript，零运行时依赖）
- 测试使用 Node.js 内置测试框架 `node:test`
- 可选：`npm install` 后执行 `npm run typecheck` 进行 `tsc --noEmit` 类型检查

## 运行

```bash
npm run example   # 运行 Q1 追溯调整验收样例
npm test          # 运行 node:test 测试
```

## 核心概念

- **时态参数条目** `KpiParamEntry`：KPI 的目标值与权重在 `[validFrom, validTo)` 内有效；
  同一版本内要求全周期每日权重之和为 1，且同一 KPI 区间不得重叠。
- **版本** `Version`：每次战略调整提交一个不可变版本，记录责任人、登记日（可晚于生效日，
  即追溯调整）、变更原因，并以 `HMAC-SHA256(责任人密钥, 规范化内容)` 生成版本签名，
  可通过 `verifyVersion` 校验防篡改。
- **得分计算** `computeScore(asOfVersionId?)`：按参数恒定区间分段，段内
  `Σ(权重 × 实际值/目标值)`，再按段天数加权汇总为季度末最终得分；
  传入历史版本 ID 即可重放该战略快照下的考核推演结果。
- **版本影响** `impacts()` / `compareVersions(a, b)`：记录每次提交相对上一版本的得分差，
  支持任意两版本间调整前后对比。

## 验收场景（examples/q1-retroactive.ts）

Q1 周期为第 `[1, 91)` 天：

1. v1（第 1 天，张伟）：目标A 权重 0.6 在 `[1, 60)` 有效，`[60, 91)` 为 0.5；目标B 对应 0.4 / 0.5。
2. v2（第 45 天，李娜）：追溯调整为全季度目标A 权重 0.4、目标B 权重 0.6。
3. 实际完成值：目标A 108（目标 100，达成率 1.08），目标B 72（目标 80，达成率 0.90）。

输出：

- 调整前（v1 快照）：`(1.008×59 + 0.99×31) / 90 = 1.0018`
- 调整后（v2）：`0.4×1.08 + 0.6×0.90 = 0.9720`
- 调整前后实际得分差：`-0.0298`
- 两个版本的责任人签名均保留并校验通过。

## 目录结构

```
src/types.ts          类型定义
src/signature.ts      规范化序列化与 HMAC 版本签名
src/ledger.ts         TemporalKpiLedger 账本核心
src/index.ts          公共导出
examples/q1-retroactive.ts  验收样例
test/ledger.test.ts   node:test 测试（12 例）
```
