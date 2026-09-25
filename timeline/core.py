"""Sample <-> time mapping for variable-length blocks.

Semantics
---------
* Every interval is half-open: [t_start, t_end).  A query that lands exactly
  on a block boundary therefore never duplicates a sample.
* A sample is a *point* located at its sample instant
  ``t_i = base_time + calibration_offset + i / sample_rate``.  Each block
  carries its own rate, so a sample-rate switch is just a new block; no old
  conversion factor is ever reused across blocks.
* Block b covers the span [t_0, t_0 + n / rate).  Where spans overlap, the
  block with the higher ``version`` wins; ties are broken explicitly by the
  lexicographically larger ``block_id`` (deterministic and documented).
* Overlap resolution produces non-overlapping slices that tile the covered
  region.  Uncovered intervals are holes (reason "gap"); intervals whose
  winning block contributes no sample instant are holes too
  (reason "rounding").  Both kinds are preserved as evidence.
* All arithmetic uses ``fractions.Fraction`` and is exact.  Float conversion
  only happens at the presentation edge; comparisons of float times should
  use ``TOLERANCE_SECONDS``.
"""

from __future__ import annotations

import math
import threading
from bisect import bisect_left, bisect_right
from dataclasses import dataclass, field
from fractions import Fraction
from typing import Iterator, Mapping, Optional, Sequence, Tuple

# Declared numeric tolerance for any comparison done in float seconds.
TOLERANCE_SECONDS = 1e-9

# Coarse-level bucket width (number of slices per bucket) of the index.
_BUCKET_SIZE = 8


def _ceil_frac(value: Fraction) -> int:
    return math.ceil(value)


@dataclass(frozen=True)
class SampleBlock:
    """A variable-length run of samples at a constant sample rate."""

    block_id: str
    samples: Tuple[float, ...]
    sample_rate: int
    version: int
    base_time: Fraction = Fraction(0)

    def __post_init__(self) -> None:
        if not self.block_id:
            raise ValueError("block_id must be non-empty")
        if self.sample_rate <= 0:
            raise ValueError("sample_rate must be positive")
        if len(self.samples) == 0:
            raise ValueError("a block must contain at least one sample")
        object.__setattr__(self, "samples", tuple(self.samples))
        object.__setattr__(self, "base_time", Fraction(self.base_time))

    @property
    def duration(self) -> Fraction:
        return Fraction(len(self.samples), self.sample_rate)


@dataclass(frozen=True)
class Calibration:
    """Maps block_id -> additive time offset (seconds, exact).

    Applying a new calibration may shift historical blocks; see
    ``Timeline.apply_calibration``.
    """

    offsets: Mapping[str, Fraction] = field(default_factory=dict)

    def __post_init__(self) -> None:
        object.__setattr__(
            self, "offsets", {k: Fraction(v) for k, v in self.offsets.items()}
        )

    def offset_for(self, block_id: str) -> Fraction:
        return self.offsets.get(block_id, Fraction(0))

    def merged(self, other: "Calibration") -> "Calibration":
        combined = dict(self.offsets)
        combined.update(other.offsets)
        return Calibration(combined)


@dataclass(frozen=True)
class Slice:
    """A non-overlapping, resolved piece of one block.

    ``lead`` and ``overshoot`` are the rounding evidence: how far the slice
    boundaries sit from the nearest owned sample instants (both >= 0, both
    exactly 0 when the boundary is sample-aligned).
    """

    block_id: str
    version: int
    sample_rate: int
    sample_start: int  # inclusive
    sample_end: int  # exclusive
    t_start: Fraction
    t_end: Fraction
    lead: Fraction
    overshoot: Fraction


@dataclass(frozen=True)
class Hole:
    t_start: Fraction
    t_end: Fraction
    reason: str  # "gap" | "rounding"


@dataclass(frozen=True)
class Segment:
    """Valid samples returned by a time-range read."""

    block_id: str
    sample_start: int
    sample_end: int
    samples: Tuple[float, ...]
    t_start: Fraction
    t_end: Fraction
    lead: Fraction
    overshoot: Fraction


@dataclass(frozen=True)
class ReadResult:
    t_start: Fraction
    t_end: Fraction
    segments: Tuple[Segment, ...]
    holes: Tuple[Hole, ...]

    @property
    def sample_count(self) -> int:
        return sum(seg.sample_end - seg.sample_start for seg in self.segments)


def _span(block: SampleBlock, offset: Fraction) -> Tuple[Fraction, Fraction]:
    t0 = block.base_time + offset
    return t0, t0 + block.duration


def build_index(
    blocks: Sequence[SampleBlock], calibration: Calibration
) -> "TimelineIndex":
    """Resolve overlaps and build an immutable, time-sorted index.

    Blocks may arrive in any order; nothing here assumes that insertion order
    correlates with time.
    """
    timed = []
    events = set()
    for block in blocks:
        t0, t1 = _span(block, calibration.offset_for(block.block_id))
        timed.append((block, t0, t1))
        events.add(t0)
        events.add(t1)

    points = sorted(events)
    slices = []
    holes = []
    for s, e in zip(points, points[1:]):
        active = [(b, t0) for (b, t0, t1) in timed if t0 <= s and t1 >= e]
        if not active:
            holes.append(Hole(s, e, "gap"))
            continue
        winner, wt0 = max(active, key=lambda bt: (bt[0].version, bt[0].block_id))
        rate = winner.sample_rate
        i0 = _ceil_frac((s - wt0) * rate)
        i1 = _ceil_frac((e - wt0) * rate)
        if i0 >= i1:
            # The winning block has no sample instant inside this interval.
            holes.append(Hole(s, e, "rounding"))
            continue
        lead = wt0 + Fraction(i0, rate) - s
        overshoot = wt0 + Fraction(i1, rate) - e
        slices.append(
            Slice(
                block_id=winner.block_id,
                version=winner.version,
                sample_rate=rate,
                sample_start=i0,
                sample_end=i1,
                t_start=s,
                t_end=e,
                lead=lead,
                overshoot=overshoot,
            )
        )

    # Merge contiguous holes that share a reason.
    merged_holes = []
    for hole in holes:
        if (
            merged_holes
            and merged_holes[-1].reason == hole.reason
            and merged_holes[-1].t_end == hole.t_start
        ):
            merged_holes[-1] = Hole(
                merged_holes[-1].t_start, hole.t_end, hole.reason
            )
        else:
            merged_holes.append(hole)

    block_map = {b.block_id: b for b in blocks}
    spans = {}
    for block, t0, t1 in timed:
        spans[block.block_id] = (t0, t1)
    return TimelineIndex(
        slices=tuple(slices),
        holes=tuple(merged_holes),
        blocks=block_map,
        spans=spans,
        calibration=calibration,
    )


class TimelineIndex:
    """Immutable two-level (coarse buckets + fine slices) time index.

    The coarse level never assumes monotonicity of the *input*; it only
    relies on the resolved slices, which are explicitly sorted by time at
    build time.
    """

    __slots__ = (
        "slices",
        "holes",
        "calibration",
        "_blocks",
        "_spans",
        "_starts",
        "_bucket_ends",
        "_buckets",
    )

    def __init__(self, slices, holes, blocks, spans, calibration):
        self.slices = slices
        self.holes = holes
        self.calibration = calibration
        self._blocks = blocks
        self._spans = spans
        self._starts = tuple(s.t_start for s in slices)
        buckets = []
        for lo in range(0, len(slices), _BUCKET_SIZE):
            hi = min(lo + _BUCKET_SIZE, len(slices))
            buckets.append((slices[hi - 1].t_end, lo, hi))
        self._buckets = tuple(buckets)
        self._bucket_ends = tuple(b[0] for b in buckets)

    # ---- internal helpers -------------------------------------------------

    def _block_t0(self, block_id: str) -> Fraction:
        return self._spans[block_id][0]

    def _classify_hole(self, s: Fraction, e: Fraction) -> str:
        for t0, t1 in self._spans.values():
            if t0 <= s and t1 >= e:
                return "rounding"
        return "gap"

    # ---- bidirectional sample <-> time queries ----------------------------

    def time_range_of(self, block_id: str, i0: int, i1: int) -> Tuple[Fraction, Fraction]:
        """Exact time range of samples [i0, i1) of a block."""
        block = self._blocks[block_id]
        if not (0 <= i0 <= i1 <= len(block.samples)):
            raise ValueError("sample range out of bounds")
        t0 = self._block_t0(block_id)
        rate = block.sample_rate
        return t0 + Fraction(i0, rate), t0 + Fraction(i1, rate)

    def sample_range_of(
        self, block_id: str, t0: Fraction, t1: Fraction
    ) -> Tuple[int, int]:
        """Inverse of ``time_range_of``: samples whose instant lies in [t0, t1)."""
        block = self._blocks[block_id]
        start = self._block_t0(block_id)
        rate = block.sample_rate
        n = len(block.samples)
        i0 = min(max(_ceil_frac((Fraction(t0) - start) * rate), 0), n)
        i1 = min(max(_ceil_frac((Fraction(t1) - start) * rate), 0), n)
        return i0, max(i0, i1)

    def slice_at(self, t: Fraction) -> Optional[Slice]:
        """The resolved slice containing instant ``t``, or None inside a hole."""
        i = bisect_right(self._starts, t) - 1
        if i >= 0 and self.slices[i].t_end > t:
            return self.slices[i]
        return None

    def slices_in(self, t0: Fraction, t1: Fraction) -> Tuple[Slice, ...]:
        """All resolved slices intersecting [t0, t1), via the two-level index."""
        t0, t1 = Fraction(t0), Fraction(t1)
        if t1 <= t0:
            return ()
        result = []
        bi = bisect_right(self._bucket_ends, t0)
        while bi < len(self._buckets):
            _, lo, hi = self._buckets[bi]
            if self.slices[lo].t_start >= t1:
                break
            for s in self.slices[lo:hi]:
                if s.t_end > t0 and s.t_start < t1:
                    result.append(s)
            bi += 1
        return tuple(result)

    # ---- reading ----------------------------------------------------------

    def read(self, t0: Fraction, t1: Fraction) -> ReadResult:
        """Read valid samples and hole intervals inside [t0, t1)."""
        t0, t1 = Fraction(t0), Fraction(t1)
        if t1 <= t0:
            raise ValueError("empty read window")
        segments = []
        for s in self.slices_in(t0, t1):
            cs = max(s.t_start, t0)
            ce = min(s.t_end, t1)
            block = self._blocks[s.block_id]
            start = self._block_t0(s.block_id)
            rate = block.sample_rate
            i0 = _ceil_frac((cs - start) * rate)
            i1 = _ceil_frac((ce - start) * rate)
            if i0 >= i1:
                continue  # clamping cut away every sample instant
            segments.append(
                Segment(
                    block_id=s.block_id,
                    sample_start=i0,
                    sample_end=i1,
                    samples=block.samples[i0:i1],
                    t_start=cs,
                    t_end=ce,
                    lead=start + Fraction(i0, rate) - cs,
                    overshoot=start + Fraction(i1, rate) - ce,
                )
            )
        holes = []
        cursor = t0
        for seg in segments:
            if seg.t_start > cursor:
                holes.append(Hole(cursor, seg.t_start, self._classify_hole(cursor, seg.t_start)))
            cursor = seg.t_end
        if cursor < t1:
            holes.append(Hole(cursor, t1, self._classify_hole(cursor, t1)))
        return ReadResult(t0, t1, tuple(segments), tuple(holes))


class Cursor:
    """A stable iterator bound to one immutable index snapshot.

    Calibration updates swap the timeline's index atomically; cursors created
    earlier keep iterating the old snapshot unchanged.
    """

    def __init__(self, index: TimelineIndex):
        self._index = index
        self._pos = 0

    @property
    def index(self) -> TimelineIndex:
        return self._index

    def __iter__(self) -> Iterator[Slice]:
        return self

    def __next__(self) -> Slice:
        if self._pos >= len(self._index.slices):
            raise StopIteration
        s = self._index.slices[self._pos]
        self._pos += 1
        return s

    def read(self, t0: Fraction, t1: Fraction) -> ReadResult:
        return self._index.read(t0, t1)


class Timeline:
    """Mutable facade; every mutation rebuilds and atomically swaps the index."""

    def __init__(self) -> None:
        self._lock = threading.Lock()
        self._blocks: dict[str, SampleBlock] = {}
        self._calibration = Calibration()
        self._index = build_index((), self._calibration)
        self._revision = 0

    def _rebuild_locked(self) -> None:
        new_index = build_index(tuple(self._blocks.values()), self._calibration)
        self._index = new_index  # single atomic reference swap
        self._revision += 1

    def add_block(self, block: SampleBlock) -> None:
        with self._lock:
            self._blocks[block.block_id] = block
            self._rebuild_locked()

    def retract_block(self, block_id: str) -> None:
        with self._lock:
            if block_id not in self._blocks:
                raise KeyError(block_id)
            del self._blocks[block_id]
            self._rebuild_locked()

    def apply_calibration(self, calibration: Calibration) -> None:
        """Merge new offsets; historical block mappings may change atomically."""
        with self._lock:
            self._calibration = self._calibration.merged(calibration)
            self._rebuild_locked()

    @property
    def revision(self) -> int:
        return self._revision

    def index(self) -> TimelineIndex:
        """The current immutable snapshot (safe to hold and query forever)."""
        return self._index

    def cursor(self) -> Cursor:
        return Cursor(self.index())
