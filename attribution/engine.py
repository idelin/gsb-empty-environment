"""归因引擎：事件版本覆盖、窗口归因、版本化无向身份图、增量重算与全量对照。"""
from __future__ import annotations

from dataclasses import dataclass
from typing import Dict, List, Optional

TOUCHPOINT = "touchpoint"
CONVERSION = "conversion"
UNATTRIBUTED = "<unattributed>"


@dataclass(frozen=True)
class Event:
    event_id: str
    version: int
    subject: str
    time: int
    kind: str  # TOUCHPOINT | CONVERSION
    channel: Optional[str] = None
    amount: Optional[int] = None


@dataclass(frozen=True)
class Attribution:
    touchpoint_id: Optional[str]
    channel: Optional[str]
    amount: int


def event_from_op(op: dict) -> Event:
    kind = op.get("kind")
    if kind not in (TOUCHPOINT, CONVERSION):
        raise ValueError(f"未知事件类型: {kind!r}")
    amount = op.get("amount")
    if kind == CONVERSION and (not isinstance(amount, int) or isinstance(amount, bool)):
        raise ValueError("成交金额必须为整数")
    version = op.get("version")
    if not isinstance(version, int) or isinstance(version, bool) or version < 1:
        raise ValueError("事件版本必须为正整数")
    return Event(
        event_id=str(op["event_id"]),
        version=version,
        subject=str(op["subject"]),
        time=int(op["time"]),
        kind=kind,
        channel=op.get("channel"),
        amount=amount,
    )


def attribute_conversion(conv: Event, touchpoints, window: int) -> Attribution:
    """窗口 [conv.time - window, conv.time] 双闭区间内取最后触点，时间相同取触点ID最小。"""
    lo = conv.time - window
    best: Optional[Event] = None
    for tp in touchpoints:
        if lo <= tp.time <= conv.time:
            if (
                best is None
                or tp.time > best.time
                or (tp.time == best.time and tp.event_id < best.event_id)
            ):
                best = tp
    if best is None:
        return Attribution(None, None, conv.amount)
    return Attribution(best.event_id, best.channel, conv.amount)


class _UnionFind:
    def __init__(self):
        self.parent: Dict[str, str] = {}

    def find(self, x: str) -> str:
        parent = self.parent
        if x not in parent:
            parent[x] = x
            return x
        root = x
        while parent[root] != root:
            root = parent[root]
        while parent[x] != root:
            parent[x], x = root, parent[x]
        return root

    def union(self, a: str, b: str) -> None:
        ra, rb = self.find(a), self.find(b)
        if ra == rb:
            return
        if rb < ra:
            ra, rb = rb, ra
        self.parent[rb] = ra


def _conservation_record(op_kind: str, edge_key, before: dict, after: dict) -> dict:
    added, revoked = {}, {}
    for ch in sorted(set(before) | set(after)):
        delta = after.get(ch, 0) - before.get(ch, 0)
        if delta > 0:
            added[ch] = delta
        elif delta < 0:
            revoked[ch] = -delta
    return {
        "type": "identity_change",
        "op": op_kind,
        "edge": list(edge_key),
        "before": before,
        "after": after,
        "added": added,
        "revoked": revoked,
        "conserved": sum(before.values()) == sum(after.values()),
    }


class Engine:
    """增量归因引擎。每个操作只精准重算受影响的成交。"""

    def __init__(self, window: int):
        if window < 0:
            raise ValueError("窗口必须非负")
        self.window = window
        self.events: Dict[str, Event] = {}
        self.edges: Dict[tuple, int] = {}  # (a, b) 有序对 -> 边版本
        self.seq = 0
        self.history: List[tuple] = []  # (seq, op) 已应用操作，供旧版本查询
        self.attributions: Dict[str, Attribution] = {}

    # ---- 图结构 ----

    def _all_subjects(self):
        subs = {ev.subject for ev in self.events.values()}
        for a, b in self.edges:
            subs.add(a)
            subs.add(b)
        return subs

    def _union_find(self) -> _UnionFind:
        uf = _UnionFind()
        for s in self._all_subjects():
            uf.find(s)
        for a, b in self.edges:
            uf.union(a, b)
        return uf

    def _component_members(self, subject: str):
        uf = self._union_find()
        root = uf.find(subject)
        return {s for s in self._all_subjects() if uf.find(s) == root}

    def _conversions_in(self, subjects):
        return [
            ev
            for ev in self.events.values()
            if ev.kind == CONVERSION and ev.subject in subjects
        ]

    def _channel_totals(self, subjects) -> dict:
        totals: Dict[str, int] = {}
        for ev in self._conversions_in(subjects):
            att = self.attributions.get(ev.event_id)
            if att is not None:
                channel = att.channel if att.channel is not None else UNATTRIBUTED
                totals[channel] = totals.get(channel, 0) + att.amount
        return totals

    # ---- 重算 ----

    def _recompute(self, conv_ids) -> List[dict]:
        if not conv_ids:
            return []
        uf = self._union_find()
        touch_by_root: Dict[str, List[Event]] = {}
        for ev in self.events.values():
            if ev.kind == TOUCHPOINT:
                touch_by_root.setdefault(uf.find(ev.subject), []).append(ev)
        changes = []
        for cid in sorted(conv_ids):
            conv = self.events.get(cid)
            if conv is None or conv.kind != CONVERSION:
                new = None
            else:
                tps = touch_by_root.get(uf.find(conv.subject), [])
                new = attribute_conversion(conv, tps, self.window)
            old = self.attributions.get(cid)
            if old != new:
                if new is None:
                    del self.attributions[cid]
                else:
                    self.attributions[cid] = new
                changes.append(
                    {
                        "type": "attribution_change",
                        "conversion": cid,
                        "amount": new.amount if new else old.amount,
                        "old_channel": old.channel if old else None,
                        "new_channel": new.channel if new else None,
                        "old_touchpoint": old.touchpoint_id if old else None,
                        "new_touchpoint": new.touchpoint_id if new else None,
                    }
                )
        return changes

    # ---- 操作应用 ----

    def apply(self, op: dict) -> List[dict]:
        self.seq += 1
        kind = op.get("op")
        if kind == "event":
            changes = self._apply_event(op)
        elif kind == "add_edge":
            changes = self._apply_add_edge(op)
        elif kind == "remove_edge":
            changes = self._apply_remove_edge(op)
        else:
            raise ValueError(f"未知操作: {kind!r}")
        self.history.append((self.seq, op))
        for ch in changes:
            ch["seq"] = self.seq
        return changes

    def _apply_event(self, op: dict) -> List[dict]:
        ev = event_from_op(op)
        old = self.events.get(ev.event_id)
        if old is not None and ev.version <= old.version:
            return []  # 旧版本/重复版本：按版本覆盖规则忽略
        self.events[ev.event_id] = ev
        affected = set()
        if old is not None and old.kind == CONVERSION:
            affected.add(ev.event_id)
        if ev.kind == CONVERSION:
            affected.add(ev.event_id)
        times = []
        if ev.kind == TOUCHPOINT:
            times.append(ev.time)
        if old is not None and old.kind == TOUCHPOINT:
            times.append(old.time)
        if times:
            subjects = {ev.subject}
            if old is not None:
                subjects.add(old.subject)
            members = set()
            for s in subjects:
                members |= self._component_members(s)
            for conv in self._conversions_in(members):
                if any(conv.time - self.window <= t <= conv.time for t in times):
                    affected.add(conv.event_id)
        return self._recompute(affected)

    def _apply_add_edge(self, op: dict) -> List[dict]:
        a, b = str(op["a"]), str(op["b"])
        if a == b:
            return []
        key = (min(a, b), max(a, b))
        if key in self.edges:
            return []
        affected_subjects = self._component_members(a) | self._component_members(b)
        before = self._channel_totals(affected_subjects)
        self.edges[key] = int(op.get("version", self.seq))
        conv_ids = {c.event_id for c in self._conversions_in(affected_subjects)}
        changes = self._recompute(conv_ids)
        after = self._channel_totals(affected_subjects)
        changes.append(_conservation_record("add_edge", key, before, after))
        return changes

    def _apply_remove_edge(self, op: dict) -> List[dict]:
        a, b = str(op["a"]), str(op["b"])
        key = (min(a, b), max(a, b))
        if key not in self.edges:
            return []
        affected_subjects = self._component_members(a)
        before = self._channel_totals(affected_subjects)
        del self.edges[key]
        conv_ids = {c.event_id for c in self._conversions_in(affected_subjects)}
        changes = self._recompute(conv_ids)
        after = self._channel_totals(affected_subjects)
        changes.append(_conservation_record("remove_edge", key, before, after))
        return changes

    # ---- 旧版本查询 ----

    def query_at(self, seq: int) -> Dict[str, Attribution]:
        """按历史序号回放，保持当时的身份关系与事件版本。"""
        ops = [op for s, op in self.history if s <= seq]
        return full_recompute(ops, self.window)


def full_recompute(ops, window: int) -> Dict[str, Attribution]:
    """全量重算独立对照：从操作序列重建事件与身份图，逐成交独立归因。"""
    events: Dict[str, Event] = {}
    edges = set()
    for op in ops:
        kind = op.get("op")
        if kind == "event":
            ev = event_from_op(op)
            old = events.get(ev.event_id)
            if old is None or ev.version > old.version:
                events[ev.event_id] = ev
        elif kind == "add_edge":
            a, b = str(op["a"]), str(op["b"])
            if a != b:
                edges.add((min(a, b), max(a, b)))
        elif kind == "remove_edge":
            a, b = str(op["a"]), str(op["b"])
            edges.discard((min(a, b), max(a, b)))
        else:
            raise ValueError(f"未知操作: {kind!r}")
    uf = _UnionFind()
    subjects = {ev.subject for ev in events.values()}
    for a, b in edges:
        subjects.add(a)
        subjects.add(b)
    for s in subjects:
        uf.find(s)
    for a, b in edges:
        uf.union(a, b)
    touch_by_root: Dict[str, List[Event]] = {}
    for ev in events.values():
        if ev.kind == TOUCHPOINT:
            touch_by_root.setdefault(uf.find(ev.subject), []).append(ev)
    result: Dict[str, Attribution] = {}
    for ev in events.values():
        if ev.kind == CONVERSION:
            tps = touch_by_root.get(uf.find(ev.subject), [])
            result[ev.event_id] = attribute_conversion(ev, tps, window)
    return result
