"""Multi-resolution min/max/energy index over sample arrays (stdlib only).

Design
------
An immutable AVL concat tree (balanced rope) over the sample array.  Every
node caches an exact aggregate of its subtree: min, max, sum, sum of
squares, valid count, missing count.  Each tree level therefore acts as a
coarser resolution of the signal.  Aggregates keep extrema instead of
averages, so a single-sample spike stays visible at every level and is
never masked by smoothing.

Because nodes are immutable, every edit produces a new root while sharing
untouched subtrees with previous versions: undo is O(1) and the number of
node copies per edit proves the maintenance is local.

Declared latency
----------------
build                        O(n) time, ~2n node copies
range summary / energy       O(log n) node visits
peak source collection       O(log n + t) visits, t = tied source samples
cut / insert / replace       O(log n + k) node copies, k = edited samples
undo                         O(1)

Numeric tolerance
-----------------
min / max / counts           exact
energy / mean / rms          relative 1e-9 (summation order only)
resample                     absolute 1e-9 (exact rational positions,
                             linear interpolation between neighbours)
"""

from __future__ import annotations

import math
from dataclasses import dataclass
from fractions import Fraction
from typing import Iterable, Optional

__all__ = ["MultiResIndex", "Summary", "Bucket", "OpStats", "TOLERANCE"]

TOLERANCE = 1e-9


def _is_missing(value) -> bool:
    return value is None or (isinstance(value, float) and math.isnan(value))


def _leaf_agg(value) -> "Agg":
    if _is_missing(value):
        return Agg(missing=1)
    v = float(value)
    return Agg(lo=v, hi=v, total=v, sumsq=v * v, count=1, missing=0)


@dataclass(frozen=True)
class Agg:
    """Exact aggregate of a sample interval."""

    lo: float = math.inf
    hi: float = -math.inf
    total: float = 0.0
    sumsq: float = 0.0
    count: int = 0
    missing: int = 0

    @property
    def size(self) -> int:
        return self.count + self.missing

    def merge(self, other: "Agg") -> "Agg":
        if other.size == 0:
            return self
        if self.size == 0:
            return other
        return Agg(
            lo=min(self.lo, other.lo),
            hi=max(self.hi, other.hi),
            total=self.total + other.total,
            sumsq=self.sumsq + other.sumsq,
            count=self.count + other.count,
            missing=self.missing + other.missing,
        )


@dataclass(frozen=True)
class _Node:
    agg: Agg
    height: int
    left: Optional["_Node"] = None
    right: Optional["_Node"] = None
    value: object = None  # leaf payload (height == 1)


@dataclass(frozen=True)
class OpStats:
    """Work counters of the last public operation."""

    visits: int = 0  # nodes inspected by a query
    copies: int = 0  # nodes allocated by an edit


@dataclass(frozen=True)
class Summary:
    start: int
    end: int
    count: int            # valid samples in range
    missing: int          # missing (None/NaN) samples in range
    minimum: Optional[float]
    maximum: Optional[float]
    energy: float         # sum of squares over valid samples only
    mean: Optional[float]
    rms: Optional[float]
    first_index: Optional[int]   # boundary info: first covered sample
    last_index: Optional[int]    # boundary info: last covered sample
    covers_full_series: bool


@dataclass(frozen=True)
class Bucket:
    """One grid-aligned bucket; partial buckets are never treated as full."""

    start: int            # nominal bucket span start (grid aligned)
    end: int              # nominal bucket span end
    coverage: int         # samples actually inside the queried range
    partial: bool         # coverage < nominal span
    count: int
    missing: int
    minimum: Optional[float]
    maximum: Optional[float]
    energy: float


def _merge_ranges(indices) -> list:
    ranges = []
    for idx in indices:
        if ranges and idx == ranges[-1][1]:
            ranges[-1] = (ranges[-1][0], idx + 1)
        else:
            ranges.append((idx, idx + 1))
    return ranges


class MultiResIndex:
    """Versioned multi-resolution min/max/energy index."""

    def __init__(self, samples: Iterable[float] = (), sample_rate=Fraction(44100)):
        self.sample_rate = Fraction(sample_rate)
        self._copies = 0
        self._visits = 0
        self.last_op = OpStats()
        self._versions = [self._build(list(samples))]
        self.last_op = OpStats(visits=0, copies=self._copies)

    # ------------------------------------------------------------------
    # node constructors (every allocation is a counted copy)
    # ------------------------------------------------------------------
    def _leaf(self, value) -> _Node:
        self._copies += 1
        return _Node(_leaf_agg(value), 1, value=value)

    def _node(self, left: _Node, right: _Node) -> _Node:
        self._copies += 1
        return _Node(
            left.agg.merge(right.agg),
            max(left.height, right.height) + 1,
            left,
            right,
        )

    # ------------------------------------------------------------------
    # AVL join / split (standard concat-tree algorithms)
    # ------------------------------------------------------------------
    def _balanced(self, l: _Node, r: _Node) -> _Node:
        if l.height > r.height + 1:
            if l.left.height >= l.right.height:
                return self._node(l.left, self._node(l.right, r))
            lr = l.right
            return self._node(self._node(l.left, lr.left), self._node(lr.right, r))
        if r.height > l.height + 1:
            if r.right.height >= r.left.height:
                return self._node(self._node(l, r.left), r.right)
            rl = r.left
            return self._node(self._node(l, rl.left), self._node(rl.right, r.right))
        return self._node(l, r)

    def _join(self, l: Optional[_Node], r: Optional[_Node]) -> Optional[_Node]:
        if l is None:
            return r
        if r is None:
            return l
        if l.height > r.height + 1:
            return self._balanced(l.left, self._join(l.right, r))
        if r.height > l.height + 1:
            return self._balanced(self._join(l, r.left), r.right)
        return self._node(l, r)

    def _split(self, t: Optional[_Node], k: int):
        """Split into (first k samples, rest)."""
        if t is None or k <= 0:
            return None, t
        if k >= t.agg.size:
            return t, None
        if t.height == 1:
            return (t, None) if k >= 1 else (None, t)
        lsize = t.left.agg.size
        if k < lsize:
            a, b = self._split(t.left, k)
            return a, self._join(b, t.right)
        if k > lsize:
            a, b = self._split(t.right, k - lsize)
            return self._join(t.left, a), b
        return t.left, t.right

    def _build(self, values) -> Optional[_Node]:
        nodes = [self._leaf(v) for v in values]

        def rec(lo: int, hi: int) -> _Node:
            if hi - lo == 1:
                return nodes[lo]
            mid = (lo + hi) // 2
            return self._node(rec(lo, mid), rec(mid, hi))

        return rec(0, len(nodes)) if nodes else None

    # ------------------------------------------------------------------
    # basic properties
    # ------------------------------------------------------------------
    @property
    def root(self) -> Optional[_Node]:
        return self._versions[-1]

    @property
    def version(self) -> int:
        return len(self._versions) - 1

    @property
    def size(self) -> int:
        r = self.root
        return r.agg.size if r else 0

    def __len__(self) -> int:
        return self.size

    def node_count(self) -> int:
        count = 0
        stack = [self.root] if self.root else []
        while stack:
            t = stack.pop()
            count += 1
            if t.height > 1:
                stack.append(t.left)
                stack.append(t.right)
        return count

    def to_list(self) -> list:
        out = []
        stack = [self.root] if self.root else []
        while stack:
            t = stack.pop()
            if t.height == 1:
                out.append(t.value)
            else:
                stack.append(t.right)
                stack.append(t.left)
        return out

    # ------------------------------------------------------------------
    # work counters
    # ------------------------------------------------------------------
    def _snapshot(self):
        return self._visits, self._copies

    def _record(self, mark) -> None:
        v, c = self._snapshot()
        self.last_op = OpStats(visits=v - mark[0], copies=c - mark[1])

    # ------------------------------------------------------------------
    # edits: versioned interval replacement, insert, cut, undo
    # ------------------------------------------------------------------
    def replace(self, start: int, end: int, new_samples: Iterable[float]) -> int:
        n = self.size
        if not (0 <= start <= end <= n):
            raise ValueError(f"bad range [{start}, {end}) for size {n}")
        mark = self._snapshot()
        a, bc = self._split(self.root, start)
        _, c = self._split(bc, end - start)
        b = self._build(list(new_samples))
        self._versions.append(self._join(self._join(a, b), c))
        self._record(mark)
        return self.version

    def insert(self, pos: int, samples: Iterable[float]) -> int:
        return self.replace(pos, pos, samples)

    def cut(self, start: int, end: int) -> int:
        return self.replace(start, end, ())

    def undo(self) -> bool:
        if len(self._versions) <= 1:
            return False
        self._versions.pop()
        return True

    # ------------------------------------------------------------------
    # queries
    # ------------------------------------------------------------------
    def _query(self, t: _Node, lo: int, hi: int, base: int) -> Agg:
        self._visits += 1
        size = t.agg.size
        if hi <= base or lo >= base + size:
            return Agg()
        if lo <= base and base + size <= hi:
            return t.agg
        if t.height == 1:
            return t.agg
        mid = base + t.left.agg.size
        agg = Agg()
        if lo < mid:
            agg = self._query(t.left, lo, hi, base)
        if hi > mid:
            agg = agg.merge(self._query(t.right, lo, hi, mid))
        return agg

    def summary(self, start: int = 0, end: Optional[int] = None) -> Summary:
        n = self.size
        end = n if end is None else end
        if not (0 <= start <= end <= n):
            raise ValueError(f"bad range [{start}, {end}) for size {n}")
        mark = self._snapshot()
        if self.root is None or start == end:
            agg = Agg()
        else:
            agg = self._query(self.root, start, end, 0)
        self._record(mark)
        count = agg.count
        return Summary(
            start=start,
            end=end,
            count=count,
            missing=agg.missing,
            minimum=agg.lo if count else None,
            maximum=agg.hi if count else None,
            energy=agg.sumsq,
            mean=agg.total / count if count else None,
            rms=math.sqrt(agg.sumsq / count) if count else None,
            first_index=start if end > start else None,
            last_index=end - 1 if end > start else None,
            covers_full_series=(start == 0 and end == n),
        )

    def _collect(self, t: _Node, lo: int, hi: int, base: int,
                 target: float, kind: str, out: list) -> None:
        self._visits += 1
        size = t.agg.size
        if hi <= base or lo >= base + size or t.agg.count == 0:
            return
        if kind == "max" and t.agg.hi < target:
            return
        if kind == "min" and t.agg.lo > target:
            return
        if t.height == 1:
            out.append(base)
            return
        mid = base + t.left.agg.size
        self._collect(t.left, lo, hi, base, target, kind, out)
        self._collect(t.right, lo, hi, mid, target, kind, out)

    def peaks(self, start: int = 0, end: Optional[int] = None, kind: str = "max"):
        """Return (value, [(s, e), ...]) with every tied source range."""
        if kind not in ("max", "min"):
            raise ValueError("kind must be 'max' or 'min'")
        n = self.size
        end = n if end is None else end
        if not (0 <= start <= end <= n):
            raise ValueError(f"bad range [{start}, {end}) for size {n}")
        mark = self._snapshot()
        if self.root is None or start == end:
            self._record(mark)
            return None, []
        agg = self._query(self.root, start, end, 0)
        if agg.count == 0:
            self._record(mark)
            return None, []
        target = agg.hi if kind == "max" else agg.lo
        indices: list = []
        self._collect(self.root, start, end, 0, target, kind, indices)
        self._record(mark)
        return target, _merge_ranges(indices)

    def buckets(self, start: int, end: int, width: int) -> list:
        """Grid-aligned buckets; edge buckets are exact and flagged partial."""
        n = self.size
        if width <= 0:
            raise ValueError("width must be positive")
        if not (0 <= start <= end <= n):
            raise ValueError(f"bad range [{start}, {end}) for size {n}")
        out = []
        i = start // width
        while i * width < end:
            bs, be = i * width, (i + 1) * width
            cs, ce = max(bs, start), min(be, end)
            s = self.summary(cs, ce)
            out.append(Bucket(
                start=bs, end=be,
                coverage=ce - cs,
                partial=(ce - cs) < (be - bs),
                count=s.count, missing=s.missing,
                minimum=s.minimum, maximum=s.maximum,
                energy=s.energy,
            ))
            i += 1
        return out

    # ------------------------------------------------------------------
    # rational sample-rate change
    # ------------------------------------------------------------------
    def resample(self, num: int, den: int) -> list:
        """Rate * num/den; exact rational positions, linear interpolation."""
        num, den = int(num), int(den)
        if num <= 0 or den <= 0:
            raise ValueError("resample factors must be positive")
        src = self.to_list()
        n = len(src)
        if n == 0:
            return []
        out = []
        for i in range((n * num) // den):
            pos = Fraction(i * den, num)
            j = pos.numerator // pos.denominator
            if j >= n - 1:
                out.append(src[-1])
                continue
            a, b = src[j], src[j + 1]
            if _is_missing(a) or _is_missing(b):
                out.append(float("nan"))
                continue
            frac = float(pos - j)
            out.append(float(a) + (float(b) - float(a)) * frac)
        return out

    def change_rate(self, num: int, den: int) -> "MultiResIndex":
        return MultiResIndex(self.resample(num, den),
                             self.sample_rate * Fraction(num, den))
