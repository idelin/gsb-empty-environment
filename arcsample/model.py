"""Path model: vertices, segments, sub-paths separated by breakpoints.

Defined degenerate behaviour (no epsilon anywhere):

* consecutive identical points form a zero-length segment.  It contributes
  nothing to the mileage and never owns a sample; interpolation never
  crosses it.
* ``BREAK`` between two runs splits the path into independent sub-paths.
  Mileage restarts at zero per sub-path and no interpolation ever crosses
  a breakpoint.
* a run of a single point yields exactly one keypoint sample whose
  direction is ``None``.
* a segment whose length is irrational raises ``NonRationalLengthError``
  at construction time.
"""

from __future__ import annotations

from dataclasses import dataclass
from fractions import Fraction
from typing import List, Optional, Sequence, Tuple

from .rational import Point, as_point, distance


class _BreakSentinel:
    _instance: Optional["_BreakSentinel"] = None

    def __new__(cls) -> "_BreakSentinel":
        if cls._instance is None:
            cls._instance = super().__new__(cls)
        return cls._instance

    def __repr__(self) -> str:  # pragma: no cover - cosmetic
        return "BREAK"


BREAK = _BreakSentinel()


@dataclass(frozen=True)
class Vertex:
    id: str
    point: Point


@dataclass(frozen=True)
class Segment:
    """One directed edge of a sub-path."""

    index: int                 # index inside its sub-path
    start_id: str
    end_id: str
    start: Point
    end: Point
    length: Fraction           # exact, may be zero
    unit: Optional[Point]      # exact unit direction, None for zero-length


@dataclass(frozen=True)
class SubPath:
    """A connected run of vertices; mileage starts at 0."""

    index: int
    vertices: Tuple[Vertex, ...]
    closed: bool
    segments: Tuple[Segment, ...]
    cum: Tuple[Fraction, ...]  # cum[i] = mileage of vertex i; cum[-1] = total length

    @property
    def length(self) -> Fraction:
        return self.cum[-1]


def _build_subpath(index: int, vertices: Tuple[Vertex, ...], closed: bool) -> SubPath:
    segments: List[Segment] = []
    count = len(vertices) - 1 if not closed else len(vertices)
    for i in range(max(count, 0)):
        a = vertices[i]
        b = vertices[(i + 1) % len(vertices)]
        seg_len = distance(a.point, b.point)
        if seg_len > 0:
            unit = tuple((y - x) / seg_len for x, y in zip(a.point, b.point))
        else:
            unit = None
        segments.append(Segment(i, a.id, b.id, a.point, b.point, seg_len, unit))
    cum = [Fraction(0)]
    for seg in segments:
        cum.append(cum[-1] + seg.length)
    return SubPath(index, vertices, closed, tuple(segments), tuple(cum))


class Path:
    """An immutable multi-run polyline with stable vertex ids."""

    def __init__(
        self,
        points: Sequence,
        *,
        ids: Optional[Sequence[str]] = None,
        closed: bool = False,
        path_id: str = "p",
    ):
        runs: List[List[Point]] = [[]]
        for entry in points:
            if entry is BREAK:
                if not runs[-1]:
                    raise ValueError("BREAK cannot start a path or follow another BREAK")
                runs.append([])
            else:
                runs[-1].append(as_point(entry))
        if not runs[-1]:
            raise ValueError("path cannot end with BREAK")
        if not any(runs):
            raise ValueError("path needs at least one point")
        total = sum(len(run) for run in runs)
        if ids is None:
            ids = [f"v{i}" for i in range(total)]
        ids = list(ids)
        if len(ids) != total:
            raise ValueError(f"expected {total} vertex ids, got {len(ids)}")
        if len(set(ids)) != total:
            raise ValueError("vertex ids must be unique")
        if closed and len(runs) != 1:
            raise ValueError("a closed path must consist of a single run")

        self.path_id = path_id
        self.closed = closed
        self._spec = list(points)
        self._ids = ids

        subpaths: List[SubPath] = []
        cursor = 0
        for run_index, run in enumerate(runs):
            vertices = tuple(
                Vertex(ids[cursor + j], point) for j, point in enumerate(run)
            )
            cursor += len(run)
            subpaths.append(_build_subpath(run_index, vertices, closed))
        self.subpaths: Tuple[SubPath, ...] = tuple(subpaths)
        self._index = {
            v.id: (sp.index, i) for sp in subpaths for i, v in enumerate(sp.vertices)
        }

    def vertex_index(self, vertex_id: str) -> Tuple[int, int]:
        try:
            return self._index[vertex_id]
        except KeyError:
            raise KeyError(f"unknown vertex id {vertex_id!r}") from None

    def replacing(self, vertex_id: str, new_point) -> "Path":
        """Return a new Path with one vertex moved; ids and structure kept."""
        self.vertex_index(vertex_id)  # validate
        new_point = as_point(new_point)
        spec: List = []
        seen = 0
        for entry in self._spec:
            if entry is BREAK:
                spec.append(BREAK)
                continue
            if self._ids[seen] == vertex_id:
                spec.append(new_point)
            else:
                spec.append(entry)
            seen += 1
        return Path(spec, ids=self._ids, closed=self.closed, path_id=self.path_id)
