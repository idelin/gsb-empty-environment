"""双时间轴版本索引。

每个对象维护三列与 ``(时间, rid)`` 元组的有序数组，支持用二分快速取到
业务时间点 ``t``、观察时刻 ``as_of`` 下可见且覆盖 ``t`` 的候选记录：

    覆盖(t)    = {vstart <= t} - {vend <= t}     # 区间左闭右开
    可见(as_of) = {recorded_at <= as_of}
    候选 = 覆盖(t) 且 可见(as_of)

胜出规则复用 :func:`bitemporal.model.choose_winner`，因此结果不依赖记录
插入或遍历顺序。索引只追加、不改写历史。
"""

from __future__ import annotations

from bisect import bisect_right, insort
from dataclasses import dataclass
from typing import Any, Iterable, Optional

from .model import INF, Record, Op, Status, Segment, choose_winner

__all__ = ["AppendError", "LookupResult", "VersionIndex"]


class AppendError(ValueError):
    """追加记录违反不变量时抛出。"""


@dataclass(frozen=True, slots=True)
class LookupResult:
    object_id: Any
    t: Any
    as_of: Any
    status: Status
    value: Optional[Any] = None
    rid: Optional[str] = None

    def value_or(self, default: Any = None) -> Any:
        return self.value if self.status is Status.PRESENT else default


class _ObjIndex:
    __slots__ = ("starts", "ends", "recorded")

    def __init__(self) -> None:
        # 每个数组按 (时间键, rid) 字典序有序；时间并列时 rid 稳定破序
        self.starts: list[tuple[Any, str]] = []
        self.ends: list[tuple[Any, str]] = []
        self.recorded: list[tuple[Any, str]] = []

    def insert(self, rec: Record) -> None:
        insort(self.starts, (rec.vstart, rec.rid))
        if rec.vend is not INF:
            insort(self.ends, (rec.vend, rec.rid))
        insort(self.recorded, (rec.recorded_at, rec.rid))


def _prefix_ids(pairs: list[tuple[Any, str]], key: Any) -> set[str]:
    """取时间键 <= key 的 rid（用于左闭边界 vstart、录入时刻）。"""
    hi = bisect_right(pairs, key, key=lambda p: p[0])
    return {rid for _, rid in pairs[:hi]}


class VersionIndex:
    """追加式双时间轴版本索引。"""

    def __init__(self) -> None:
        self._records: dict[str, Record] = {}
        self._objects: dict[Any, _ObjIndex] = {}

    # ------------------------------------------------------------------ 写入

    def append(self, rec: Record) -> None:
        """校验并追加一条记录。"""
        if not isinstance(rec, Record):
            raise AppendError("expect a Record instance")
        if rec.rid in self._records:
            raise AppendError(f"duplicate rid: {rec.rid}")
        if not (rec.vstart < rec.vend):
            raise AppendError(f"invalid interval [{rec.vstart}, {rec.vend})")
        if rec.op is Op.RETRACT and rec.value is not None:
            raise AppendError("retraction must carry value=None")
        if rec.op is Op.ASSERT and rec.value is None:
            raise AppendError("assertion must carry a non-None value")
        if rec.replaces_id is not None:
            parent = self._records.get(rec.replaces_id)
            if parent is None:
                raise AppendError(f"replaces unknown rid: {rec.replaces_id}")
            if parent.object_id != rec.object_id:
                raise AppendError("cannot replace a record of another object")
            if (parent.vstart, parent.vend) != (rec.vstart, rec.vend):
                raise AppendError(
                    "correction/retraction must keep the same business interval"
                )
            if not (parent.recorded_at <= rec.recorded_at):
                raise AppendError("a correction cannot predate its predecessor")
        obj = self._objects.setdefault(rec.object_id, _ObjIndex())
        obj.insert(rec)
        self._records[rec.rid] = rec

    def append_many(self, records: Iterable[Record]) -> None:
        """批量追加；按输入顺序做链式校验。"""
        for rec in records:
            self.append(rec)

    def remove(self, rid: str) -> None:
        """物理删除一条记录（仅供归档回收使用，普通更正请用追加）。

        安全性由 :mod:`bitemporal.retention` 的可达性判定保证。
        """
        rec = self._records.pop(rid)
        obj = self._objects[rec.object_id]
        for pairs, key in (
            (obj.starts, rec.vstart),
            (obj.ends, rec.vend),
            (obj.recorded, rec.recorded_at),
        ):
            if pairs is obj.ends and rec.vend is INF:
                continue
            idx = bisect_right(pairs, (key, rid)) - 1
            if idx >= 0 and pairs[idx] == (key, rid):
                pairs.pop(idx)

    # ------------------------------------------------------------------ 读取

    def objects(self) -> list[Any]:
        return sorted(self._objects, key=repr)

    def get_record(self, rid: str) -> Record:
        return self._records[rid]

    def __contains__(self, rid: str) -> bool:
        return rid in self._records

    def __len__(self) -> int:
        return len(self._records)

    def _candidates(self, object_id: Any, t: Any, as_of: Any) -> list[Record]:
        obj = self._objects.get(object_id)
        if obj is None:
            return []
        started = _prefix_ids(obj.starts, t)          # vstart <= t
        ended = _prefix_ids(obj.ends, t)             # vend <= t（右开，需排除）
        seen = _prefix_ids(obj.recorded, as_of)      # recorded_at <= as_of
        ids = (started - ended) & seen
        return [self._records[r] for r in ids]

    def get(self, object_id: Any, t: Any, as_of: Any) -> LookupResult:
        """点查：业务时间 ``t`` 在观察时刻 ``as_of`` 的生效结果。"""
        winner = choose_winner(self._candidates(object_id, t, as_of))
        if winner is None:
            return LookupResult(object_id, t, as_of, Status.UNKNOWN)
        if winner.op is Op.RETRACT:
            return LookupResult(object_id, t, as_of, Status.RETRACTED,
                                None, winner.rid)
        return LookupResult(object_id, t, as_of, Status.PRESENT,
                            winner.value, winner.rid)

    def evidence(self, object_id: Any, t: Any, as_of: Any) -> list[Record]:
        """观察时刻前已录入且覆盖 ``t`` 的全部证据（含已被取代者）。"""
        rows = self._candidates(object_id, t, as_of)
        return sorted(rows, key=lambda r: (r.recorded_at, r.rid))

    def _visible_records(self, object_id: Any, as_of: Any) -> list[Record]:
        obj = self._objects.get(object_id)
        if obj is None:
            return []
        ids = _prefix_ids(obj.recorded, as_of)
        return [self._records[r] for r in ids]

    def timeline_as_of(self, object_id: Any, as_of: Any) -> list[Segment]:
        """轴二：固定观察时刻，返回该时刻所知的整条业务时间轴。

        只覆盖“至少有一条已录入证据”的业务时间范围；范围内部无证据的
        区段展开为 ``UNKNOWN`` 段。相邻等价段合并。开放终点延伸到 INF。
        """
        rows = self._visible_records(object_id, as_of)
        if not rows:
            return []
        points = sorted({r.vstart for r in rows} |
                        {r.vend for r in rows if r.vend is not INF})
        segs: list[Segment] = []
        for lo, hi in zip(points, points[1:]):
            mid = _midpoint(lo, hi)
            res = self.get(object_id, mid, as_of)
            segs.append(Segment(lo, hi, res.status, res.value, res.rid))
        if any(r.vend is INF for r in rows):
            tail_start = points[-1]
            res = self.get(object_id, tail_start, as_of)
            segs.append(Segment(tail_start, INF, res.status, res.value, res.rid))
        return _merge_segments(segs)

    def evolutions(self, object_id: Any, t: Any) -> list[tuple[Any, LookupResult]]:
        """轴一：固定业务时间 ``t``，沿观察时刻回放认知演化。

        返回 ``[(recorded_at, 结果), ...]``：只保留真正改变结果的录入
        时刻；首项之前对该业务时间的认知一律为 ``UNKNOWN``。
        """
        obj = self._objects.get(object_id)
        if obj is None:
            return []
        covering = {rid for rid in self._records
                    if self._records[rid].object_id == object_id
                    and self._records[rid].covers(t)}
        if not covering:
            return []
        times = sorted({self._records[r].recorded_at for r in covering})
        out: list[tuple[Any, LookupResult]] = []
        prev: Optional[LookupResult] = None
        for when in times:
            res = self.get(object_id, t, when)
            if prev is None or (res.status, res.value, res.rid) != (
                prev.status, prev.value, prev.rid
            ):
                out.append((when, res))
                prev = res
        return out


def _midpoint(lo: Any, hi: Any) -> Any:
    """区间内部探针：数值类型用中点；不支持算术时退回左端点（必在 [lo,hi)）。"""
    try:
        mid = (lo + hi) / 2
    except TypeError:
        return lo
    if not (lo <= mid < hi):  # 离散类型（如整数）同样安全
        return lo
    return mid


def _merge_segments(segs: list[Segment]) -> list[Segment]:
    merged: list[Segment] = []
    for seg in segs:
        if merged and merged[-1].vend == seg.vstart and (
            merged[-1].status, merged[-1].value, merged[-1].rid
        ) == (seg.status, seg.value, seg.rid):
            merged[-1] = Segment(merged[-1].vstart, seg.vend,
                                 seg.status, seg.value, seg.rid)
        else:
            merged.append(seg)
    return merged
