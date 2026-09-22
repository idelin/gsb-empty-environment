"""合成夹具：覆盖题目要求的全部场景。

时间用整数表示（相对刻度），不影响规则本身；换成 datetime 同样成立。
每个场景返回一组追加日志 ``Record``，可喂给索引或参考实现。
"""

from __future__ import annotations

from dataclasses import dataclass
from typing import Sequence

from .model import INF, Record, Op

__all__ = ["SCENARIOS", "scenario_basic", "scenario_retroactive_retraction",
           "scenario_adjacent", "scenario_multi_correction",
           "scenario_independent_overlap", "scenario_future_version",
           "scenario_backfill", "scenario_unknown_then_retract"]


@dataclass(frozen=True)
class Scenario:
    name: str
    description: str
    records: tuple[Record, ...]


def _r(rid, obj, v0, v1, at, op=Op.ASSERT, value=None, replaces=None):
    return Record(rid=rid, object_id=obj, vstart=v0, vend=v1,
                  recorded_at=at, op=op, value=value, replaces_id=replaces)


def scenario_basic() -> Scenario:
    """10 时发生的值，20 录入 A，30 更正为 B：25 看仍 A，35 看 B。"""
    recs = (
        _r("s1", "sensor", 10, 20, 20, value="A"),
        _r("s2", "sensor", 10, 20, 30, value="B", replaces="s1"),
    )
    return Scenario("basic", "10点发生：20录入A、30更正B", recs)


def scenario_retroactive_retraction() -> Scenario:
    """40 录入的撤销，追溯抹掉 10 点在新观察时刻的认知。"""
    recs = scenario_basic().records + (
        _r("s3", "sensor", 10, 20, 40, Op.RETRACT, replaces="s2"),
    )
    return Scenario("retroactive_retraction", "40时追溯撤销10点的值", recs)


def scenario_adjacent() -> Scenario:
    """相接区间 [0,10) 与 [10,20)：左闭右开，边界 10 属于后段。"""
    recs = (
        _r("c1", "contract", 0, 10, 12, value="X"),
        _r("c2", "contract", 10, 20, 12, value="Y"),
    )
    return Scenario("adjacent", "相接区间 [0,10)/[10,20)", recs)


def scenario_multi_correction() -> Scenario:
    """同一记录被多次更正：A -> B -> C，旧观察时刻逐个回放。"""
    recs = (
        _r("l1", "ledger", 10, 20, 20, value=100),
        _r("l2", "ledger", 10, 20, 30, value=110, replaces="l1"),
        _r("l3", "ledger", 10, 20, 40, value=95, replaces="l2"),
    )
    return Scenario("multi_correction", "同一记录连续两次更正", recs)


def scenario_independent_overlap() -> Scenario:
    """两条无取代关系的重叠记录：按 (recorded_at, rid) 确定胜出，不靠到达顺序。"""
    recs = (
        _r("m1", "meter", 10, 20, 20, value=1),
        _r("m2", "meter", 10, 20, 30, value=2),  # 独立晚到证据
    )
    return Scenario("independent_overlap", "独立重叠记录的确定性破并列", recs)


def scenario_future_version() -> Scenario:
    """观察时刻 25 时看不到“未来版本”（recorded_at=50）。"""
    recs = (
        _r("p1", "plan", 0, INF, 10, value="old"),
        _r("p2", "plan", 0, INF, 50, value="new", replaces="p1"),
    )
    return Scenario("future_version", "更正记录录入时刻晚于观察时刻", recs)


def scenario_backfill() -> Scenario:
    """补录：25 补录 [0,10) 历史值，之前对该区间一无所知。"""
    recs = (
        _r("b1", "backfill", 0, 10, 25, value="late-A"),
        _r("b2", "backfill", 10, 20, 20, value="on-time"),
    )
    return Scenario("backfill", "晚到的历史区间补录", recs)


def scenario_unknown_then_retract() -> Scenario:
    """先有值、再撤销；再对相邻区间录入，覆盖 unknown/present 混合分段。"""
    recs = (
        _r("u1", "usage", 0, 10, 5, value="a"),
        _r("u2", "usage", 0, 10, 15, Op.RETRACT, replaces="u1"),
        _r("u3", "usage", 10, 20, 15, value="b"),
    )
    return Scenario("unknown_then_retract", "撤销后留空，相邻段有值", recs)


def _all() -> tuple[Scenario, ...]:
    return (
        scenario_basic(),
        scenario_retroactive_retraction(),
        scenario_multi_correction(),
        scenario_independent_overlap(),
        scenario_future_version(),
        scenario_backfill(),
        scenario_adjacent(),
        scenario_unknown_then_retract(),
    )


SCENARIOS: dict[str, Scenario] = {s.name: s for s in _all()}


def all_records() -> list[Record]:
    out: list[Record] = []
    for sc in _all():
        out.extend(sc.records)
    return out
