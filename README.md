# 动态排班技能资质时态匹配器

轮班调度与岗位资质的**双时态**匹配器（TypeScript，零依赖，Node.js 内置测试运行器验证）。

## 概念

- **有效时间（valid time）**：证书实际生效/失效的业务时间。证书有效期为半开区间
  `[validFrom, validTo)`，可选复审期 `reviewBy` 会提前截断有效截止。
- **事务时间（transaction time）**：系统知悉某一事实的时间。追溯吊销以
  `{ revokedAt, knownAt }` 事件追加，支持"事后才知悉"的双时态快照查询。

## 核心行为

- `Scheduler.schedule(shift)`：按当前知悉状态校验班次**全程**被有效资质覆盖；
  未覆盖则拒绝并返回缺口子区间（如 `[100, 120)`）。
- `Scheduler.revoke(certId, revokedAt, knownAt)`：登记追溯吊销，自动扫描该员工
  该技能的已排班次，输出受影响班次的违规预警列表（含违规子区间）。
- `Certificate.effectiveInterval(knownAt)`：双时态快照——同一证书在不同知悉
  时间下呈现不同的有效区间。

## 运行

```bash
npm test    # node --test（Node ≥ 23，原生类型擦除，无需编译）
npm run demo
```

## 验收边界（test/matcher.test.ts）

| 场景 | 结果 |
| --- | --- |
| 持证 `[10,100)`，排班 `[80,120)` | 拒绝，缺口 `[100,120)` |
| 排班 `[10,100)` 与持证期重合 | 接受（半开区间边界） |
| 排班时未知吊销，`[50,80)` 通过 | 接受 |
| 知悉时间 30 收到"60 吊销"通知 | 自动预警该班次违规区间 `[60,80)` |
| 知悉前查询快照 | 仍显示 `[10,100)` 有效 |
| 复审期 `reviewBy=70` | 有效截止截断为 70 |
| 多证书并集 | 拼接覆盖整个班次 |

## 结构

- `src/interval.ts` — 半开区间运算（相交/相减求缺口）
- `src/certificate.ts` — 双时态证书（生效期、复审期、追溯吊销事件）
- `src/scheduler.ts` — 排班校验与吊销预警扫描
- `test/matcher.test.ts` — Node.js 内置 `node:test` 用例
- `examples/demo.ts` — 端到端运行样例
