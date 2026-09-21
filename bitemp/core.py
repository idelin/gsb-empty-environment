"""双时态（bitemporal）版本索引。

两条时间轴
----------
- 业务时间（valid time）：事件真实发生的区间 ``[valid_from, valid_to)``，左闭右开。
- 录入时间（transaction time）：证据在系统中生效的区间 ``[tx_from, tx_to)``，左闭右开。

查询输入为 ``(valid_time, as_of)``：只能看到 ``tx_from <= as_of < tx_to``
的版本，即“观察时刻之前（含当刻）已录入、且尚未被更正/撤销”的证据。

更正、撤销、补录都不改旧数据，只关闭旧版本的 ``tx_to`` 并追加新版本，
因此任何历史观察时刻的查询结果永远不被后续写入污染。
"""

from __future__ import annotations

import bisect
from dataclasses import dataclass, replace
from typing import Any, Iterable, Iterator

INF = float("inf")

__all__ = ["INF", "Version", "BitemporalStore", "resolve_winner"]


@dataclass(frozen=True)
class Version:
    """一条不可变的双时态版本记录。"""

    object_id: str
    valid_from: float  # 业务区间起点（含）
    valid_to: float  # 业务区间终点（不含），INF 表示开口
    payload: Any
    priority: int  # 重叠裁决用的业务优先级，大者优先
    record_id: str  # 全局唯一，调用方提供，裁决兜底键
    tx_from: float  # 录入生效时刻（含）
    tx_to: float  # 录入失效时刻（不含），INF 表示当前仍可信

    def covers(self, t: float) -> bool:
        return self.valid_from <= t < self.valid_to

    def visible_at(self, as_of: float) -> bool:
        return self.tx_from <= as_of < self.tx_to


def resolve_winner(candidates: Iterable[Version]) -> Version | None:
    """同对象、同一业务时刻、同一观察时刻下重叠版本的裁决。

    全序规则（只依赖记录内容，与到达/遍历顺序无关）：

    1. ``priority`` 大者优先（业务显式声明，例如“审计值压过抓取值”）；
    2. ``tx_from`` 晚者优先（同级证据里，后录入的信息更新）；
    3. ``record_id`` 字典序小者优先（确定性兜底；``record_id`` 全局唯一，
       因此该序是全序，任何输入顺序都得到同一结果）。
    """
    best: Version | None = None
    for v in candidates:
        if best is None:
            best = v
        elif (v.priority, v.tx_from) > (best.priority, best.tx_from):
            best = v
        elif (v.priority, v.tx_from) == (best.priority, best.tx_from) and (
            v.record_id < best.record_id
        ):
            best = v
    return best


class _ObjectIndex:
    """单对象的版本索引：record_id 字典 + 按业务起点排序的键列表。"""

    __slots__ = ("by_record", "_keys")

    def __init__(self) -> None:
        self.by_record: dict[str, Version] = {}  # 全部版本（含已关闭）
        self._keys: list[tuple[float, float, str]] = []  # (valid_from, valid_to, record_id) 有序

    def add(self, v: Version) -> None:
        self.by_record[v.record_id] = v
        bisect.insort(self._keys, (v.valid_from, v.valid_to, v.record_id))

    def close(self, record_id: str, at: float) -> Version:
        old = self.by_record[record_id]
        closed = replace(old, tx_to=at)
        self.by_record[record_id] = closed  # 排序键不含 tx_to，索引无需变动
        return closed

    def candidates_at(self, t: float, as_of: float) -> list[Version]:
        # 二分定位 valid_from <= t 的前缀，再按 valid_to 与 tx 窗口过滤
        cut = bisect.bisect_right(self._keys, (t, INF, "\U0010ffff"))
        out = []
        for _, valid_to, rid in self._keys[:cut]:
            if valid_to > t:
                v = self.by_record[rid]
                if v.visible_at(as_of):
                    out.append(v)
        return out

    def visible_versions(self, as_of: float) -> list[Version]:
        return [v for v in self.by_record.values() if v.visible_at(as_of)]

    def gc(self, horizon: float) -> int:
        dead = [rid for rid, v in self.by_record.items() if v.tx_to <= horizon]
        for rid in dead:
            del self.by_record[rid]
        if dead:
            self._keys = [k for k in self._keys if k[2] in self.by_record]
        return len(dead)


_KEEP = object()


class BitemporalStore:
    """双时态版本库：写入只追加/关闭，查询按 (对象, 业务时间, 观察时刻) 定位。"""

    def __init__(self) -> None:
        self._objects: dict[str, _ObjectIndex] = {}
        self._open: dict[str, str] = {}  # record_id -> object_id（仅 tx_to == INF）
        self._used_ids: set[str] = set()

    # ------------------------------------------------------------------ 写入

    def assert_version(
        self,
        object_id: str,
        valid_from: float,
        valid_to: float,
        payload: Any,
        *,
        at: float,
        priority: int = 0,
        record_id: str,
    ) -> Version:
        """录入一条新证据。``at`` 是录入生效时刻；业务区间可在过去（补录）或未来。"""
        if not valid_from < valid_to:
            raise ValueError(f"业务区间必须左闭右开且非空: [{valid_from}, {valid_to})")
        if record_id in self._used_ids:
            raise ValueError(f"record_id 重复: {record_id!r}")
        v = Version(
            object_id=object_id,
            valid_from=valid_from,
            valid_to=valid_to,
            payload=payload,
            priority=priority,
            record_id=record_id,
            tx_from=at,
            tx_to=INF,
        )
        idx = self._objects.setdefault(object_id, _ObjectIndex())
        idx.add(v)
        self._open[record_id] = object_id
        self._used_ids.add(record_id)
        return v

    def correct(
        self,
        record_id: str,
        payload: Any = _KEEP,
        *,
        at: float,
        new_record_id: str,
        valid_from: float | None = None,
        valid_to: float | None = None,
        priority: int | None = None,
    ) -> Version:
        """更正：关闭旧版本的 tx 区间，追加继承其字段（可覆盖）的新版本。"""
        old = self._close(record_id, at)
        return self.assert_version(
            old.object_id,
            old.valid_from if valid_from is None else valid_from,
            old.valid_to if valid_to is None else valid_to,
            old.payload if payload is _KEEP else payload,
            at=at,
            priority=old.priority if priority is None else priority,
            record_id=new_record_id,
        )

    def retract(self, record_id: str, *, at: float) -> Version:
        """撤销：只关闭 tx 区间，不留替代版本。返回被关闭的版本。"""
        return self._close(record_id, at)

    def _close(self, record_id: str, at: float) -> Version:
        object_id = self._open.get(record_id)
        if object_id is None:
            raise KeyError(f"record {record_id!r} 不存在或已被关闭")
        idx = self._objects[object_id]
        old = idx.by_record[record_id]
        if at < old.tx_from:
            raise ValueError(
                f"关闭时刻 {at} 早于该版本录入时刻 {old.tx_from}"
            )
        closed = idx.close(record_id, at)
        del self._open[record_id]
        return closed

    # ------------------------------------------------- 查询：时间轴一「何时发生了什么」

    def candidates(
        self, object_id: str, valid_time: float, *, as_of: float
    ) -> list[Version]:
        """该业务时刻、该观察时刻下全部可见版本（未裁决），按 record_id 排序。"""
        idx = self._objects.get(object_id)
        if idx is None:
            return []
        return sorted(
            idx.candidates_at(valid_time, as_of), key=lambda v: v.record_id
        )

    def query(
        self, object_id: str, valid_time: float, *, as_of: float
    ) -> Version | None:
        """「valid_time 时发生了什么」：按观察时刻 as_of 可见证据裁决后的结果。"""
        idx = self._objects.get(object_id)
        if idx is None:
            return None
        return resolve_winner(idx.candidates_at(valid_time, as_of))

    # ------------------------------------------------- 查询：时间轴二「当时我们知道什么」

    def snapshot(self, object_id: str, *, as_of: float) -> list[Version]:
        """观察时刻 as_of 下该对象全部可见版本，按业务区间排序。"""
        idx = self._objects.get(object_id)
        if idx is None:
            return []
        return sorted(
            idx.visible_versions(as_of),
            key=lambda v: (v.valid_from, v.valid_to, v.record_id),
        )

    def history(self, object_id: str) -> list[Version]:
        """该对象全部版本（含已关闭），按录入时间排序——审计用全量历史。"""
        idx = self._objects.get(object_id)
        if idx is None:
            return []
        return sorted(
            idx.by_record.values(),
            key=lambda v: (v.tx_from, v.valid_from, v.record_id),
        )

    def objects(self) -> list[str]:
        return sorted(self._objects)

    # ------------------------------------------------------------------ 回收

    def gc(self, horizon: float) -> int:
        """物理回收 ``tx_to <= horizon`` 的版本，返回回收条数。

        安全条件：``horizon`` 不晚于系统承诺服务的最早观察时刻。
        tx 区间左闭右开，``tx_to <= horizon <= as_of`` 的版本对任何
        ``as_of >= horizon`` 的查询本就不可见，回收不改变这类查询的结果；
        ``tx_to == INF`` 的当前版本永不回收。``as_of < horizon`` 的旧查询
        在回收后不再保证可重放。
        """
        return sum(idx.gc(horizon) for idx in self._objects.values())
