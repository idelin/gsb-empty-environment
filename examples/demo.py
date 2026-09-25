"""timemap 运行样例：暂停 / 倍速 / 反向段的像、逆像、复合、版本化与检验。"""
import os
import sys

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

from timemap import (  # noqa: E402
    Interval,
    IntervalSet,
    OffsetTable,
    PiecewiseAffineMap,
    Segment,
    VersionedTimeline,
    WallClock,
    verify_composition,
)


def show(title):
    print(f"\n=== {title} ===")


f = PiecewiseAffineMap(
    [
        Segment(Interval(0, 5, True, False), 1, 0, "s1", "normal"),
        Segment(Interval(5, 10, True, False), 0, 5, "p1", "pause"),
        Segment(Interval(10, 20, True, False), 1, -5, "s2", "normal"),
        Segment(Interval(20, 30, True, False), -1, 35, "r1", "reverse"),
        Segment(Interval(30, 40, True, False), 2, -55, "v1", "speed"),
    ]
)

show("分段仿射映射 f（常速/暂停/常速/反向/二倍速）")
for seg in f.segments:
    print(f"  {seg.seg_id:3} {seg.domain}  rate={seg.rate}  offset={seg.offset}  kind={seg.kind}")

show("合成偏移转换表（OffsetTable）")
print(OffsetTable(f))

show("点映射：暂停 [5,10) 使 5 和 10 映射到同一逻辑点")
for x in (0, 5, 7, 10, 25, 35):
    print(f"  f({x}) = {f.value_at(x)}")

show("像：f([0,40)) 的完整区间集合（含证据）")
res = f.image(IntervalSet.of(Interval(0, 40, True, False)))
print(f"  image = {res.values}")
for c in res.evidence:
    print(f"    证据: 段 {c.seg_id:3} 源 {c.source} -> 像 {c.target}")

show("逆像：f^-1({5}) 常量段返回整段原像，边界选择保留")
res = f.preimage(IntervalSet.point(5))
print(f"  preimage = {res.values}")
for c in res.evidence:
    print(f"    证据: 段 {c.seg_id:3} 目标 {c.target} <- 原像 {c.source}")

show("逆像：f^-1((5,15)) 开区间端点开闭不丢失")
res = f.preimage(IntervalSet.of(Interval(5, 15, False, False)))
print(f"  preimage = {res.values}")

g = PiecewiseAffineMap(
    [
        Segment(Interval(0, 10, True, False), "1/2", 0, "g1", "speed"),
        Segment(Interval(10, 30, True, False), 3, -20, "g2", "speed"),
    ]
)
h = f.compose(g)
show("映射复合 h = g ∘ f（段上记录来源 parents）")
for seg in h.segments:
    print(f"  {seg.domain}  rate={seg.rate}  offset={seg.offset}  parents={seg.parents}")

show("独立有理点枚举检验 h == g ∘ f（按断点采样，不逐 tick 展开）")
errors = verify_composition(f, g, h)
print(f"  失配数 = {len(errors)}  ->  {'通过' if not errors else errors}")

show("版本化时间线：新增暂停不修改旧版本查询")
base = PiecewiseAffineMap(
    [Segment(Interval(0, 20, True, False), 1, 0, "base", "normal")]
)
tl = VersionedTimeline(base)
tl.insert_pause(WallClock(5), WallClock(10))
tl.insert_pause(WallClock(12), WallClock(13))
for v in range(tl.version_count):
    row = ", ".join(
        f"f({x})={tl.logical_at(WallClock(x), version=v).value}" for x in (7, 15)
    )
    print(f"  版本 v{v}: {row}")
res = tl.preimage(IntervalSet.point(5), version=1)
print(f"  v1 中逻辑点 5 的原像 = {res.values}  （边界选择：暂停段 [5,10) + 点 10）")

show("巨大区间按断点处理（不逐 tick 展开）")
big = PiecewiseAffineMap(
    [Segment(Interval(0, 10**18, True, False), 2, 1, "big", "speed")]
)
hb = big.compose(big)
print(f"  复合定义域 = {[str(s.domain) for s in hb.segments]}")
print(f"  检验失配数 = {len(verify_composition(big, big, hb))}")
print(f"  f(10^17) = {hb.value_at(10**17)}")
