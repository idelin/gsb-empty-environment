# 多窗口排序复用（窗口定义共享计划）

纯 Python 3.11 + 标准库实现，不调用任何 SQL 引擎。给定多个窗口定义，判断
哪些窗口能物理复用同一次排序，保留各自不同的 `ROWS` 帧边界，输出共享计划
与**实际排序次数**，并用本地合成关系表执行验证。

## 复用判定规则

两个窗口 `w1`、`w2` 能共享一次排序，当且仅当：

1. `PARTITION BY` 的**多重集合**相同（列顺序无关，重复列计数，例如
   `PARTITION BY a,b` 等价于 `b,a`，但 `a,a` 不同于 `a`）；
2. `w2` 的 `ORDER BY` 逐键是 `w1` 的**前缀**：列相同、升降序相同、
   生效的 NULL 位置相同（完全相同也允许）。

由此直接得到：

- 同一排序、不同 `ROWS` 帧：**共享排序，不共享结果**（每个窗口独立算帧）；
- 升序 vs 降序：不可直接复用（不假设反向扫描）；
- `NULLS FIRST` vs `NULLS LAST`：即使方向相同也不可复用；
- 更长排序可服务更短窗口：额外排序键只会打破对等行（peer）内部的并列，
  不改变排名函数值或 ROWS 帧聚合结果。

## NULL 与多重集合语义

- NULL 位置缺省采用 SQL:2008/PostgreSQL 约定：`ASC` → `NULLS LAST`，
  `DESC` → `NULLS FIRST`；可显式覆盖。
- 输入是**多重集合**：完全重复的行保留，不做任何 `DISTINCT`。
- 并列行上的 `row_number` 以输入顺序为稳定决胜键；`rank`/`dense_rank`
  对对等行给同名次；`sum`/`avg` 跳过 NULL，`count(col)` 不计数 NULL，
  `count(*)` 对帧内所有行计数。

## 排序次数口径

- **逻辑排序形状**：去重/前缀合并后需要的不同
  `(PARTITION BY 形状, ORDER BY 全键)` 数量。
- **实际排序次数（physical sort calls）**：每个排序形状 × 它对应的非空分区数，
  也是执行器真实调用 `sorted(...)` 的次数（由 `SortCounter` 核对）。
- 未共享基线：每个窗口每个分区各排一次。

## 运行

```bash
python3.11 demo.py                                   # 运行样例
python3.11 -m unittest discover -s tests -v          # 测试
```

已保存输出：`sample_output/demo_run.txt`、`sample_output/test_results.txt`。

## 代码结构

- `window_sharing/model.py`：`WindowSpec` / `OrderKey` / `Frame` / `FrameBound`，
  以及默认帧与默认 NULL 位置。
- `window_sharing/planner.py`：前缀覆盖判定、极小排序集合、共享计划与
  排序次数计算（`build_share_plan`、`physical_sort_count`、`naive_sort_count`）。
- `window_sharing/engine.py`：本地执行器。每个 `(排序形状, 分区)` 只排序一次，
  多个窗口复用同一有序列表，但帧切片与结果数组各自独立。
- `demo.py`：8 行合成销售表（含 NULL 与重复行）和 6 个窗口。
- `tests/test_window_sharing.py`：17 个 unittest 用例。

## 样例结论

6 个窗口在 2 个 `region` 分区上：逻辑排序形状 4 个；实际排序
7 次（全局无分区 1 次 + 3 个 region 排序形状各 2 次）；不共享需要 11 次。
其中两个 ROWS 帧不同的聚合窗口共享同一排序，一个更短的单键排名窗口以前缀
方式复用两键排序，而降序与 NULL 位置不同的窗口各自单独排序。
