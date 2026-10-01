"""一维稀疏区间冲突修复。

规则模型：
- 每条配置绑定一个 key，具有 priority、version、半开区间 [start, end) 与输出值 value。
- 同一 key 上多条配置重叠时，生效者按如下顺序选择：
  priority 高者优先 -> version 高者优先 -> id 小者（字典序）优先。
- 规范时间线按“输出值”合并相邻原子区间：只要相邻两段的有效值相等
  （即使生效配置不同），就合并为一段，从而保证段数最少。
- 撤销配置时仅在该配置自身区间内做增量重算，返回受影响区间；
  缓存中不属于受影响范围的段保持不变，受影响范围内不保留过期段。
"""
from __future__ import annotations

from dataclasses import dataclass
from typing import Any, Hashable

_UNCOVERED = object()  # 内部哨兵：某原子区间无任何配置覆盖


@dataclass(frozen=True)
class Config:
    id: str
    key: str
    priority: int
    version: int
    start: float
    end: float
    value: Hashable

    def __post_init__(self) -> None:
        if not self.start < self.end:
            raise ValueError(f"config {self.id!r}: 需要 start < end")

    def rank(self) -> tuple:
        # 升序元组，min() 即最优：priority 高、version 高、id 小
        return (-self.priority, -self.version, self.id)


@dataclass(frozen=True)
class Segment:
    start: float
    end: float
    value: Hashable


@dataclass(frozen=True)
class AffectedInterval:
    key: str
    start: float
    end: float
    old_value: Any  # None 表示此前无覆盖
    new_value: Any  # None 表示此后无覆盖


@dataclass(frozen=True)
class Explanation:
    start: float
    end: float
    value: Hashable
    winner: str
    shadowed: tuple  # 被遮蔽的配置 id，按 rank 从强到弱排列


def _canon(atomics):
    """把 (start, end, value|_UNCOVERED) 原子序列合并为最少段数的规范段。"""
    segs: list[Segment] = []
    for a, b, v in atomics:
        if v is _UNCOVERED:
            continue
        if segs and segs[-1].end == a and segs[-1].value == v:
            segs[-1] = Segment(segs[-1].start, b, v)
        else:
            segs.append(Segment(a, b, v))
    return segs


def _coverage_list(segs, lo, hi):
    """把段列表在 [lo, hi) 内展开为 (a, b, value|None) 序列。"""
    pts = {lo, hi}
    for s in segs:
        if s.end > lo and s.start < hi:
            pts.add(max(s.start, lo))
            pts.add(min(s.end, hi))
    sp = sorted(pts)
    out = []
    for a, b in zip(sp, sp[1:]):
        v = None
        for s in segs:
            if s.start <= a and s.end >= b:
                v = s.value
                break
        out.append((a, b, v))
    return out


def _diff(key, old, new, lo, hi):
    """比较 [lo, hi) 内新旧段，返回受影响区间（相邻同型变化合并）。"""
    oc = _coverage_list(old, lo, hi)
    nc = _coverage_list(new, lo, hi)
    pts = sorted({p for a, b, _ in oc + nc for p in (a, b)})

    def val_at(cov, a, b):
        for x, y, v in cov:
            if x <= a and y >= b:
                return v
        return None

    res: list[AffectedInterval] = []
    for a, b in zip(pts, pts[1:]):
        ov, nv = val_at(oc, a, b), val_at(nc, a, b)
        if ov != nv:
            if res and res[-1].end == a and res[-1].old_value == ov and res[-1].new_value == nv:
                res[-1] = AffectedInterval(key, res[-1].start, b, ov, nv)
            else:
                res.append(AffectedInterval(key, a, b, ov, nv))
    return res


class Timeline:
    """按 key 组织的增量规范时间线。"""

    def __init__(self) -> None:
        self._configs: dict[str, Config] = {}
        self._keys: dict[str, set[str]] = {}
        self._segments: dict[str, list[Segment]] = {}

    # ---- 写入 ----

    def add(self, config: Config) -> list[AffectedInterval]:
        if config.id in self._configs:
            raise ValueError(f"重复的配置 id: {config.id!r}")
        self._configs[config.id] = config
        self._keys.setdefault(config.key, set()).add(config.id)
        return self._recompute_range(config.key, config.start, config.end)

    def revoke(self, config_id: str) -> list[AffectedInterval]:
        if config_id not in self._configs:
            raise KeyError(f"未知配置 id: {config_id!r}")
        cfg = self._configs.pop(config_id)
        self._keys[cfg.key].discard(config_id)
        # 增量：只在该配置自身区间内重算，区间外的缓存段不动
        return self._recompute_range(cfg.key, cfg.start, cfg.end)

    # ---- 读取 ----

    def segments(self, key: str) -> list[Segment]:
        return list(self._segments.get(key, []))

    def value_at(self, key: str, t: float):
        """从配置出发的暴力点查询（ ground truth ）。"""
        covering = [
            self._configs[i]
            for i in self._keys.get(key, ())
            if self._configs[i].start <= t < self._configs[i].end
        ]
        if not covering:
            return None
        return min(covering, key=Config.rank).value

    def explain(self, key: str) -> list[Explanation]:
        """逐原子区间给出生效配置与被遮蔽配置列表。"""
        cfgs = [self._configs[i] for i in self._keys.get(key, ())]
        if not cfgs:
            return []
        pts = sorted({p for c in cfgs for p in (c.start, c.end)})
        out: list[Explanation] = []
        for a, b in zip(pts, pts[1:]):
            covering = sorted(
                (c for c in cfgs if c.start <= a and c.end >= b),
                key=Config.rank,
            )
            if not covering:
                continue
            winner = covering[0]
            shadowed = tuple(c.id for c in covering[1:])
            if (
                out
                and out[-1].end == a
                and out[-1].value == winner.value
                and out[-1].winner == winner.id
                and out[-1].shadowed == shadowed
            ):
                out[-1] = Explanation(out[-1].start, b, winner.value, winner.id, shadowed)
            else:
                out.append(Explanation(a, b, winner.value, winner.id, shadowed))
        return out

    # ---- 内部 ----

    def _atomics(self, key, lo, hi):
        cfgs = [self._configs[i] for i in self._keys.get(key, ())]
        pts = {lo, hi}
        for c in cfgs:
            if c.end > lo and c.start < hi:
                pts.add(max(c.start, lo))
                pts.add(min(c.end, hi))
        sp = sorted(pts)
        out = []
        for a, b in zip(sp, sp[1:]):
            covering = [c for c in cfgs if c.start <= a and c.end >= b]
            if covering:
                out.append((a, b, min(covering, key=Config.rank).value))
            else:
                out.append((a, b, _UNCOVERED))
        return out

    def _recompute_range(self, key, lo, hi) -> list[AffectedInterval]:
        old = self._segments.get(key, [])
        new_inner = _canon(self._atomics(key, lo, hi))
        kept: list[Segment] = []
        for s in old:
            if s.end <= lo or s.start >= hi:
                kept.append(s)
            else:
                if s.start < lo:
                    kept.append(Segment(s.start, lo, s.value))
                if s.end > hi:
                    kept.append(Segment(hi, s.end, s.value))
        combined = sorted(kept + new_inner, key=lambda s: (s.start, s.end))
        final: list[Segment] = []
        for s in combined:
            if final and final[-1].end == s.start and final[-1].value == s.value:
                final[-1] = Segment(final[-1].start, s.end, s.value)
            else:
                final.append(s)
        self._segments[key] = final
        return _diff(key, old, final, lo, hi)
