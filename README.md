# ordinal-weekly-calendar

TypeScript 日历库：在显式整数日期序号（ordinal）上展开每周重复规则。

## 规则模型（`WeeklyRule`）

- `start`：起始日序号（含），任意整数（支持负数）。
- `interval`：周间隔，自 `start` 所在周起每第 `interval` 周为活跃周。
- `weekdays`：活跃周内的星期集合（0..6，序号的星期为 `ordinal mod 7`）。
- `count`：终止次数，按**排除前**的候选计算——被 `exdates` 排除的候选仍消耗次数，
  因此排除会缩短可见序列而不是顺延。
- `exdates`：从可见序列中剔除的序号集合。

## 语义要点

- 实例 id 稳定：`inst@<原始序号>`；改期（`Calendar.reschedule`）后保留原 id 与
  `originalDate`，可改到任意日期（包括另一实例的日期、非候选日、exdate）。
- 改期到另一实例的日期时，两个实例共存于该日，按 `(date, originalDate, id)` 排序。
- 展开窗口为 `[from, to)`；`expand` 支持 `limit` + 不透明游标分页，
  游标编码完整排序键，保证同日期多实例时翻页不丢不重。

## 验证

`test/verify.ts` 用独立的逐日扫描 oracle 与分页展开交叉核对，覆盖：
分页一致性（多种页大小）、改期到另一实例日期、改期到非候选日/exdate、
终止边界（count 0/1/N、窗口端点恰好落在末次实例）、排除前计数、
同日期多实例游标稳定性、负序号与参数校验。

```sh
npm test   # 需要可用的 tsc（TypeScript 编译器）
```
