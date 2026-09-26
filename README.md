# timemap — 实际时间 ↔ 连续逻辑时间的分段仿射映射

纯 Python 3.11+（仅标准库），把含暂停段的实际（墙钟）时间映射到连续逻辑时间。
所有数值用 `fractions.Fraction` 精确表示，拒绝 float；区间端点的开闭全程保留；
一切运算只按断点进行，绝不逐 tick 展开，巨大区间（如 10^15）同样是常数级开销。

## 功能概览

- **分段仿射映射**：播放（斜率 1）、倍速（斜率 k）、暂停（斜率 0）、反向（斜率 < 0）段任意组合。
- **像与逆像**：`image()` / `preimage()` 返回完整 `IntervalSet`；常量（暂停）段对命中值返回整段原像，
  暂停 `[5,10)` 使 5 和 10 映到同一逻辑点，逆映射返回含两端边界选择的 `[5, 10]`。
- **映射复合**：`f.compose(g)` 得到 `g ∘ f`，斜率相乘、断点对齐、证据合并。
- **独立校验**：`check_composition(f, g, h)` 用有理点枚举（边界 + 相邻中点）独立验证 `h == g ∘ f`。
- **局部修订与版本**：`MapHistory.revise()` 生成新的不可变版本，旧版本查询结果不变；
  每段携带 `Evidence` 来源证据，修订记入 `Revision` 日志。
- **合成偏移转换表**：`OffsetTable` 以 `logical = logical_start + (real - real_start) * rate` 精确换算。
- **类型区分**：`WallClock`（墙钟时间点）、`LogicalTime`（逻辑时间点）、`Duration`（持续时长），
  时间点 − 时间点 = 时长，时间点 + 时间点 直接 `TypeError`。

## 快速开始

```python
from fractions import Fraction
from timemap import Interval, MapHistory, Evidence, PiecewiseMap, check_composition

# 播放 [0,5) → 暂停 [5,10) → 2 倍速 [10,20) → 反向 [20,30]
f = PiecewiseMap.build(0, 0, [(5, 1), (5, 0), (10, 2), (10, -1)])

f.eval(5) == f.eval(10) == 5                 # 暂停两端映到同一逻辑点
f.image(f.domain())                           # [0, 25]
f.preimage(Interval.point(5))                 # [5, 10] —— 边界选择完整保留

g = PiecewiseMap.build(0, 0, [(10, 1), (5, 0), (15, Fraction(1, 2))])
h = f.compose(g)                              # g ∘ f
check_composition(f, g, h).ok                 # True —— 有理点枚举独立验证

hist = MapHistory(f)
v2 = hist.revise(1, 12, 14, [(2, 0)], reason="ad break",
                 evidence=(Evidence("editor", "insert ad break pause"),))
hist.version(v2).eval(13)                     # 9  —— 新版本含广告暂停
hist.version(1).eval(13)                      # 11 —— 旧版本查询不受影响
```

## 运行样例

```
$ python3 examples/demo.py
========================================================================
1. Piecewise-affine map: play / pause / 2x speed / reverse
========================================================================
[0, 5) play: logical = 1*real + 0
[5, 10) pause: logical = 0*real + 5
[10, 20) speed x2: logical = 2*real + -15
[20, 30] reverse: logical = -1*real + 45

-- pause [5,10) maps both bounds to one logical point
f(5) = 5  f(10) = 5
-- pre-image of logical point 5 (boundary choices kept)
preimage({5}) = [5, 10]

========================================================================
2. Composition with a second map, checked by rational enumeration
========================================================================
composition check h == g . f: OK (13 rational sample points)
h(13) = 10  preimage({10}) = [25/2, 15] U {30}

========================================================================
3. Versioned local revision: insert an ad-break pause at real [12, 14)
========================================================================
new pause: v2 f(12) = 9  f(14) = 9
old version still answers: v1 f(13) = 11  v2 f(13) = 9
evidence at v2 t=13: ['editor: insert ad break pause']
evidence at v1 t=13: ['ingest: original timeline']

========================================================================
4. Offset conversion table with exact wall-clock / logical types
========================================================================
WallClock(25/2) -> LogicalTime(10)
wall-clock choices for LogicalTime(5): [5, 10]

========================================================================
5. Huge intervals stay breakpoint-based (no tick expansion)
========================================================================
domain [0, 3000000000000000] with 3 segments
image = [0, 4000000000000000]
preimage({1000000000000000}) = [1000000000000000, 2000000000000000]
```

## 测试结果

```
$ python3 -m unittest discover -s tests -v
...
test_revision_log (test_versioning.TestVersioning.test_revision_log) ... ok
test_stacked_revisions (test_versioning.TestVersioning.test_stacked_revisions) ... ok
test_versions_diverge_only_in_revised_range ... ok

----------------------------------------------------------------------
Ran 45 tests in 0.006s

OK
```

## 目录结构

- `timemap/types.py` — `WallClock` / `LogicalTime` / `Duration`，精确值（拒绝 float）
- `timemap/intervals.py` — 开闭端点区间与规范化区间集合
- `timemap/mapping.py` — 分段仿射映射：像、逆像、复合、局部修订、证据
- `timemap/verify.py` — 有理点枚举的复合关系独立校验
- `timemap/versioning.py` — 不可变版本与修订日志
- `timemap/table.py` — 合成偏移转换表
- `tests/` — 45 个 unittest 用例
- `examples/demo.py` — 端到端运行样例
