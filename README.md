# weekly-recurrence

TypeScript 日历库：在显式整数日期序号（date serial）上展开每周重复规则。

## 模型

- 日期序号为整数天编号，序号 0 为周一；星期取值 `0`（周一）至 `6`（周日）。
- `WeeklyRule`：`startSerial`（起始序号，含）、`intervalWeeks`（周间隔）、
  `weekdays`（星期集合）、`count`（结束次数）、`exdates`（排除日期）。
- **次数按排除前候选计算**：先生成恰好 `count` 个候选，再应用排除日期；
  排除只会缩短序列，绝不向后顺延补位。
- 实例 id 为 `<ruleId>@<originalSerial>`；改期（move）只改变 `serial`，
  **保留原实例 id**。

## API（`src/weekly.ts`）

- `expandCandidates(rule)` — 排除前的候选序号（升序，恰好 `count` 个）。
- `expand(rule, moves?)` — 完整展开：应用排除与改期，按当前序号排序。
- `expandPage(rule, { offset, limit, moves? })` — 分页展开，返回
  `{ instances, offset, limit, total, hasMore }`。
- `expandRange(rule, from, to, moves?)` — 序号区间内的实例。
- `instancesOn(rule, serial, moves?)` — 单日实例（逐日扫描的基础）。
- `lastCandidateSerial(rule)` — 终止边界（最后一个排除前候选的序号）。

## 测试

```sh
npm test   # node --test（Node >= 23.6 原生运行 TypeScript）
```

测试通过逐日扫描（`instancesOn` 逐序号遍历）核对：分页展开与完整展开一致、
改期到另一实例日期时两实例共存且保留原 id、以及终止边界之后无任何实例。
