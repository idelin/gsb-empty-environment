#!/usr/bin/env python3
"""两条时间轴的查询演示。

轴一 “10时发生了什么”：固定业务时间 t=10，沿观察时刻回放认知演化。
轴二 “当时我们知道什么”：固定观察时刻 as_of，回放当时所知的整条业务时间轴。

运行：python3 demo.py
"""

from __future__ import annotations

from bitemporal import INF, VersionIndex, Status
from bitemporal.fixtures import SCENARIOS


def build(name: str) -> VersionIndex:
    idx = VersionIndex()
    idx.append_many(SCENARIOS[name].records)
    return idx


def fmt_v(v):
    return "—" if v is None else repr(v)


def banner(title: str) -> None:
    print("\n" + "=" * 68)
    print(title)
    print("=" * 68)


def _line(res) -> str:
    if res.status is Status.PRESENT:
        return f"{res.status.value:9s} = {fmt_v(res.value)}  (rid={res.rid})"
    if res.status is Status.RETRACTED:
        return f"{res.status.value:9s}     (tombstone rid={res.rid})"
    return f"{res.status.value:9s}"


def show_checkpoints() -> None:
    idx = build("retroactive_retraction")  # A@20 -> B@30 -> 撤销@40
    banner("检查点：旧查询不被最新值污染（对象 sensor, 业务时间 t=10）")
    print("  as_of=15  证据未录入          ->", _line(idx.get("sensor", 10, 15)))
    print("  as_of=25  只看到20录入的 A     ->", _line(idx.get("sensor", 10, 25)))
    print("  as_of=35  看到30的更正 B       ->", _line(idx.get("sensor", 10, 35)))
    print("  as_of=45  看到40的追溯撤销     ->", _line(idx.get("sensor", 10, 45)))
    print("  证据：", [r.rid for r in idx.evidence("sensor", 10, 45)],
          "全部保留，按链规则选出撤销。")


def axis_one() -> None:
    banner("轴一：10时发生了什么 —— 固定 t=10，观察时刻推进")
    for scn, obj in [("retroactive_retraction", "sensor"),
                     ("multi_correction", "ledger")]:
        idx = build(scn)
        print(f"\n对象 {obj}（场景 {scn}）:")
        for when, res in idx.evolutions(obj, 10):
            tag = {Status.PRESENT: "有值", Status.RETRACTED: "撤销",
                   Status.UNKNOWN: "未知"}[res.status]
            print(f"  as_of={when:>3}  {tag:4s}  value={fmt_v(res.value):>6}"
                  f"  rid={res.rid}")


def axis_two() -> None:
    banner("轴二：当时我们知道什么 —— 固定 as_of，整条业务时间轴")
    views = [
        ("retroactive_retraction", "sensor", 25),
        ("retroactive_retraction", "sensor", 45),
        ("unknown_then_retract", "usage", 15),
        ("adjacent", "contract", 12),
        ("backfill", "backfill", 20),
        ("backfill", "backfill", 25),
    ]
    for scn, obj, as_of in views:
        idx = build(scn)
        print(f"\n对象 {obj} @ as_of={as_of}（场景 {scn}）:")
        for seg in idx.timeline_as_of(obj, as_of):
            end = "INF" if seg.vend is INF else seg.vend
            tag = {Status.PRESENT: "有值", Status.RETRACTED: "撤销",
                   Status.UNKNOWN: "未知"}[seg.status]
            print(f"  [{seg.vstart:>3},{end:>3})  {tag:4s}"
                  f"  value={fmt_v(seg.value):>8}  rid={seg.rid}")


def show_independent_overlap() -> None:
    idx = build("independent_overlap")
    banner("重叠记录优先级（无取代关系，规则不依赖插入顺序）")
    for as_of in (15, 25, 35):
        print(f"  meter t=10 as_of={as_of}: {_line(idx.get('meter', 10, as_of))}")
    print("  规则：链头按 (recorded_at, rid) 取最大；m2 录入更晚故胜出。")


def show_future_version() -> None:
    idx = build("future_version")
    banner("未来版本不可见")
    print("  plan t=0 as_of=25:", _line(idx.get("plan", 0, 25)))
    print("  plan t=0 as_of=55:", _line(idx.get("plan", 0, 55)))


def main() -> None:
    print("已加载合成夹具场景：")
    for name, sc in SCENARIOS.items():
        print(f"  - {name}: {sc.description}（{len(sc.records)} 条记录）")
    show_checkpoints()
    axis_one()
    axis_two()
    show_independent_overlap()
    show_future_version()
    print("\n演示结束。")


if __name__ == "__main__":
    main()
