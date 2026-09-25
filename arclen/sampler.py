"""Equal-arc-length sampling of polylines with anchors, seams and patches.

Design summary (see README.md for the full contract):

* The polyline is split into runs at anchors (path ends, key vertices,
  both sides of breaks, shared-edge endpoints).  Inside a run, samples
  are placed at exact Fraction mileages spacing, 2*spacing, ... with the
  remainder carried exactly across segment boundaries (rational mileage,
  no float drift).  Anchors are always emitted as key samples.
* Sample ids are anchor-based (K{vertex}, G{anchor}.{ordinal},
  E{edge}.{canonical ordinal}) and decoupled from absolute mileage, so
  samples of untouched runs keep identical ids and points after an edit.
* move_vertex() resamples only the affected runs and returns a Patch
  (removed / added / mileage_delta); downstream runs only get a constant
  mileage shift.  undo() restores the exact previous state.
* sample(budget=...) never drops points silently: when the budget runs
  out it returns a resumable Cursor; a cursor used after a modification
  raises StaleCursorError.
"""
from __future__ import annotations

from dataclasses import dataclass, replace
from fractions import Fraction

from .model import Point, Polyline, Vertex, segment_length, to_point


class StaleCursorError(RuntimeError):
    """A cursor was used after the sampler had been modified."""


@dataclass(frozen=True)
class Sample:
    id: str
    mileage: Fraction          # exact global arc-length position
    point: Point
    kind: str                  # "key" (anchor) or "grid" (equal-arc-length)
    seg: int | None            # source segment index (its start vertex)
    t: Fraction | None         # exact parameter inside the source segment
    direction: Point | None    # exact (unnormalized) source segment direction


@dataclass(frozen=True)
class Cursor:
    """Resumable position for budgeted sampling."""

    run_index: int
    offset: int  # next sample index inside that run's emission
    version: int


@dataclass
class Batch:
    samples: list
    done: bool
    cursor: Cursor | None  # set iff not done; pass back to sample() to resume


@dataclass
class Patch:
    """Result of move_vertex(); also serves as the undo token."""

    removed: list            # old samples whose id vanished or content changed
    added: list              # new samples whose id is new or content changed
    mileage_delta: Fraction  # constant odometer shift for all later samples
    insert_at: int           # index where the affected slice starts
    affected_range: tuple    # old-mileage interval that was resampled
    _first: int
    _last: int
    _old_run_samples: dict
    _new_run_samples: dict
    _old_polyline: Polyline
    _new_polyline: Polyline
    _old_runs: list
    _new_runs: list
    _old_bases: list
    applied: bool = False
    _after_version: int = -1


@dataclass
class RunInfo:
    vertices: list   # vertex indices, consecutive existing segments
    segments: list   # segment indices (segment j starts at vertex j)
    lengths: list
    shared: bool     # single shared edge -> sampled via SeamRegistry

    @property
    def length(self) -> Fraction:
        return sum(self.lengths, Fraction(0))


def _checked_spacing(spacing) -> Fraction:
    if isinstance(spacing, Fraction):
        value = spacing
    elif isinstance(spacing, int) and not isinstance(spacing, bool):
        value = Fraction(spacing)
    else:
        raise TypeError("spacing must be int or Fraction")
    if value <= 0:
        raise ValueError("spacing must be positive")
    return value


def _anchors(poly: Polyline) -> list:
    n = len(poly.vertices)
    flags = [False] * n
    if n == 0:
        return flags
    flags[0] = True
    if not poly.closed:
        flags[n - 1] = True
    for i, v in enumerate(poly.vertices):
        if v.key or v.break_before:
            flags[i] = True
    for j in poly.shared:
        if not poly.segment_exists(j):
            raise ValueError(f"shared segment {j} does not exist")
        flags[j] = True
        flags[(j + 1) % n] = True
    for i, v in enumerate(poly.vertices):
        if v.break_before:
            if i > 0:
                flags[i - 1] = True  # vertex before a gap ends its run
            elif poly.closed:
                flags[n - 1] = True
    return flags


def _subpaths(poly: Polyline) -> list:
    n = len(poly.vertices)
    if n == 0:
        return []
    subs = [[0]]
    for i in range(1, n):
        if poly.segment_exists(i - 1):
            subs[-1].append(i)
        else:
            subs.append([i])
    if poly.closed and n > 1 and poly.segment_exists(n - 1):
        subs[-1].append(0)  # closing segment wraps back to the fixed start
    return subs


def _make_run(poly: Polyline, vertices: list) -> RunInfo:
    segments = []
    lengths = []
    for a, b in zip(vertices, vertices[1:]):
        segments.append(a)  # segment index == index of its start vertex
        lengths.append(segment_length(poly.vertices[a].point, poly.vertices[b].point))
    shared = len(segments) == 1 and segments[0] in poly.shared
    return RunInfo(vertices=list(vertices), segments=segments, lengths=lengths,
                   shared=shared)


def _build_runs(poly: Polyline) -> list:
    if not poly.vertices:
        return []
    flags = _anchors(poly)
    runs = []
    for sub in _subpaths(poly):
        if len(sub) == 1:
            runs.append(RunInfo(vertices=[sub[0]], segments=[], lengths=[],
                                shared=False))
            continue
        chain = [sub[0]]
        for v in sub[1:]:
            chain.append(v)
            if flags[v]:
                runs.append(_make_run(poly, chain))
                chain = [v]
        if len(chain) > 1:
            runs.append(_make_run(poly, chain))
        # a length-1 leftover vertex already closed the previous run
    return runs


def _direction(poly: Polyline, seg: int) -> Point:
    n = len(poly.vertices)
    a = poly.vertices[seg].point
    b = poly.vertices[(seg + 1) % n].point
    return tuple(bk - ak for ak, bk in zip(a, b))


def _diff_samples(old_slice: list, new_slice: list) -> tuple:
    old_by_id = {s.id: s for s in old_slice}
    new_by_id = {s.id: s for s in new_slice}
    removed = [s for s in old_slice if new_by_id.get(s.id) != s]
    added = [s for s in new_slice if old_by_id.get(s.id) != s]
    return removed, added


class Sampler:
    def __init__(self, polyline: Polyline, spacing, registry=None):
        self.polyline = polyline
        self.spacing = _checked_spacing(spacing)
        self.registry = registry
        self.version = 0
        self._runs = _build_runs(self.polyline)
        self._reemit()

    # ------------------------------------------------------------------
    # emission
    # ------------------------------------------------------------------
    def _reemit(self):
        self._bases = []
        self._run_samples = []
        base = Fraction(0)
        for r, run in enumerate(self._runs):
            self._bases.append(base)
            self._run_samples.append(self._emit_run(self.polyline, self._runs, r, base))
            base += run.length
        self.total_length = base

    def _emit_flags(self, poly, runs, r) -> tuple:
        run = runs[r]
        emit_start = r == 0 or runs[r - 1].vertices[-1] != run.vertices[0]
        emit_end = len(run.vertices) > 1
        if (poly.closed and r == len(runs) - 1 and runs[0].vertices
                and run.vertices[-1] == runs[0].vertices[0]):
            emit_end = False  # closed path: do not duplicate the fixed start
        return emit_start, emit_end

    def _emit_run(self, poly, runs, r, base) -> list:
        run = runs[r]
        emit_start, emit_end = self._emit_flags(poly, runs, r)
        out = []
        if emit_start:
            out.append(self._key_sample(poly, run, base, start=True))
        if run.shared:
            out.extend(self._emit_shared(poly, run, base))
        else:
            out.extend(self._emit_grid(poly, run, base))
        if emit_end:
            out.append(self._key_sample(poly, run, base + run.length, start=False))
        return out

    @staticmethod
    def _key_sample(poly, run, mileage, start) -> Sample:
        v = run.vertices[0] if start else run.vertices[-1]
        if run.segments:
            seg = run.segments[0] if start else run.segments[-1]
            t = Fraction(0) if start else Fraction(1)
            direction = _direction(poly, seg)
        else:
            seg = t = direction = None
        return Sample(id=f"K{v}", mileage=mileage, point=poly.vertices[v].point,
                      kind="key", seg=seg, t=t, direction=direction)

    def _emit_grid(self, poly, run, base) -> list:
        pts = poly.vertices
        out = []
        segs, lens, total = run.segments, run.lengths, run.length
        seg_i = 0
        seg_start = Fraction(0)
        m = self.spacing
        ordinal = 0
        while m < total:
            # advance past segments ending at or before m; zero-length
            # segments therefore never own a sample (no cross interpolation)
            while seg_i + 1 < len(segs) and seg_start + lens[seg_i] <= m:
                seg_start += lens[seg_i]
                seg_i += 1
            t = (m - seg_start) / lens[seg_i]  # lens[seg_i] > 0 here
            a = pts[run.vertices[seg_i]].point
            b = pts[run.vertices[seg_i + 1]].point
            ordinal += 1
            out.append(Sample(
                id=f"G{run.vertices[0]}.{ordinal}",
                mileage=base + m,
                point=tuple(ak + t * (bk - ak) for ak, bk in zip(a, b)),
                kind="grid", seg=segs[seg_i], t=t,
                direction=_direction(poly, segs[seg_i]),
            ))
            m += self.spacing
        return out

    def _emit_shared(self, poly, run, base) -> list:
        if self.registry is None:
            raise ValueError("polyline has shared edges but no SeamRegistry")
        seg = run.segments[0]
        a = poly.vertices[run.vertices[0]].point
        b = poly.vertices[run.vertices[1]].point
        canonical = self.registry.interior_samples(a, b, self.spacing)
        edge_seq = self.registry.edge_id(a, b)
        forward = a == self.registry.canonical_key(a, b)[0]
        total = run.length
        direction = _direction(poly, seg)
        out = []
        for ordinal, cs in enumerate(canonical, start=1):
            local = cs.mileage if forward else total - cs.mileage
            t = cs.t if forward else 1 - cs.t
            out.append(Sample(
                id=f"E{edge_seq}.{ordinal}", mileage=base + local,
                point=cs.point, kind="grid", seg=seg, t=t, direction=direction,
            ))
        out.sort(key=lambda s: s.mileage)
        return out

    # ------------------------------------------------------------------
    # public sampling API
    # ------------------------------------------------------------------
    @property
    def samples(self) -> list:
        return [s for run_samples in self._run_samples for s in run_samples]

    def sample(self, budget=None, cursor: Cursor | None = None) -> Batch:
        """Sample with an optional budget; resume by passing the cursor back."""
        if cursor is not None and cursor.version != self.version:
            raise StaleCursorError(
                "sampler was modified after this cursor was issued"
            )
        if budget is not None and budget < 0:
            raise ValueError("budget must be non-negative")
        start_run = cursor.run_index if cursor else 0
        start_offset = cursor.offset if cursor else 0
        out = []
        for r in range(start_run, len(self._runs)):
            pos = start_offset if r == start_run else 0
            run_samples = self._run_samples[r]
            while pos < len(run_samples):
                if budget is not None and len(out) == budget:
                    return Batch(out, False, Cursor(r, pos, self.version))
                out.append(run_samples[pos])
                pos += 1
        return Batch(out, True, None)

    # ------------------------------------------------------------------
    # incremental editing
    # ------------------------------------------------------------------
    def move_vertex(self, index: int, new_point) -> Patch:
        """Move one control point; resample only the affected runs."""
        old_vertex = self.polyline.vertices[index]
        vertices = list(self.polyline.vertices)
        vertices[index] = Vertex(to_point(new_point), key=old_vertex.key,
                                 break_before=old_vertex.break_before)
        new_polyline = Polyline(vertices, closed=self.polyline.closed,
                                shared=self.polyline.shared)
        new_runs = _build_runs(new_polyline)  # may raise NonRationalLengthError
        if [r.vertices for r in new_runs] != [r.vertices for r in self._runs]:
            raise RuntimeError("run topology changed during a pure move")

        n = len(vertices)
        adjacent = set()
        if n > 1:
            if index > 0:
                adjacent.add(index - 1)
            elif self.polyline.closed:
                adjacent.add(n - 1)
            if index < n - 1:
                adjacent.add(index)
            elif self.polyline.closed:
                adjacent.add(n - 1)
        adjacent = {s for s in adjacent if self.polyline.segment_exists(s)}
        affected = set()
        for r, run in enumerate(self._runs):
            if run.vertices == [index] or any(s in adjacent for s in run.segments):
                affected.add(r)
        if not affected:
            raise ValueError(f"vertex {index} does not belong to any run")
        first, last = min(affected), max(affected)

        base = self._bases[first]
        new_run_samples = {}
        for r in range(first, last + 1):
            new_run_samples[r] = self._emit_run(new_polyline, new_runs, r, base)
            base += new_runs[r].length
        delta = base - (self._bases[last] + self._runs[last].length)

        old_run_samples = {r: self._run_samples[r] for r in range(first, last + 1)}
        old_slice = [s for r in range(first, last + 1) for s in old_run_samples[r]]
        new_slice = [s for r in range(first, last + 1) for s in new_run_samples[r]]
        removed, added = _diff_samples(old_slice, new_slice)

        patch = Patch(
            removed=removed, added=added, mileage_delta=delta,
            insert_at=sum(len(self._run_samples[r]) for r in range(first)),
            affected_range=(self._bases[first],
                            self._bases[last] + self._runs[last].length),
            _first=first, _last=last,
            _old_run_samples=old_run_samples, _new_run_samples=new_run_samples,
            _old_polyline=self.polyline, _new_polyline=new_polyline,
            _old_runs=self._runs, _new_runs=new_runs, _old_bases=self._bases,
        )
        self._apply_patch(patch)
        return patch

    def _apply_patch(self, patch: Patch):
        for r, samples in patch._new_run_samples.items():
            self._run_samples[r] = samples
        for r in range(patch._last + 1, len(self._runs)):
            self._run_samples[r] = [
                replace(s, mileage=s.mileage + patch.mileage_delta)
                for s in self._run_samples[r]
            ]
        bases = list(patch._old_bases)
        base = bases[patch._first]
        for r in range(patch._first, patch._last + 1):
            bases[r] = base
            base += patch._new_runs[r].length
        for r in range(patch._last + 1, len(self._runs)):
            bases[r] += patch.mileage_delta
        self._bases = bases
        self.polyline = patch._new_polyline
        self._runs = patch._new_runs
        self.total_length += patch.mileage_delta
        self.version += 1
        patch.applied = True
        patch._after_version = self.version

    def undo(self, patch: Patch):
        """Revert the most recently applied patch exactly."""
        if not patch.applied or self.version != patch._after_version:
            raise RuntimeError("only the most recently applied patch can be undone")
        for r, samples in patch._old_run_samples.items():
            self._run_samples[r] = samples
        for r in range(patch._last + 1, len(self._runs)):
            self._run_samples[r] = [
                replace(s, mileage=s.mileage - patch.mileage_delta)
                for s in self._run_samples[r]
            ]
        self._bases = list(patch._old_bases)
        self.polyline = patch._old_polyline
        self._runs = patch._old_runs
        self.total_length -= patch.mileage_delta
        self.version -= 1
        patch.applied = False
