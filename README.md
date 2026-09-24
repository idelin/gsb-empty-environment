# joinplan — 多表连接规划与执行（纯标准库，无 SQL 引擎）

Python 3.11，仅使用标准库。合成关系在本地内存中构造，NULL 与多重集合
（multiset）语义显式定义，不调用任何 SQL 引擎。

## 语义约定

- **NULL**：`None` 表示 SQL NULL；等值连接中 NULL 永不匹配（包括 NULL=NULL）。
- **多重集合**：关系允许重复行；连接结果为匹配行对的笛卡尔积基数
  （重复键相乘）。左外连接对未匹配的保留侧行以 None 填充。
- **内连接**可自由交换/结合；**左外连接不可交换**（保留侧必须先于可空侧
  引入）；**相关依赖**以额外的先后序对（`dependencies`）限制合法顺序；
  不允许笛卡尔积（每步必须与已接入集合有连接边）。

## 成本模型（非单一选择率乘积）

每个连接步骤单独估计（`joinplan/costmodel.py`）：

```
step_cost = row_weight * 中间行数 + 2 * spill_weight * 溢写字节
溢写字节  = max(0, 中间行数 * 行宽 - 内存预算)   # 写出再读回各一次
```

连接基数用两侧 NDV（按各自行数截断）与 NULL 比例做区间运算估计；
外连接结果下界取保留侧行数（每行至少出现一次）。

## 统计未知与保守备选

统计以区间 `[lo, hi]` 表示（`Interval`），未知统计是宽区间
（`RelationStat.unknown()`）。规划按最坏情况（hi）排序，返回主计划加
保守备选（`PlanResult.alternatives`），并标记 `conservative=True`。

## 执行期重规划（安全物化边界）

执行器（`joinplan/executor.py`）左深执行并完全物化每个中间结果，物化点
是唯一安全的重规划边界：

- 观测行数超出估计区间（容差 `replan_tolerance`）时，仅用物化中间结果的
  实测统计（精确行数、实测 NDV/NULL 比例）重规划**剩余**连接；已物化
  结果保留，已产出行不会重复，最终多重集合与未重规划执行完全一致。
- 目录版本变化（统计过期）触发 `stale_stats` 决策并以新统计重规划。
- 中间结果超内存预算时，后续连接切换到分区（grace-hash）路径，
  记录模拟溢写字节。
- 明确状态：`Status.COMPLETED` / `BUDGET_EXHAUSTED` / `CANCELLED`，
  决策日志（`Decision`）确定性可复算。

## 小表穷举校验

`ranked_orders` 枚举全部合法计划并按最坏成本排序；关系数 ≤ 6 时
`make_plan` 自动报告启发式是否成本最优或差距（`PlanResult.optimal/gap`）。

## 运行

```bash
python3.11 -m joinplan.samples        # 可重放样例（确定性输出）
python3.11 -m unittest discover -s tests -v
```

样例覆盖：三表低选择率优先（含穷举校验）、外连接不可交换、未知统计的
保守备选、重复键超内存改分区路径、执行中观测偏差触发的边界重规划
（多重集合不变）、统计过期/预算耗尽/取消三种状态。

## 文件结构

- `joinplan/relations.py` — 关系、hash/partitioned 连接、命名多重集合比较
- `joinplan/stats.py` — 区间统计与带版本目录（过期检测）
- `joinplan/query.py` — 连接图：内/外连接边与相关依赖
- `joinplan/costmodel.py` — 行数 + 内存 + 溢写的区间成本模型
- `joinplan/planner.py` — 合法性、低选择率优先启发式、穷举校验
- `joinplan/executor.py` — 物化执行、边界重规划、分区路径、状态
- `joinplan/samples.py` — 六个可重放样例
- `tests/test_joinplan.py` — 24 个 unittest 用例
