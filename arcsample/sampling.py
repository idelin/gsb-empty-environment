"""Equal-arc-length sampling with stable ids, budgets and local repair.

Sample identity
---------------
* keypoint sample:  ``"<path>:v<vertex id>"``  -- anchored at a vertex.
* regular sample:   ``"<path>:g<k>"``          -- anchored at the k-th
  multiple of the spacing measured from the sub-path start (the original
  anchor; for closed paths the fixed start vertex).

Because regular ids come from the spacing grid and keypoint ids from
vertex anchors, a local edit keeps every downstream sample id stable;
only mileages/points shift.  A local repair therefore produces a small
:class:`Patch` (added / removed / moved) instead of a full resample, and
applying the patch is exactly equivalent to an independent full resample.
"""

from __future__ import annotations

from bisect import bisect_right
from dataclasses import dataclass
from fractions import Fraction
from typing import List, Optional, Tuple

from .model import Path, SubPath
from .rational import Number, Point, as_fraction


@dataclass(frozen=True)
class SourceRef:
    """Where a sample came from."""

    subpath: int
    segment: Optional[int]            # segment index, None for keypoints
    t: Optional[Fraction]             # exact parameter inside the segment
    edge: Optional[Tuple[str, str]]   # (start vertex id, end vertex id)
    vertex: Optional[str]             # vertex id for keypoints


@dataclass(frozen=True)
class Sample:
    id: str
    mileage: Fraction                 # exact arc length from sub-path start
    point: Point
    direction: Optional[Point]        # exact unit tangent, None if undefined
    kind: str                         # "keypoint" | "regular"
    seam: bool                        # True for the fixed start of a closed path
    source: SourceRef

    def signature(self) -> tuple:
        """Observable identity used by patch diffing (excludes id/source)."""
        return (self.mileage, self.point, self.direction, self.kind, self.seam)


@dataclass(frozen=True)
class Cursor:
    """Resumable position returned when a sample budget runs out."""

    subpath: int
    offset: int                       # next sample index inside the sub-path
    version: int


@dataclass(frozen=True)
class SamplingResult:
    samples: Tuple[Sample, ...]
    complete: bool                    # False iff the budget was insufficient
    cursor: Optional[Cursor]          # set iff not complete; never silently drops


@dataclass(frozen=True)
class SampleMove:
    id: str
    old: Sample
    new: Sample


@dataclass(frozen=True)
class Patch:
    """A minimal, invertible edit script between two sample sets."""

    path_id: str
    added: Tuple[Sample, ...]
    removed: Tuple[Sample, ...]
    moved: Tuple[SampleMove, ...]
    affected: Optional[Tuple[Fraction, Fraction]]      # old-mileage range, None = full
    new_affected: Optional[Tuple[Fraction, Fraction]]  # same range after the edit
    length_delta: Fraction

    def apply(self, samples: Tuple[Sample, ...]) -> Tuple[Sample, ...]:
        moved_new = {m.id: m.new for m in self.moved}
        removed_ids = {s.id for s in self.removed}
        out: List[Sample] = []
        for sample in samples:
            if sample.id in removed_ids:
                continue
            out.append(moved_new.get(sample.id, sample))
        out.extend(self.added)
        return tuple(sorted(out, key=lambda s: (s.mileage, s.id)))

    def inverse(self) -> "Patch":
        return Patch(
            path_id=self.path_id,
            added=self.removed,
            removed=self.added,
            moved=tuple(SampleMove(m.id, m.new, m.old) for m in self.moved),
            affected=self.new_affected,
            new_affected=self.affected,
            length_delta=-self.length_delta,
        )


def _keypoint_direction(sub: SubPath, vertex_index: int) -> Optional[Point]:
    """Direction of the next non-degenerate segment, else the previous one."""
    count = len(sub.segments)
    for step in range(count):
        idx = vertex_index + step
        if not sub.closed and idx >= count:
            break
        seg = sub.segments[idx % count]
        if seg.unit is not None:
            return seg.unit
    for step in range(1, count + 1):
        idx = vertex_index - step
        if not sub.closed and idx < 0:
            break
        seg = sub.segments[idx % count]
        if seg.unit is not None:
            return seg.unit
    return None


def _subpath_samples(path_id: str, sub: SubPath, spacing: Fraction) -> List[Sample]:
    samples: List[Sample] = []
    n = len(sub.vertices)
    for i, vertex in enumerate(sub.vertices):
        samples.append(
            Sample(
                id=f"{path_id}:{vertex.id}",
                mileage=sub.cum[i],
                point=vertex.point,
                direction=_keypoint_direction(sub, i),
                kind="keypoint",
                seam=sub.closed and i == 0,
                source=SourceRef(sub.index, None, None, None, vertex.id),
            )
        )
    total = sub.length
    if total > 0:
        vertex_mileages = set(sub.cum[:n])  # cum[n] is the seam, not a vertex
        cum = list(sub.cum)
        k = 1
        while True:
            mileage = k * spacing
            if mileage >= total:
                break
            if mileage not in vertex_mileages:
                seg_index = bisect_right(cum, mileage) - 1
                seg = sub.segments[seg_index]
                # seg is non-degenerate: mileage lies strictly inside it.
                t = (mileage - cum[seg_index]) / seg.length
                point = tuple(
                    a + t * (b - a) for a, b in zip(seg.start, seg.end)
                )
                samples.append(
                    Sample(
                        id=f"{path_id}:g{k}",
                        mileage=mileage,
                        point=point,
                        direction=seg.unit,
                        kind="regular",
                        seam=False,
                        source=SourceRef(
                            sub.index, seg_index, t, (seg.start_id, seg.end_id), None
                        ),
                    )
                )
            k += 1
    samples.sort(key=lambda s: (s.mileage, s.id))
    return samples


def sample_path(
    path: Path,
    spacing: Number,
    budget: Optional[int] = None,
    cursor: Optional[Cursor] = None,
    version: int = 0,
) -> SamplingResult:
    """Sample every sub-path; honour an optional budget with a resumable cursor."""
    spacing = as_fraction(spacing)
    if spacing <= 0:
        raise ValueError("spacing must be positive")
    if budget is not None and budget < 0:
        raise ValueError("budget must be non-negative")
    start_sub, start_off = 0, 0
    if cursor is not None:
        if cursor.version != version:
            raise ValueError(
                f"stale cursor: path version is {version}, cursor is {cursor.version}"
            )
        start_sub, start_off = cursor.subpath, cursor.offset
    out: List[Sample] = []
    for si in range(start_sub, len(path.subpaths)):
        sub_samples = _subpath_samples(path.path_id, path.subpaths[si], spacing)
        offset = start_off if si == start_sub else 0
        for j in range(offset, len(sub_samples)):
            if budget is not None and len(out) >= budget:
                return SamplingResult(tuple(out), False, Cursor(si, j, version))
            out.append(sub_samples[j])
    return SamplingResult(tuple(out), True, None)


def make_patch(
    path_id: str,
    old_samples: Tuple[Sample, ...],
    new_samples: Tuple[Sample, ...],
    affected: Optional[Tuple[Fraction, Fraction]],
    new_affected: Optional[Tuple[Fraction, Fraction]],
    length_delta: Fraction,
) -> Patch:
    old_by_id = {s.id: s for s in old_samples}
    new_by_id = {s.id: s for s in new_samples}
    removed = tuple(s for s in old_samples if s.id not in new_by_id)
    added = tuple(s for s in new_samples if s.id not in old_by_id)
    moved = tuple(
        SampleMove(sid, old_by_id[sid], new_by_id[sid])
        for sid in old_by_id.keys() & new_by_id.keys()
        if old_by_id[sid].signature() != new_by_id[sid].signature()
    )
    return Patch(path_id, added, removed, moved, affected, new_affected, length_delta)


class PathSampler:
    """Stateful sampler supporting local control-point edits."""

    def __init__(self, path: Path, spacing: Number):
        self.spacing = as_fraction(spacing)
        if self.spacing <= 0:
            raise ValueError("spacing must be positive")
        self.path = path
        self.version = 0
        self._samples = sample_path(path, self.spacing).samples

    @property
    def samples(self) -> Tuple[Sample, ...]:
        return self._samples

    def sample(self, budget: Optional[int] = None, cursor: Optional[Cursor] = None) -> SamplingResult:
        return sample_path(self.path, self.spacing, budget, cursor, self.version)

    def move_point(self, vertex_id: str, new_point) -> Patch:
        """Move one control point and repair only the affected mileage range.

        Returns an invertible :class:`Patch`.  Samples outside the affected
        range keep their ids; downstream samples keep their grid/anchor ids
        and are reported as moves when their mileage or point shifts.
        """
        sub_index, vertex_index = self.path.vertex_index(vertex_id)
        sub = self.path.subpaths[sub_index]
        n = len(sub.vertices)
        if sub.closed:
            affected = None
        else:
            low = sub.cum[vertex_index - 1] if vertex_index > 0 else sub.cum[0]
            high = sub.cum[vertex_index + 1] if vertex_index < n - 1 else sub.cum[-1]
            affected = (low, high)

        new_path = self.path.replacing(vertex_id, new_point)
        new_sub = new_path.subpaths[sub_index]
        if affected is None:
            new_affected = None
        else:
            new_high = new_sub.cum[vertex_index + 1] if vertex_index < n - 1 else new_sub.cum[-1]
            new_affected = (affected[0], new_high)

        new_samples = sample_path(new_path, self.spacing).samples
        patch = make_patch(
            self.path.path_id,
            self._samples,
            new_samples,
            affected,
            new_affected,
            new_sub.length - sub.length,
        )
        self.path = new_path
        self.version += 1
        self._samples = new_samples
        return patch
