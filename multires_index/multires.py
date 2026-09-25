"""Persistent multi-resolution min/max/energy index (Python 3.11, stdlib only).

The index is an immutable binary pyramid.  Internally the timeline is a
canonical sequence of complete power-of-two trees (one tree per set bit of the
total length).  Every node stores:

    minimum, maximum, energy (sum of squared samples), present/missing counts.

Min and max are tracked independently of any average, so a one-sample spike
can never be hidden by a mean.  A range query decomposes [lo, hi) into aligned
dyadic nodes that are all *fully inside* the range: range edges are covered by
small aligned nodes (down to individual leaves) and never by a bucket that
reaches outside the range.  Partial coverage is always reported separately; a
piece with missing samples (or a small edge piece) is never counted as a full
bucket.

Edits are copy-on-write: every mutation returns a new immutable version and
old versions stay addressable.  Sample rates are exact rationals
(fractions.Fraction), so time<->sample mapping introduces no rounding.
"""

from __future__ import annotations

import math
from dataclasses import dataclass
from fractions import Fraction
from typing import List, Optional, Sequence, Tuple, Union

# ---------------------------------------------------------------------------
# Complexity / latency declaration
# ---------------------------------------------------------------------------
# n = timeline length.
#   build / append k samples : O(n) node allocations once; amortised O(k)
#   replace/insert/delete    : O(k + log n) copied nodes, O(log n) carries,
#                              O(log n) path visits (persistent subtree share)
#   query / peak             : O(log n) node visits + O(#pieces), #pieces is
#                              O(log n) for the dyadic decomposition
#   rate change              : O(1) (metadata-only version)
# Recursion depth is bounded by ceil(log2(n)).
RATE_T = Union[int, Fraction, str]


@dataclass(frozen=True)
class _Node:
    """Immutable dyadic node covering exactly ``size = 1 << level`` samples."""

    level: int
    lo: float          # minimum present value; +inf if the node has no sample
    hi: float          # maximum present value; -inf if the node has no sample
    energy: float      # sum of squared present samples
    present: int
    missing: int
    left: Optional["_Node"]
    right: Optional["_Node"]

    @property
    def size(self) -> int:
        return 1 << self.level


class _Ops:
    __slots__ = ("visits", "copied", "reused")

    def __init__(self) -> None:
        self.visits = 0   # pre-existing nodes read
        self.copied = 0   # new nodes allocated (maintenance footprint)
        self.reused = 0   # subtrees shared without copying


def _is_missing(x: float) -> bool:
    return isinstance(x, float) and math.isnan(x)


def _leaf(x: float) -> _Node:
    if _is_missing(x):
        return _Node(0, math.inf, -math.inf, 0.0, 0, 1, None, None)
    v = float(x)
    return _Node(0, v, v, v * v, 1, 0, None, None)


def _join(level: int, left: _Node, right: _Node) -> _Node:
    return _Node(
        level,
        min(left.lo, right.lo),
        max(left.hi, right.hi),
        left.energy + right.energy,
        left.present + right.present,
        left.missing + right.missing,
        left,
        right,
    )


@dataclass(frozen=True)
class _Block:
    """A complete tree plus its absolute start offset in the timeline."""

    start: int
    root: _Node

    @property
    def end(self) -> int:
        return self.start + self.root.size

    @property
    def level(self) -> int:
        return self.root.level


def _pack(blocks: Sequence[_Block], ops: Optional[_Ops]) -> List[_Block]:
    """Binary-adder normalisation: merge adjacent equal-sized blocks.

    Equal-level inputs are assumed adjacent; carries are applied so the output
    has strictly descending levels (large canonical block first).
    """
    out: List[_Block] = []
    for blk in blocks:
        while out and out[-1].level == blk.level:
            prev = out.pop()
            if ops is not None:
                ops.visits += 2
            blk = _Block(prev.start, _join(blk.level + 1, prev.root, blk.root))
            if ops is not None:
                ops.copied += 1
        out.append(blk)
    return out


def _pack_roots(roots: Sequence[_Node], ops: Optional[_Ops]) -> List[_Block]:
    blocks: List[_Block] = []
    pos = 0
    for root in roots:
        blocks.append(_Block(pos, root))
        pos += root.size
    return _pack(blocks, ops)


def _relabel(blocks: Sequence[_Block]) -> List[_Block]:
    """Recompute absolute starts from offset 0; no nodes are copied."""
    out: List[_Block] = []
    pos = 0
    for blk in blocks:
        out.append(_Block(pos, blk.root))
        pos += blk.root.size
    return out


# ---------------------------------------------------------------------------
# Immutable series
# ---------------------------------------------------------------------------


@dataclass(frozen=True)
class _Series:
    blocks: Tuple[_Block, ...]
    length: int

    @classmethod
    def empty(cls) -> "_Series":
        return cls((), 0)

    @classmethod
    def from_values(cls, values: Sequence[float], ops: Optional[_Ops]) -> "_Series":
        n = len(values)
        roots: List[_Node] = []
        pos = 0
        remaining = n
        while remaining:
            level = remaining.bit_length() - 1
            size = 1 << level
            level_nodes: List[_Node] = [_leaf(v) for v in values[pos:pos + size]]
            if ops is not None:
                ops.copied += len(level_nodes)
            cur = 0
            while len(level_nodes) > 1:
                level_nodes = [
                    _join(cur + 1, level_nodes[i], level_nodes[i + 1])
                    for i in range(0, len(level_nodes), 2)
                ]
                cur += 1
                if ops is not None:
                    ops.copied += len(level_nodes)
            roots.append(level_nodes[0])
            pos += size
            remaining -= size
        blocks = _pack_roots(roots, None)  # chunks already distinct powers of two
        return cls(tuple(blocks), n)

    def concat(self, other: "_Series", ops: Optional[_Ops]) -> "_Series":
        shifted = [_Block(self.length + b.start, b.root) for b in other.blocks]
        packed = _pack(list(self.blocks) + shifted, ops)
        return _Series(tuple(packed), self.length + other.length)

    def split(self, k: int, ops: Optional[_Ops]) -> Tuple["_Series", "_Series"]:
        if not 0 <= k <= self.length:
            raise ValueError("split position out of range")
        left_roots: List[_Node] = []
        right_roots: List[_Node] = []
        for blk in self.blocks:
            if k >= blk.end:
                left_roots.append(blk.root)
                if ops is not None:
                    ops.visits += 1
            elif k <= blk.start:
                right_roots.append(blk.root)
                if ops is not None:
                    ops.visits += 1
            else:
                lp, rp = _tree_split(blk.root, k - blk.start, ops)
                left_roots.extend(b.root for b in lp)
                right_roots.extend(b.root for b in rp)
        left = _Series(tuple(_pack_roots(left_roots, ops)), k)
        right = _Series(tuple(_pack_roots(right_roots, ops)), self.length - k)
        return left, right


def _tree_split(
    root: _Node, k: int, ops: Optional[_Ops]
) -> Tuple[List[_Block], List[_Block]]:
    """Split one complete tree at local offset k with 0 < k < root.size.

    Returned blocks on each side are densely labelled from offset 0.
    """
    if root.level == 0:
        raise AssertionError("leaf cannot be split at an interior offset")
    if ops is not None:
        ops.visits += 1
    assert root.left is not None and root.right is not None
    if k == 1 << (root.level - 1):
        ops.reused += 2
        return [_Block(0, root.left)], [_Block(0, root.right)]
    half = 1 << (root.level - 1)
    if k < half:
        lp, rp = _tree_split(root.left, k, ops)
        ops.reused += 1
        return lp, _pack_roots([b.root for b in rp] + [root.right], ops)
    lp2, rp2 = _tree_split(root.right, k - half, ops)
    ops.reused += 1
    return _pack_roots([root.left] + [b.root for b in lp2], ops), rp2


# ---------------------------------------------------------------------------
# Query results
# ---------------------------------------------------------------------------


@dataclass(frozen=True)
class Piece:
    start: int
    end: int
    level: int
    node: _Node
    touches_boundary: bool

    @property
    def size(self) -> int:
        return self.end - self.start


@dataclass(frozen=True)
class BucketInfo:
    start: int
    end: int
    level: int
    complete: bool          # every sample the piece can cover is present
    touches_boundary: bool
    present: int
    missing: int


@dataclass(frozen=True)
class Summary:
    lo: int
    hi: int
    samples: int
    present: int
    missing: int
    minimum: Optional[float]
    maximum: Optional[float]
    energy: float
    mean_energy: float
    full_buckets: int
    partial_buckets: int
    boundary_levels: Tuple[int, ...]
    buckets: Tuple[BucketInfo, ...]
    visits: int
    complete: bool

    def as_tuple(self) -> Tuple[Optional[float], Optional[float], float]:
        return self.minimum, self.maximum, self.energy


@dataclass(frozen=True)
class PeakResult:
    lo: int
    hi: int
    present: int
    peak: Optional[float]
    ranges: Tuple[Tuple[int, int], ...]          # maximal aligned tied nodes
    sample_ranges: Tuple[Tuple[int, int], ...]   # exact tied sample runs
    sample_indices: Tuple[int, ...]
    visits: int


@dataclass(frozen=True)
class Version:
    version_id: int
    rate: Fraction
    parent_id: Optional[int]
    note: str
    copied: int
    visits: int
    reused: int
    length: int


# ---------------------------------------------------------------------------
# Query decomposition: aligned dyadic pieces, edges exact
# ---------------------------------------------------------------------------


def _check_range(lo: int, hi: int, length: int) -> None:
    if not (0 <= lo <= hi <= length):
        raise ValueError(f"range [{lo}, {hi}) outside timeline of {length}")


def _decompose(series: _Series, lo: int, hi: int, ops: Optional[_Ops]) -> List[Piece]:
    pieces: List[Piece] = []

    def visit(node: _Node, start: int) -> None:
        end = start + node.size
        if end <= lo or start >= hi:
            return
        if lo <= start and end <= hi:
            if ops is not None:
                ops.visits += 1
            pieces.append(Piece(start, end, node.level, node,
                                start == lo or end == hi))
            return
        if node.level == 0:
            if ops is not None:
                ops.visits += 1
            pieces.append(Piece(start, end, 0, node, True))
            return
        if ops is not None:
            ops.visits += 1
        assert node.left is not None and node.right is not None
        mid = start + (1 << (node.level - 1))
        visit(node.left, start)
        visit(node.right, mid)

    for blk in series.blocks:
        if blk.end <= lo or blk.start >= hi:
            continue
        visit(blk.root, blk.start)
    return pieces


def _bucket_infos(pieces: Sequence[Piece]) -> Tuple[List[BucketInfo], int, int]:
    infos: List[BucketInfo] = []
    full = partial = 0
    for p in pieces:
        is_full = p.node.present == p.size and p.node.missing == 0
        if is_full:
            full += 1
        else:
            partial += 1
        infos.append(BucketInfo(
            p.start, p.end, p.level, is_full, p.touches_boundary,
            p.node.present, p.node.missing,
        ))
    return infos, full, partial


# ---------------------------------------------------------------------------
# Public index
# ---------------------------------------------------------------------------


class MultiResolutionIndex:
    """Versioned persistent multi-resolution index."""

    def __init__(self, rate: RATE_T = 1) -> None:
        self._rate0 = Fraction(rate)
        self._series: List[_Series] = [_Series.empty()]
        self._versions: List[Version] = [
            Version(0, self._rate0, None, "empty", 0, 0, 0, 0)
        ]

    # -- basic properties ---------------------------------------------------

    @property
    def head(self) -> int:
        return len(self._versions) - 1

    @property
    def length(self) -> int:
        return self._series[self.head].length

    @property
    def rate(self) -> Fraction:
        return self._versions[self.head].rate

    def version(self, version_id: Optional[int] = None) -> Version:
        return self._versions[self._vid(version_id)]

    def versions(self) -> Tuple[Version, ...]:
        return tuple(self._versions)

    def _vid(self, version_id: Optional[int]) -> int:
        if version_id is None:
            return self.head
        if not 0 <= version_id <= self.head:
            raise KeyError(f"unknown version {version_id}")
        return version_id

    # -- mutation primitives ------------------------------------------------

    def _commit(
        self,
        series: _Series,
        rate: Fraction,
        ops: _Ops,
        note: str,
        parent: Optional[int],
    ) -> int:
        parent_id = self.head if parent is None else parent
        self._series.append(series)
        vid = len(self._versions)
        self._versions.append(Version(
            vid, rate, parent_id, note, ops.copied, ops.visits, ops.reused,
            series.length,
        ))
        return vid

    def append(self, values: Sequence[float], note: str = "append") -> int:
        ops = _Ops()
        added = _Series.from_values(values, ops)
        series = self._series[self.head].concat(added, ops)
        return self._commit(series, self.rate, ops, note, self.head)

    def replace_range(
        self,
        lo: int,
        hi: int,
        values: Sequence[float],
        note: str = "replace",
    ) -> int:
        """Versioned interval replacement: [lo, hi) is overwritten.

        ``len(values) != hi - lo`` changes the length; inserting uses a zero
        width range and deleting uses an empty ``values`` sequence.
        """
        series = self._series[self.head]
        _check_range(lo, hi, series.length)
        ops = _Ops()
        left, rest = series.split(lo, ops)
        _, right = rest.split(hi - lo, ops)
        middle = _Series.from_values(values, ops)
        new_series = left.concat(middle, ops).concat(right, ops)
        return self._commit(new_series, self.rate, ops, note, self.head)

    def insert(self, at: int, values: Sequence[float], note: str = "insert") -> int:
        return self.replace_range(at, at, values, note)

    def delete(self, lo: int, hi: int, note: str = "delete") -> int:
        return self.replace_range(lo, hi, (), note)

    # "withdraw": publish a withdrawn/recalled interval as missing markers
    # without moving later samples.
    def withdraw(self, lo: int, hi: int, note: str = "withdraw") -> int:
        series = self._series[self.head]
        _check_range(lo, hi, series.length)
        if lo == hi:
            raise ValueError("withdraw requires a non-empty interval")
        return self.replace_range(lo, hi, [math.nan] * (hi - lo), note)

    def restore(
        self, lo: int, hi: int, values: Sequence[float], note: str = "restore"
    ) -> int:
        if len(values) != hi - lo:
            raise ValueError("restore length must match the interval length")
        return self.replace_range(lo, hi, values, note)

    def change_rate(self, rate: RATE_T, note: str = "rate") -> int:
        """Record an exact rational sample-rate change without moving samples."""
        new_rate = Fraction(rate)
        if new_rate <= 0:
            raise ValueError("sample rate must be positive")
        ops = _Ops()
        return self._commit(self._series[self.head], new_rate, ops, note,
                            self.head)

    def rollback(self, version_id: int, note: str = "rollback") -> int:
        """Publish a new head pointing at the state of ``version_id``."""
        vid = self._vid(version_id)
        ops = _Ops()
        return self._commit(
            self._series[vid], self._versions[vid].rate, ops, note, self.head,
        )

    # -- time helpers (exact rational mapping) ------------------------------

    def index_at_time(self, seconds: Fraction, *, version_id: Optional[int] = None) -> int:
        """Floor sample index containing ``seconds`` since index time origin."""
        vid = self._vid(version_id)
        rate = self._versions[vid].rate
        t = Fraction(seconds)
        idx = (t * rate).numerator // (t * rate).denominator
        return max(0, min(self._series[vid].length, idx))

    def time_at_index(self, index: int, *, version_id: Optional[int] = None) -> Fraction:
        vid = self._vid(version_id)
        if not 0 <= index <= self._series[vid].length:
            raise ValueError("index out of range")
        return Fraction(index, self._versions[vid].rate)

    # -- queries ------------------------------------------------------------

    def query(
        self,
        lo: int,
        hi: Optional[int] = None,
        *,
        version_id: Optional[int] = None,
    ) -> Summary:
        vid = self._vid(version_id)
        series = self._series[vid]
        if hi is None:
            hi = lo + 1
        _check_range(lo, hi, series.length)
        ops = _Ops()
        pieces = _decompose(series, lo, hi, ops)
        infos, full, partial = _bucket_infos(pieces)
        samples = hi - lo
        present = sum(p.node.present for p in pieces)
        missing = sum(p.node.missing for p in pieces)
        if present:
            minimum = min(p.node.lo for p in pieces if p.node.present)
            maximum = max(p.node.hi for p in pieces if p.node.present)
        else:
            minimum = maximum = None
        energy = math.fsum(p.node.energy for p in pieces)
        boundary_levels = tuple(sorted({p.level for p in pieces
                                        if p.touches_boundary}))
        return Summary(
            lo=lo, hi=hi, samples=samples, present=present, missing=missing,
            minimum=minimum, maximum=maximum, energy=energy,
            mean_energy=(energy / present if present else 0.0),
            full_buckets=full, partial_buckets=partial,
            boundary_levels=boundary_levels,
            buckets=tuple(infos), visits=ops.visits,
            complete=(present == samples),
        )

    def query_time(
        self,
        t0: Fraction,
        t1: Fraction,
        *,
        version_id: Optional[int] = None,
    ) -> Summary:
        vid = self._vid(version_id)
        return self.query(self.index_at_time(t0, version_id=vid),
                          self.index_at_time(t1, version_id=vid),
                          version_id=vid)

    def peak(
        self,
        lo: int,
        hi: Optional[int] = None,
        *,
        version_id: Optional[int] = None,
    ) -> PeakResult:
        """Return the peak and *all* tied source ranges.

        ``ranges`` lists maximal aligned dyadic nodes attaining the peak (the
        pyramid-level provenance).  ``sample_ranges``/``sample_indices`` refine
        ties to exact leaf positions.
        """
        vid = self._vid(version_id)
        series = self._series[vid]
        if hi is None:
            hi = lo + 1
        _check_range(lo, hi, series.length)
        ops = _Ops()
        pieces = _decompose(series, lo, hi, ops)
        live = [p for p in pieces if p.node.present]
        if not live:
            return PeakResult(lo, hi, 0, None, (), (), (), ops.visits)
        top = max(p.node.hi for p in live)

        # Provenance: descend each tied piece to the maximal aligned nodes
        # whose *entire* covered span attains the peak (a coarse bucket whose
        # maximum merely equals the peak still contains non-peak samples and
        # is not a valid source range).
        ranges: List[Tuple[int, int]] = []
        indices: List[int] = []

        def collect(node: _Node, start: int) -> None:
            if node.hi != top:
                return
            ops.visits += 1
            if node.level == 0:
                indices.append(start)
                ranges.append((start, start + 1))
                return
            assert node.left is not None and node.right is not None
            # Whole node attains the peak only when every present sample does.
            whole = (
                node.present == node.size
                and node.missing == 0
                and node.lo == top
            )
            if whole:
                ranges.append((start, start + node.size))
                indices.extend(range(start, start + node.size))
                return
            mid = start + (1 << (node.level - 1))
            collect(node.left, start)
            collect(node.right, mid)

        for p in live:
            if p.node.hi == top:
                collect(p.node, p.start)

        indices.sort()
        runs: List[Tuple[int, int]] = []
        for idx in indices:
            if runs and runs[-1][1] == idx:
                runs[-1] = (runs[-1][0], idx + 1)
            else:
                runs.append((idx, idx + 1))
        return PeakResult(
            lo=lo, hi=hi, present=sum(p.node.present for p in pieces),
            peak=top, ranges=tuple(ranges),
            sample_ranges=tuple(runs), sample_indices=tuple(indices),
            visits=ops.visits,
        )

    # -- introspection ------------------------------------------------------

    def level_nodes(
        self, level: int, *, version_id: Optional[int] = None
    ) -> Tuple[Tuple[int, int, float, float, float, int, int], ...]:
        """In-order listing of nodes at pyramid ``level`` for inspection."""
        vid = self._vid(version_id)
        out: List[Tuple[int, int, float, float, float, int, int]] = []

        def walk(node: _Node, start: int) -> None:
            if node.level == level:
                out.append((start, start + node.size, node.lo, node.hi,
                            node.energy, node.present, node.missing))
                return
            if node.level < level or node.left is None:
                return
            mid = start + (1 << (node.level - 1))
            walk(node.left, start)
            walk(node.right, mid)

        for blk in self._series[vid].blocks:
            if blk.root.level >= level:
                walk(blk.root, blk.start)
        return tuple(out)

    def levels(self, *, version_id: Optional[int] = None) -> Tuple[int, ...]:
        vid = self._vid(version_id)
        return tuple(sorted({b.level for b in self._series[vid].blocks}))
