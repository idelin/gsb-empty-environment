# 可撤回的最小/最大间隔网络（整数精确时间）

Python 3.11 标准库实现，无第三方依赖。处理事件之间的最小/最大间隔与绝对时间
范围，给出可行安排（feasible schedule）、每个事件最早/最晚可行时间界，并在不
可行时输出指回原约束 ID 的负环（contradiction cycle）。所有时间量都是整数
tick，最短路径比较不使用任何浮点容差，因此相差一个 tick 的矛盾也会被精确检出。

## 运行

```bash
python3.11 demo.py                 # 运行样例
python3.11 -m unittest discover -s tests -v
```

## 模型（差分约束）

事件 `v` 的时间为整数 `t(v)`。每条约束展开为有向边 `u -> v (权 w)`，表示
`t(v) - t(u) <= w`：

| 约束 | 边 |
| --- | --- |
| `lo <= b-a <= hi`（区间） | `a -> b : hi`，`b -> a : -lo` |
| `b-a >= lo`（最小间隔） | `b -> a : -lo` |
| `b-a <= hi`（最大间隔） | `a -> b : hi` |
| `lo <= b <= hi`（绝对时间窗） | 同上，对锚点 `Z`（`t(Z)=0`） |

维护全源最短路径闭包 `D`：

- 可行性 ⇔ 无负环；
- 锚点 `a=t0` 时，事件 `v` 的最紧时间窗为
  `t0 - D[v][a] <= t(v) <= t0 + D[a][v]`，不可达的一侧返回 `None`；
- 可行安排由 Bellman-Ford 零势解 + 连通分量平移得到。

## 关键语义

- **精确整数，不靠 epsilon**：`B-A∈[3,5]` 且 `A-B>=0` 精确不可行，负环
  `A -> B -> A`（和 -3），证据为原约束 ID（见 `demo.py` 第 1 节）。
- **统一平移自由度可锚定**：只有相对约束时，全体事件可统一平移；`bounds`
  返回两侧无界，`solve` 把每个自由连通分量平移到最早为 0；`anchor(event,
  value)` 锚定平移。
- **无约束事件单独返回范围**：与其它事件不连通的事件，`bounds` 返回
  `[-inf, +inf]`，`solve` 单独给值 0。
- **撤回必须放宽失去支持的界**：`pop()` / `remove_constraint()` 用存活约束
  从零重建闭包（Floyd-Warshall），凡是靠被撤约束撑住的界限必然放松。
- **增量传播 + 预算中断原子性**：新边用动态 APSP 插入传播，每条成功松弛计入
  `budget`；预算耗尽时恢复传播前快照并抛出 `BudgetExceeded`，网络不留半传播
  状态，`solve` 永远不可能返回伪可行安排。
- **负环证据**：增量传播发现负对角后，用独立的 Bellman-Ford（虚拟零源）重建
  负环，逐条边带原约束 `cid`。
- **历史版本**：`save_version(name)` / `restore_version(name)` 保存不可变快照。

## 时间概念的区分（`tempnet/time_model.py`）

- `Duration`：带符号的整数时长；
- `TimePoint`：事件轴上的整数时刻，轴原点在锚定前没有物理含义；
- `WallClock`：用合成偏移换算表 `UnitTable`（如 tick=微秒，毫秒/秒/分/时/天均
  为整数倍）绑定“纪元”后的墙钟时刻。两时间点相减得到的是 `Duration` 而不是
  `TimePoint`；非整除换算直接报错，绝不静默舍入；拒绝 float 入参。

## 独立校验（`tempnet/verifier.py`）

`Verifier` 不引用生产代码的任何中间状态：自行展开边、用 Bellman-Ford 判可行、
用独立 Floyd-Warshall 算最紧界、直接代入不等式验证安排。
`run_differential(RandomScenario(...))` 驱动随机的增量 add / push / pop /
commit 流，在每个检查点对比可行性、最早/最晚界和安排有效性（测试中 900+
检查点全部一致）。

## 文件

- `tempnet/time_model.py` — 整数 tick、合成单位表、时长/时间点/墙钟。
- `tempnet/network.py` — 可撤回最小/最大间隔网络：增量传播、预算、push/pop、
  版本、界与可行安排、负环取证、撤回重建。
- `tempnet/verifier.py` — 独立全量算法 oracle 与随机差分测试驱动。
- `demo.py` — 八个可运行场景。
- `tests/test_network.py`、`tests/test_time_model.py` — 30 个 unittest 用例。
