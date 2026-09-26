# logmerge — 多来源日志的因果稳定合并

纯 Python 3.11 标准库实现。把多个来源、各自带**时钟偏移误差区间**的日志，
合并成一条保持因果的**稳定拓扑序**，并明确标注哪些相邻对的顺序是
*必然的*、哪些是*无法确定的*。矛盾时返回具体的**约束环**，绝不强行修改
已记录的时间。事件更正以**版本**方式发布差量，旧读者保留旧版本。

## 概念区分

- `WallClock`：某个来源时钟的原始读数（不同来源之间不可直接比较/相减）。
- `TimePoint`：参考时间轴上的绝对时间点。
- `Duration`：两个时间点之差（有符号时长）。

所有时间值用 `fractions.Fraction` 精确表示，全流程无浮点误差。
`OffsetTable` 是**合成偏移转换表**：`true = wall + offset`，
`offset ∈ [lo, hi]`（每来源一个区间），被视为不可修改的事实。

## 工作原理

1. **联合可行性求解**：偏移区间、来源内局部顺序、跨源因果边统一编码为
   差分约束 `x_v - x_u <= c`（`logmerge/constraints.py`），Bellman-Ford 判可行；
   不可行时提取负环作为**矛盾约束环**返回（`InfeasibleError.ring`），时间不被改写。
2. **因果环检测**：显式因果/局部顺序图上的有向环单独检测
   （`CausalCycleError`）——零权环时间上可行但因果上不成立。
3. **必然序**：`a ≺ b` 当且仅当 (a) 因果/局部顺序可达，或 (b) 联合约束
   系统蕴含 `t_a < t_b`（严格，由 Floyd-Warshall 全对闭包精确判定）。
   墙钟看似反向但有显式因果时按因果排序；无因果且误差区间重叠的对
   标为 **uncertain**。
4. **稳定线性扩展**：Kahn 算法 + 确定性优先级（最早可行真时间、来源、
   墙钟读数、ID），同一输入永远得到同一输出；每个相邻对标注
   NECESSARY / uncertain。
5. **版本化更正**：`LogMerger.correct()` 生成新 `Version` 并给出
   `OrderDiff`（移动的事件、新变必然/不确定的对）；旧版本不可变，
   `get_version(1)` 的旧读者视图不受影响。导致矛盾的更正被拒绝且不产生新版本。
6. **独立核验**（`logmerge/verify.py`）：
   - `verify_markers`：枚举必然序 DAG 的**全部线性扩展**，核验每个必然对
     在所有扩展中同向、每个不确定对在扩展集中两个方向都出现；
   - `sampled_orders`：对每来源偏移取 lo/中点/hi 网格，过滤满足全部
     局部顺序与因果约束的赋值，收集诱导出的全序，检查它们都是必然序的
     线性扩展。

## 运行

```bash
python3.11 examples/run_sample.py     # 端到端样例
python3.11 -m unittest discover -s tests -v   # 测试
```

## 样例输出（`examples/run_sample.py`）

```
== version 1: initial merge
  0: w1   true-time in [995, 1005]
  1: d1   true-time in [1000, 1010]
  2: d2   true-time in [1015, 1025]
  3: w2   true-time in [1045, 1055]
  4: c1   true-time in [1050, 1070]
  adjacent pairs:
    w1 -> d1: NECESSARY
    d1 -> d2: NECESSARY
    d2 -> w2: NECESSARY
    w2 -> c1: uncertain
  uncertain pairs: c1~w2
  marker verification: OK

== contradictory input (causal edge c1 -> w1 is impossible under the offsets)
  rejected, constraint ring returned, times unmodified:
    [offset] offset(web) <= 5
    [wall] t(w1) = 1000 + offset(web)
    [causal] c1 happens-before w1
    [wall] t(c1) = 960 + offset(cache)
    [offset] offset(cache) >= 90

== version 2: fix c1 wall clock
  0: w1   true-time in [995, 1005]
  1: d1   true-time in [1000, 1010]
  2: d2   true-time in [1015, 1025]
  3: w2   true-time in [1045, 1055]
  4: c1   true-time in [1120, 1140]
  adjacent pairs:
    w1 -> d1: NECESSARY
    d1 -> d2: NECESSARY
    d2 -> w2: NECESSARY
    w2 -> c1: NECESSARY
  marker verification: OK

diff v1 -> v2: order unchanged
  now necessarily ordered: w2 < c1

== old reader still sees v1: w1 d1 d2 w2 c1
   v1 uncertain pairs: [('c1', 'w2')]
```

要点：`c1` 的墙钟读数 960 看似最早，但偏移表与因果边 `d2 → c1`
把它放在 `d2` 之后（按因果而非墙钟）；`w2`/`c1` 误差区间重叠且无因果，
标为 uncertain；矛盾输入返回完整约束环而不改时间。

## 测试结果

```
$ python3.11 -m unittest discover -s tests
Ran 32 tests in 0.109s
OK
```

覆盖：精确时间运算与非法混用拒绝、同 ID 冲突拒绝/相同去重、
局部顺序保持、墙钟反向按因果排序、重叠区间标不确定、严格分离标必然、
边界相等（`t_a ≤ t_b` 可取等）判不确定、分数精确判定严格性、
矛盾约束环/因果环/来源内墙钟倒退、更正差量与旧版本保留、
矛盾更正拒绝、全拓扑序枚举核验、200 个随机小场景交叉验证。

## 目录结构

```
logmerge/
  timemodel.py    WallClock / TimePoint / Duration / OffsetTable（精确时间）
  events.py       Event、同 ID 冲突拒绝与去重
  constraints.py  差分约束系统：可行性、矛盾环提取、全对闭包
  merger.py       因果环检测、必然序闭包、稳定线性扩展与标注
  versions.py     LogMerger 版本化、更正、OrderDiff
  verify.py       全线性扩展枚举核验 + 偏移网格采样交叉验证
examples/run_sample.py
tests/            unittest 套件
```
