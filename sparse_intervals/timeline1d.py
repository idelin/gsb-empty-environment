"""一维稀疏区间冲突修复：规范时间线引擎。

每条规则带优先级、版本与半开时间区间 [start, end)。
同一时刻多条规则覆盖时，生效规则 = 优先级最高、其次版本最高、最后 ID 最小。
时间线按"生效值"合并相邻段，输出最少段数的规范时间线。
撤销规则时仅在被撤销规则的区间内做局部重算（增量更新），并返回受影响区间。
"""

from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any, Hashable, Iterable, Optional


@dataclass(frozen=True)
class Rule:
    """一条一维区间配置。区间为半开 [start, end)。"""

    rid: str
    priority: int
    version: int
    start: float
    end: float
    value: Hashable

    def __post_init__(self) -> None:
        if not self.start < self.end:
            raise ValueError(
                f"rule {self.rid!r}: empty interval [{self.start}, {self.end})"
            )

    def rank_key(self) -> tuple:
        # 排序键越小越优先：优先级高者优先，其次版本高者，最后 ID 小者。
        return (-self.priority, -self.version, self.rid)


@dataclass
class Segment:
    """规范时间线上的一段。[start, end) 内生效值恒为 value。

    winners:  在本段某处胜出的规则 ID（段由同值合并而来，可能不止一个）。
    shadowed: 覆盖本段某处、但在该处未胜出的规则 ID（被遮蔽规则，用于解释）。
    """

    start: float
    end: float
    value: Any
    winners: tuple = ()
    shadowed: tuple = ()

    def to_dict(self, explain: bool = False) -> dict:
        d = {"start": self.start, "end": self.end, "value": self.value}
        if explain:
            d["winners"] = list(self.winners)
            d["shadowed"] = list(self.shadowed)
        return d


def _merge_append(out: list, seg: Segment) -> None:
    """同值相邻段合并（保证最少段数）。"""
    if out and out[-1].value == seg.value and out[-1].end == seg.start:
        m = out[-1]
        m.end = seg.end
        m.winners = tuple(dict.fromkeys(m.winners + seg.winners))
        m.shadowed = tuple(dict.fromkeys(m.shadowed + seg.shadowed))
    else:
        out.append(Segment(seg.start, seg.end, seg.value, seg.winners, seg.shadowed))


class Timeline:
    """一维规范时间线。支持增量撤销并返回受影响区间。"""

    def __init__(self, rules: Iterable[Rule] = ()) -> None:
        self._rules: dict[str, Rule] = {}
        self._segments: Optional[list[Segment]] = None
        for r in rules:
            self.add(r)

    # ---- 规则管理 ----

    def add(self, rule: Rule) -> None:
        if rule.rid in self._rules:
            raise ValueError(f"duplicate rule id: {rule.rid!r}")
        self._rules[rule.rid] = rule
        self._segments = None  # 全量失效，惰性重建

    def rule_ids(self) -> list:
        return sorted(self._rules)

    # ---- 扫描 ----

    @staticmethod
    def _sweep(rules: Iterable[Rule], lo: float, hi: float) -> list:
        """对 [lo, hi) 扫描，返回按值合并后的 Segment 列表。"""
        clipped = []
        for r in rules:
            s = r.start if r.start > lo else lo
            e = r.end if r.end < hi else hi
            if s < e:
                clipped.append((s, e, r))
        bounds = {lo, hi}
        for s, e, _ in clipped:
            bounds.add(s)
            bounds.add(e)
        pts = sorted(bounds)
        out: list[Segment] = []
        for a, b in zip(pts, pts[1:]):
            active = [r for (s, e, r) in clipped if s <= a and e >= b]
            active.sort(key=Rule.rank_key)
            winner = active[0] if active else None
            value = winner.value if winner is not None else None
            winners = (winner.rid,) if winner is not None else ()
            shadowed = tuple(r.rid for r in active[1:])
            _merge_append(out, Segment(a, b, value, winners, shadowed))
        return out

    def segments(self) -> list:
        """返回当前规范时间线（Segment 列表的拷贝）。"""
        if self._segments is None:
            rules = list(self._rules.values())
            if not rules:
                self._segments = []
            else:
                lo = min(r.start for r in rules)
                hi = max(r.end for r in rules)
                self._segments = self._sweep(rules, lo, hi)
        return [Segment(s.start, s.end, s.value, s.winners, s.shadowed)
                for s in self._segments]

    # ---- 增量撤销 ----

    def revoke(self, rid: str) -> list:
        """撤销规则，仅在其区间内局部重算，返回受影响区间列表。

        返回元素: {"start", "end", "old_value", "new_value"}。
        区间外的缓存段原样保留（不重新计算、不复制其生效规则集）。
        """
        rule = self._rules.get(rid)
        if rule is None:
            raise KeyError(f"unknown rule id: {rid!r}")
        old = self.segments()  # 撤销前的完整时间线（含被撤销规则）
        del self._rules[rid]
        lo, hi = rule.start, rule.end

        # 局部重算：仅 [lo, hi) 窗口，使用剩余规则。
        local = self._sweep(list(self._rules.values()), lo, hi)

        # 拼接：窗口外的旧段保留（被撤销规则在窗外不覆盖，结果不变），
        # 跨越窗口边界的旧段在边界处切分，局部重算段插入中间，
        # 跨缝同值相邻段自动合并。
        left: list[Segment] = []
        right: list[Segment] = []
        for s in old:
            if s.end <= lo:
                left.append(s)
            elif s.start >= hi:
                right.append(s)
            else:
                # 跨越窗口边界的段被切开：被撤销规则不覆盖切下的小段，
                # 用剩余规则对其局部重扫，刷新 winners/shadowed 解释信息。
                if s.start < lo:
                    left.extend(self._sweep(list(self._rules.values()),
                                            s.start, lo))
                if s.end > hi:
                    right.extend(self._sweep(list(self._rules.values()),
                                             hi, s.end))
        combined: list[Segment] = []
        for s in left:
            _merge_append(combined, s)
        for s in local:
            _merge_append(combined, s)
        for s in right:
            _merge_append(combined, s)
        # 修剪两端无覆盖段：全量重建的时间线跨度为 [min start, max end)，
        # 撤销后窗口边缘可能残留超出该跨度的 None 段，需与之对齐。
        while combined and combined[0].value is None:
            combined.pop(0)
        while combined and combined[-1].value is None:
            combined.pop()
        self._segments = combined

        return _diff_ranges(old, local, lo, hi)


def _value_at(segs: Iterable[Segment], pos: float) -> Any:
    for s in segs:
        if s.start <= pos < s.end:
            return s.value
    return None


def _diff_ranges(old: list, new_window: list, lo: float, hi: float) -> list:
    """比较窗口 [lo, hi) 内新旧时间线，返回生效值发生变化的区间。"""
    bounds = {lo, hi}
    for s in old:
        if s.end > lo and s.start < hi:
            bounds.add(max(s.start, lo))
            bounds.add(min(s.end, hi))
    for s in new_window:
        bounds.add(s.start)
        bounds.add(s.end)
    pts = sorted(bounds)
    out: list[dict] = []
    for a, b in zip(pts, pts[1:]):
        if b <= lo or a >= hi:
            continue
        mid = (a + b) / 2
        ov = _value_at(old, mid)
        nv = _value_at(new_window, mid)
        if ov != nv:
            if (out and out[-1]["end"] == a
                    and out[-1]["old_value"] == ov
                    and out[-1]["new_value"] == nv):
                out[-1]["end"] = b
            else:
                out.append({"start": a, "end": b,
                            "old_value": ov, "new_value": nv})
    return out
