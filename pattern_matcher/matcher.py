"""Pattern matching core.

- Injective vertex mapping (two pattern vertices never share a target vertex).
- Positive edges must have at least one witness edge; negative edges none.
- Deterministic output: results sorted by target vertex ids in pattern-id order.
- Dedup strategies:
    VERTEX_MAPPING  -- one result per vertex mapping (parallel target edges
                       never duplicate a mapping);
    EDGE_EMBEDDING  -- one result per combination of concrete witness edges.
- Candidate limit (`max_candidates` per pattern vertex) and `max_matches`.
- `MatchEnumerator`: interruptible / resumable enumeration bound to a graph
  version; resuming on a different version raises StaleVersionError.
"""
from __future__ import annotations

import hashlib
import itertools
from dataclasses import dataclass
from enum import Enum

from .graphs import PatternGraph, TargetGraph, match_predicates


class DedupStrategy(Enum):
    VERTEX_MAPPING = "vertex_mapping"
    EDGE_EMBEDDING = "edge_embedding"


class StaleVersionError(RuntimeError):
    """Raised when an enumerator is used after the target graph changed."""


@dataclass
class Metrics:
    """How much work a matching pass performed."""
    candidates_touched: int = 0      # candidate vertex assignments tested
    matches_revalidated: int = 0     # existing matches re-checked (incremental)

    def __iadd__(self, other: "Metrics") -> "Metrics":
        self.candidates_touched += other.candidates_touched
        self.matches_revalidated += other.matches_revalidated
        return self


@dataclass(frozen=True)
class Match:
    map_id: str
    mapping: tuple                 # sorted ((pattern_vid, target_vid), ...)
    edge_assignment: tuple | None  # sorted ((pattern_eid, target_eid), ...) or None

    @property
    def vertex_mapping(self) -> dict:
        return dict(self.mapping)


def mapping_id(mapping_items, edge_assignment=None) -> str:
    """Stable id derived only from the mapping content (not object identity)."""
    h = hashlib.sha1()
    for pv, tv in mapping_items:
        h.update(f"v:{pv}={tv};".encode("utf-8"))
    for pe, te in edge_assignment or ():
        h.update(f"e:{pe}~{te};".encode("utf-8"))
    return h.hexdigest()[:16]


def match_sort_key(pattern: PatternGraph, m: Match) -> tuple:
    vm = dict(m.mapping)
    key = tuple(vm[pv] for pv in sorted(pattern.vertices))
    return key + tuple(m.edge_assignment or ())


# ---------------------------------------------------------------------------
# Low-level checks
# ---------------------------------------------------------------------------

def _vertex_ok(pvertex, tvertex) -> bool:
    if pvertex.type is not None and tvertex.type != pvertex.type:
        return False
    return match_predicates(pvertex.preds, tvertex.props)


def _edge_witnesses(target: TargetGraph, pattern_edge,
                    src_tv: str, dst_tv: str, at_time) -> list:
    out = []
    for e in target.edges_between(src_tv, dst_tv, at_time=at_time):
        if pattern_edge.type is not None and e.type != pattern_edge.type:
            continue
        if not match_predicates(pattern_edge.preds, e.props):
            continue
        out.append(e)
    return out


def _consistent(target: TargetGraph, pattern: PatternGraph, mapping: dict,
                pv: str, at_time) -> bool:
    """Check all pattern edges incident to `pv` whose other endpoint is mapped."""
    for pe in pattern.edges:
        if pe.src != pv and pe.dst != pv:
            continue
        if pe.src == pe.dst:
            src_tv = dst_tv = mapping[pv]
        else:
            other = pe.dst if pe.src == pv else pe.src
            if other not in mapping:
                continue
            src_tv, dst_tv = mapping[pe.src], mapping[pe.dst]
        witnesses = _edge_witnesses(target, pe, src_tv, dst_tv, at_time)
        if pe.negative:
            if witnesses:
                return False
        elif not witnesses:
            return False
    return True


# ---------------------------------------------------------------------------
# Vertex-mapping enumeration (backtracking, injective, resumable)
# ---------------------------------------------------------------------------

def iter_vertex_mappings(target: TargetGraph, pattern: PatternGraph, *,
                         at_time=None, max_candidates=None, metrics=None,
                         seed=None, resume_after=None, skip_cursor=True):
    """Yield vertex mappings (dict pattern_vid -> target_vid).

    `seed` pins some pattern vertices to fixed target vertices.
    `resume_after` is a path tuple of target ids (in sorted pattern-id order
    of the non-seeded vertices); enumeration skips everything before it, and
    also the cursor path itself unless `skip_cursor` is False.
    """
    order = sorted(pattern.vertices)
    seed = dict(seed or {})
    for pv, tv in seed.items():
        tvertex = target.vertices.get(tv)
        if tvertex is None or not _vertex_ok(pattern.vertices[pv], tvertex):
            return
    if len(set(seed.values())) != len(seed):
        return  # seed itself violates injectivity
    mapping = dict(seed)
    for pv in seed:
        if not _consistent(target, pattern, mapping, pv, at_time):
            return
    used = set(seed.values())
    remaining = [pv for pv in order if pv not in mapping]

    pools = {}
    for pv in remaining:
        pool = [v.id for v in target.vertices.values()
                if _vertex_ok(pattern.vertices[pv], v)]
        pool.sort()
        if max_candidates is not None:
            pool = pool[:max_candidates]
        pools[pv] = pool

    cursor = tuple(resume_after) if resume_after is not None else None

    def rec(idx: int, on_cursor: bool):
        if idx == len(remaining):
            if on_cursor and skip_cursor:
                return
            yield dict(mapping)
            return
        pv = remaining[idx]
        for tv in pools[pv]:
            if tv in used:
                continue
            child_on_cursor = False
            if on_cursor:
                if tv < cursor[idx]:
                    continue
                child_on_cursor = (tv == cursor[idx])
            if metrics is not None:
                metrics.candidates_touched += 1
            mapping[pv] = tv
            used.add(tv)
            if _consistent(target, pattern, mapping, pv, at_time):
                yield from rec(idx + 1, child_on_cursor)
            del mapping[pv]
            used.discard(tv)

    yield from rec(0, cursor is not None)


# ---------------------------------------------------------------------------
# Match expansion (dedup strategies) and one-shot enumeration
# ---------------------------------------------------------------------------

def _expand_mapping(target: TargetGraph, pattern: PatternGraph, vm: dict,
                    at_time, dedup: DedupStrategy) -> list:
    items = tuple(sorted(vm.items()))
    if dedup is DedupStrategy.VERTEX_MAPPING:
        return [Match(map_id=mapping_id(items), mapping=items,
                      edge_assignment=None)]
    pos = pattern.positive_edges
    witness_lists = [
        [e.id for e in _edge_witnesses(target, pe, vm[pe.src], vm[pe.dst], at_time)]
        for pe in pos
    ]
    out = []
    for combo in itertools.product(*witness_lists):
        ea = tuple(sorted((pe.id, te) for pe, te in zip(pos, combo)))
        out.append(Match(map_id=mapping_id(items, ea), mapping=items,
                         edge_assignment=ea))
    return out


def enumerate_matches(target: TargetGraph, pattern: PatternGraph, *,
                      at_time=None, dedup=DedupStrategy.VERTEX_MAPPING,
                      max_candidates=None, max_matches=None,
                      metrics=None) -> list:
    """Full enumeration from scratch; results in stable sorted order."""
    out = []
    for vm in iter_vertex_mappings(target, pattern, at_time=at_time,
                                   max_candidates=max_candidates,
                                   metrics=metrics):
        for m in _expand_mapping(target, pattern, vm, at_time, dedup):
            out.append(m)
            if max_matches is not None and len(out) >= max_matches:
                out.sort(key=lambda m: match_sort_key(pattern, m))
                return out
    out.sort(key=lambda m: match_sort_key(pattern, m))
    return out


# ---------------------------------------------------------------------------
# Interruptible / resumable enumerator
# ---------------------------------------------------------------------------

class MatchEnumerator:
    """Pull-based enumerator bound to a fixed target-graph version.

    `cursor` is an opaque token ((path tuple, embedding index)); pass it as
    `resume_after` to a new enumerator to continue after the last yielded
    match -- valid only while the graph version is unchanged.
    """

    def __init__(self, target: TargetGraph, pattern: PatternGraph, *,
                 at_time=None, dedup=DedupStrategy.VERTEX_MAPPING,
                 max_candidates=None, max_matches=None, resume_after=None,
                 metrics=None):
        self._target = target
        self._pattern = pattern
        self._version = target.version
        self._metrics = metrics if metrics is not None else Metrics()
        self._max_matches = max_matches
        self._emitted = 0
        self._cursor = None
        self._exhausted = False
        self._order = sorted(pattern.vertices)
        self._gen = self._run(at_time, dedup, max_candidates, resume_after)

    @property
    def version(self) -> int:
        return self._version

    @property
    def cursor(self):
        return self._cursor

    @property
    def exhausted(self) -> bool:
        return self._exhausted

    def _check_version(self) -> None:
        if self._target.version != self._version:
            raise StaleVersionError(
                f"enumerator bound to version {self._version}, "
                f"graph is at version {self._target.version}")

    def _run(self, at_time, dedup, max_candidates, resume_after):
        resume_path, resume_emb = (None, -1)
        if resume_after is not None:
            resume_path, resume_emb = resume_after
        skip_cursor = dedup is DedupStrategy.VERTEX_MAPPING
        for vm in iter_vertex_mappings(
                self._target, self._pattern, at_time=at_time,
                max_candidates=max_candidates, metrics=self._metrics,
                resume_after=resume_path, skip_cursor=skip_cursor):
            path = tuple(vm[pv] for pv in self._order)
            for idx, m in enumerate(
                    _expand_mapping(self._target, self._pattern, vm,
                                    at_time, dedup)):
                if (resume_path is not None and path == resume_path
                        and idx <= resume_emb):
                    continue
                self._cursor = (path, idx)
                yield m
                self._emitted += 1
                if (self._max_matches is not None
                        and self._emitted >= self._max_matches):
                    self._exhausted = True
                    return
        self._exhausted = True

    def next_batch(self, n: int) -> list:
        self._check_version()
        out = []
        while len(out) < n and not self._exhausted:
            try:
                out.append(next(self._gen))
            except StopIteration:
                self._exhausted = True
                break
        return out

    def __iter__(self):
        return self

    def __next__(self):
        self._check_version()
        if self._exhausted:
            raise StopIteration
        try:
            return next(self._gen)
        except StopIteration:
            self._exhausted = True
            raise
