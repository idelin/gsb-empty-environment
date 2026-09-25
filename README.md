# timemap：含暂停段的分段仿射时间映射

把含暂停段的实际（墙钟）时间映射到连续逻辑时间。纯 Python 3.11 标准库实现，
测试使用 `unittest`。所有时间量都是 `fractions.Fraction` 精确有理数。

## 能力

- **三种时间量**：`WallClock`（墙钟时间）、`TimePoint`（逻辑时间点）、
  `Duration`（持续时长），类型系统禁止混用（`timemap/quantities.py`）。
- **分段仿射映射**：`logical = rate * real + offset`，段可为常速、倍速
  （rate>1）、反向（rate<0）、暂停（rate=0 常量段）（`timemap/mapping.py`）。
- **像与逆像**：返回完整 `IntervalSet`，端点开闭严格保留；常量段的逆像
  返回整段原像，相邻段贡献的边界点一并保留（边界选择）。
- **暂停语义**：暂停 `[5,10)` 使 5 和 10 映射到同一逻辑点；`f^-1({5})`
  返回 `[5,10]`（暂停段 `[5,10)` 与后继段贡献的边界点 10 之并）。
- **映射复合**：`f.compose(g)` 得 `h = g ∘ f`，段上记录来源 `parents`。
- **局部修订与版本化**：`VersionedTimeline.insert_pause` 生成新版本，
  旧版本查询不受影响（`timemap/versioned.py`）。
- **来源证据**：像/逆像结果携带 `Contribution(段, 源区间, 目标区间)`。
- **合成偏移转换表**：`OffsetTable` 由映射合成 (断点, 速率, 偏移) 表，
  二分点查（`timemap/table.py`）。
- **独立检验**：`verify_composition` 用有理点枚举（断点 + 均分点）精确
  检验 `h == g ∘ f`；巨大区间按断点处理，绝不逐 tick 展开
  （`timemap/checker.py`）。

## 运行

```bash
python3.11 examples/demo.py     # 运行样例
python3.11 -m unittest discover # 运行测试（46 个）
```

## 运行样例（节选）

```
=== 点映射：暂停 [5,10) 使 5 和 10 映射到同一逻辑点 ===
  f(5) = 5
  f(7) = 5
  f(10) = 5

=== 逆像：f^-1({5}) 常量段返回整段原像，边界选择保留 ===
  preimage = IntervalSet([5, 10], [30, 30])
    证据: 段 p1  目标 [5, 5] <- 原像 [5, 10)
    证据: 段 s2  目标 [5, 5] <- 原像 [10, 10]
    证据: 段 v1  目标 [5, 5] <- 原像 [30, 30]

=== 版本化时间线：新增暂停不修改旧版本查询 ===
  版本 v0: f(7)=7, f(15)=15
  版本 v1: f(7)=5, f(15)=10
  版本 v2: f(7)=5, f(15)=9

=== 独立有理点枚举检验 h == g ∘ f（按断点采样，不逐 tick 展开） ===
  失配数 = 0  ->  通过
```

完整输出见 `examples/demo.py` 的运行结果。

## 测试结果

```
$ python3.11 -m unittest discover
..............................................
----------------------------------------------------------------------
Ran 46 tests in 0.006s

OK
```

覆盖：时间量类型规则、区间开闭合并/求交、五段映射的像与逆像、
常量段整段原像与边界选择、复合及来源、检验器通过/抓错、
版本化暂停（含跨段暂停）、巨大区间（10^15 ~ 10^18）断点处理。

## 结构

```
timemap/
  quantities.py  # WallClock / TimePoint / Duration，Fraction 精确表示
  intervals.py   # Interval / IntervalSet，端点开闭不丢失
  mapping.py     # Segment / PiecewiseAffineMap：像、逆像、复合、证据
  table.py       # OffsetTable 合成偏移转换表
  versioned.py   # VersionedTimeline 局部修订与版本查询
  checker.py     # 有理点枚举检验复合关系
tests/           # 46 个 unittest 用例
examples/demo.py # 运行样例
```
