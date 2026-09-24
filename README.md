# temporal-diff：带生效区间与优先级的差分约束系统

维护约束图 `x_v - x_u <= c`（边 `u -> v`，权 `c`），每条约束带左闭右开
生效区间 `[start, end)` 与优先级。纯标准库，Python 3.11+，测试用 unittest。

## 功能

- **查询**：`query(t0, t1)` 返回时间窗的最大可行分段（相邻且势值相同的
  分段合并）及逐点最大势值；冲突分段给出准确半开区间与原始约束 ID
  构成的负环。
- **批量更正**：`batch_correct` 支持 upsert / retract / add_vertex；
  同 ID 再 upsert 视为新版本（version 递增）。
- **探索**：`push()` / `pop()` 快照并还原域（约束版本）、证据（决策日志）
  与优先选择（软撤销集）。
- **软撤销**：`resolve(t0, t1)` 反复撤销负环上优先级最低（数值最小）的
  约束直至可行；每个决策显式记录进 `decisions` 并返回，绝不静默忽略冲突。
- **图形态**：顶点、边均为字符串 ID；保留孤立点与重边；支持零权环
  （不视为冲突）与任意大整数权重。

## 分段状态共享

时间轴被所有区间端点切成原子分段，每个分段内活跃约束集恒定。求解结果
按 `(分段, 活跃约束版本集, 相关撤销集, 顶点集版本)` 缓存：区间更正只让
被覆盖的分段失效，未变分段直接命中缓存，不会为每个时间点独立复制图。
`tests` 中的 `test_unchanged_segments_share_solve_state` 用对象同一性
（`is`）验证这一点。

## 参考核对

`temporal_diff/reference.py` 是独立的逐区间全量 Bellman-Ford 实现：
对每个原子分段从零建图跑到不动点，与主系统逐段比对可行性、势值，
并独立验证负环证据（边均活跃、首尾相接、权和为负）。

## 运行

```bash
python3 -m examples.replay        # 可重放样例
python3 -m unittest discover -s tests -v
```
