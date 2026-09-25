# logmerge — 多源日志因果拓扑合并

合并来自多个来源、带局部顺序与时钟偏移误差的日志，输出保持因果的
稳定拓扑序，标注无法确定顺序的相邻事件对，并检测因果环。纯标准库，
Python 3.11，测试使用 unittest。

## 时间模型（`logmerge/timevals.py`）

三种概念是三种类型，不可混用：

- `WallClock`：源本地时钟读数，无算术，必须经 `OffsetTable` 转换。
- `TimePoint`：参考时间轴上的绝对时间点。
- `Duration`：持续时长。

所有数值为 `fractions.Fraction`，时间精确表示（如 `1/3` 偏移不丢精度）。
每个事件的参考时间是不确定区间 `[t - err, t + err]`。

## 约束与可行性（`logmerge/merger.py`）

三类约束共同进入可行性求解：

- `local`：同源事件按本地序号有序；
- `causal`：显式跨源因果边（happens-before）；
- `time`：区间不重叠时隐含的时间序（`hi(u) < lo(v)` 则 u 先于 v）。

求解流程：

1. 环检测：任一约束组合成环即返回 `Contradiction(reason="cycle")`，
   附带约束环（含边的类型），绝不强改时间。
2. 时间可行性：对严格不等式 `t_u < t_v` 在区间域上精确传播下界
   （`(值, 是否严格)` 对），不可行时返回
   `Contradiction(reason="infeasible-time")` 与见证链。
3. 稳定线性扩展：Kahn 算法 + 堆键 `(区间下界, 事件ID)`，输出确定。
4. 必然序标注：由传递闭包计算，每个相邻对标注
   `next_necessary`（`True` 必然有序 / `False` 不确定 / 末位 `None`）。

`enumerate_extensions` 对小规模输入枚举全部拓扑序，
`verify_necessity` 用枚举结果交叉核验闭包算出的必然关系与不确定标记。

## 版本与增量修订（`logmerge/store.py`）

- `LogStore.publish()` 产生不可变 `Version`，旧读者通过 `get(n)` 保留旧版本。
- `revise(corrections)` 按 ID 更正事件，返回 `Revision`，其中 `Delta`
  给出顺序差量：移动的事件（旧位置→新位置）、增删事件、
  必然序关系的变化（`became_necessary` / `became_uncertain`）。
- 相同 ID 不同内容的事件被 `ConflictError` 拒绝，更正必须走 `revise()`。

## 运行

```sh
python3.11 -m unittest discover -v   # 20 个测试
python3.11 examples/sample.py        # 端到端样例
```

样例覆盖：墙钟看似反向但按显式因果排序；误差重叠且无因果标
`UNCERTAIN`；因果边与时间区间矛盾返回约束环；严格因果序与相同精确
时间戳矛盾返回不可行链；事件更正后的增量差量与旧版本保留。
