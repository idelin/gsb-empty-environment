"""不依赖索引的参考查询：对追加日志做朴素线性扫描。

这里故意不读取索引的任何内部结构，只按定义逐行过滤记录，作为随机版本
序列差异测试的“标准答案”。规则与 :mod:`bitemporal.index` 完全一致：

* 候选 = 覆盖业务点 ``t``（``vstart <= t < vend``）且 ``recorded_at <= as_of``；
* 被可见候选直接取代的记录先剔除（链式）；
* 链头按 ``(recorded_at, rid)`` 最大者胜出；
* 胜出者是撤销则结果为 RETRACTED，无候选则 UNKNOWN。
"""

from __future__ import annotations

from typing import Any, Iterable, Optional, Sequence

from .index import _midpoint, _merge_segments  # 仅复用纯函数，不触碰索引结构
from .model import INF, Record, Op, Status, Segment, choose_winner

__all__ = ["ref_get", "ref_timeline_as_of", "ref_evolutions", "LookupResultLike"]


def ref_get(
    log: Sequence[Record], object_id: Any, t: Any, as_of: Any
):
    """线性扫描版点查。返回与索引相同的 LookupResult。"""
    from .index import LookupResult

    candidates = [
        r for r in log
        if r.object_id == object_id and r.visible(as_of) and r.covers(t)
    ]
    winner = choose_winner(candidates)
    if winner is None:
        return LookupResult(object_id, t, as_of, Status.UNKNOWN)
    if winner.op is Op.RETRACT:
        return LookupResult(object_id, t, as_of, Status.RETRACTED,
                            None, winner.rid)
    return LookupResult(object_id, t, as_of, Status.PRESENT,
                        winner.value, winner.rid)


def ref_timeline_as_of(
    log: Sequence[Record], object_id: Any, as_of: Any
) -> list[Segment]:
    """线性扫描版：固定观察时刻的业务时间轴。"""
    rows = [r for r in log
            if r.object_id == object_id and r.visible(as_of)]
    if not rows:
        return []
    points = sorted({r.vstart for r in rows} |
                    {r.vend for r in rows if r.vend is not INF})
    segs: list[Segment] = []
    for lo, hi in zip(points, points[1:]):
        res = ref_get(log, object_id, _midpoint(lo, hi), as_of)
        segs.append(Segment(lo, hi, res.status, res.value, res.rid))
    if any(r.vend is INF for r in rows):
        tail_start = points[-1]
        res = ref_get(log, object_id, tail_start, as_of)
        segs.append(Segment(tail_start, INF, res.status, res.value, res.rid))
    return _merge_segments(segs)


def ref_evolutions(
    log: Sequence[Record], object_id: Any, t: Any
) -> list[tuple[Any, object]]:
    """线性扫描版：固定业务时间，结果随观察时刻的演化。"""
    covering = [r for r in log
                if r.object_id == object_id and r.covers(t)]
    if not covering:
        return []
    out: list[tuple[Any, object]] = []
    prev = None
    for when in sorted({r.recorded_at for r in covering}):
        res = ref_get(log, object_id, t, when)
        key = (res.status, res.value, res.rid)
        if prev is None or key != prev:
            out.append((when, res))
            prev = key
    return out
