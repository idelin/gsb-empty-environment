"""两种时间轴的查询演示：``python -m bitemp.demo``"""

from __future__ import annotations

from .core import BitemporalStore, Version
from .fixtures import build_demo_store


def _fmt(v: Version | None) -> str:
    if v is None:
        return "(无可见证据)"
    return (
        f"{v.payload!r}  [valid [{v.valid_from}, {v.valid_to})"
        f" tx [{v.tx_from}, {v.tx_to}) record={v.record_id}]"
    )


def axis_valid_time(s: BitemporalStore) -> None:
    print("=" * 72)
    print("时间轴一：「10 时发生了什么」——固定业务时间 t=10，移动观察时刻 as_of")
    print("=" * 72)
    for as_of in (15, 25, 35, 45, 55):
        v = s.query("order-1", 10, as_of=as_of)
        print(f"  as_of={as_of:>3}  ->  {_fmt(v)}")


def axis_knowledge(s: BitemporalStore) -> None:
    print()
    print("=" * 72)
    print("时间轴二：「当时我们知道什么」——固定观察时刻 as_of，展开可见知识")
    print("=" * 72)
    for as_of in (25, 35, 45, 55):
        print(f"  as_of={as_of:>3} 时，order-1 的全部可见版本:")
        snap = s.snapshot("order-1", as_of=as_of)
        if not snap:
            print("      (一无所知)")
        for v in snap:
            print(f"      {_fmt(v)}")


def extra_scenarios(s: BitemporalStore) -> None:
    print()
    print("=" * 72)
    print("边界与裁决场景")
    print("=" * 72)
    print("相接区间 meter-1（录入时刻 5）:")
    for t in (19, 20, 29, 30):
        print(f"  t={t:>2}  ->  {_fmt(s.query('meter-1', t, as_of=10))}")
    print("多次更正 acct-1（旧观察时刻不被最新值污染）:")
    for as_of in (15, 25, 35, 45):
        print(f"  as_of={as_of:>3}  ->  {_fmt(s.query('acct-1', 5, as_of=as_of))}")
    print("重叠优先级 claim-1（as_of=20）:")
    for t in (15, 25, 40):
        print(f"  t={t:>2}  ->  {_fmt(s.query('claim-1', t, as_of=20))}")


def main() -> None:
    s = build_demo_store()
    axis_valid_time(s)
    axis_knowledge(s)
    extra_scenarios(s)
    print()
    print("order-1 全量双时态历史（审计视角，含已关闭版本）:")
    for v in s.history("order-1"):
        print(f"  {_fmt(v)}")


if __name__ == "__main__":
    main()
