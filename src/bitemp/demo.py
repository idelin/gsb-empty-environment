"""Query demo for both time axes.  Run:  python -m bitemp.demo

* 业务时间轴 "10 时发生了什么": fix the observation moment, sweep business
  time  -> index.timeline(object, t_tx)
* 录入时间轴 "当时我们知道什么": fix the business instant, sweep the
  observation moment -> index.knowledge(object, t_valid, observations)
"""

from __future__ import annotations

from .fixtures import full_fixture, prompt_scenario
from .index import BitemporalIndex
from .model import INF, Version


def _fmt(v: Version | None) -> str:
    if v is None:
        return "<无记录>"
    if v.is_tombstone:
        return "<已撤销>"
    return repr(v.value)


def _fmt_end(x: float) -> str:
    return "inf" if x == INF else f"{x:g}"


def main() -> None:
    print("== 需求示例：发生于 10，20 录入为 A，30 更正为 B ==")
    idx = BitemporalIndex(prompt_scenario())
    a25 = idx.value_at("acct-1", 10, 25)
    a35 = idx.value_at("acct-1", 10, 35)
    print(f"  观察时刻 25 查询业务时刻 10 -> {a25!r}  (期望 'A')")
    print(f"  观察时刻 35 查询业务时刻 10 -> {a35!r}  (期望 'B')")
    assert a25 == "A" and a35 == "B"

    idx = BitemporalIndex(full_fixture())

    print("\n== 业务时间轴：截至观察时刻 45，acct-1 发生了什么 ==")
    for s in idx.timeline("acct-1", 45):
        print(f"  [{s.valid_from:g}, {_fmt_end(s.valid_to)}) -> {_fmt(s.version)}")
    print("  (tx=40 的追溯撤销生效：业务时刻 10 起变为'已撤销')")

    print("\n== 录入时间轴：关于业务时刻 10，各观察时刻我们知道什么 ==")
    for obj in ("acct-1", "acct-2"):
        print(f"  {obj}:")
        for tx, v in idx.knowledge(obj, 10, [15, 21, 23, 25, 35, 45]):
            print(f"    观察时刻 {tx:>2} -> {_fmt(v)}")
    assert idx.value_at("acct-1", 10, 35) == "B"      # 旧查询不被撤销污染
    assert idx.value_at("acct-1", 10, 45) is None     # 追溯撤销生效
    assert idx.value_at("acct-2", 10, 21) == "A"      # 多次更正各自可见
    assert idx.value_at("acct-2", 10, 23) == "A'"
    assert idx.value_at("acct-2", 10, 25) == "A''"

    print("\n== 未来版本 / 相接区间 / 补录 ==")
    print(f"  acct-3 业务时刻 150 @观察 26 -> {_fmt(idx.query('acct-3', 150, 26))}")
    print(f"  acct-3 业务时刻 150 @观察 24 -> {_fmt(idx.query('acct-3', 150, 24))}")
    print(f"  acct-4 业务时刻  9  @观察 10 -> {_fmt(idx.query('acct-4', 9, 10))}")
    print(f"  acct-4 业务时刻 10  @观察 10 -> {_fmt(idx.query('acct-4', 10, 10))}"
          "  (左闭右开：边界 10 属于后一段)")
    print(f"  acct-5 业务时刻  6  @观察 49 -> {_fmt(idx.query('acct-5', 6, 49))}")
    print(f"  acct-5 业务时刻  6  @观察 51 -> {_fmt(idx.query('acct-5', 6, 51))}"
          "  (tx=50 才补录)")
    assert idx.value_at("acct-3", 150, 26) == "future"
    assert idx.query("acct-3", 150, 24) is None
    assert idx.value_at("acct-4", 9, 10) == "p"
    assert idx.value_at("acct-4", 10, 10) == "q"
    assert idx.query("acct-5", 6, 49) is None
    assert idx.value_at("acct-5", 6, 51) == "old"

    print("\n全部演示断言通过。")


if __name__ == "__main__":
    main()
