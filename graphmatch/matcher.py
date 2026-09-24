"""Incremental injective subgraph matching.

Maintains the full set of vertex mappings under batch edge add/remove
updates without ever rescanning all target vertices or re-validating
all existing mappings:

- A match index (pattern edge, target src, target dst) -> match ids
  locates existing matches affected by a changed edge in O(1), covering
  the two revocation cases: removed positive-edge witnesses and added
  edges that violate negative constraints.
- The two cases that can create matches (added positive-edge witnesses,
  removed edges under negative constraints) enumerate only mappings
  anchored at the changed edge's endpoints.

Every enumerated or located candidate is reconciled against
ground-truth validity: valid mappings are ensured present, invalid ones
ensured absent.
"""
from __future__ import annotations

import itertools
from bisect import bisect_right
from dataclasses import dataclass
from typing import Callable, Dict, Iterable, List, Optional, Set, Tuple

from .graph import Edge, TargetGraph
from .pattern import PatternGraph


class DedupStrategy:
    NONE = "none"                    # every injective mapping is a result
    AUTOMORPHISM = "automorphism"    # quotient by pattern automorphisms


class VersionMismatchError(RuntimeError):
    """Enumeration cursor used against a different graph version."""


class _CandidateLimitExceeded(Exception):
    pass


def mapping_id(mapping: Dict[str, str]) -> str:
    """Stable string id for a vertex mapping, independent of dict order."""
    return ";".join(f"{pv}={mapping[pv]}" for pv in sorted(mapping))


@dataclass
class MatchStats:
    candidates_touched: int = 0   # candidate (re)validations performed
    validity_checks: int = 0      # full-mapping validity evaluations
    truncated: bool = False       # hit the candidate limit


@dataclass(frozen=True)
class Delta:
    version: int
    added: Tuple[str, ...]    # stable mapping ids, sorted
    removed: Tuple[str, ...]


class IncrementalMatcher:
    def __init__(self, graph: TargetGraph, pattern: PatternGraph, *,
                 time: int = 0, dedup: str = DedupStrategy.NONE,
                 max_candidates: int = 1_000_000) -> None:
        self.graph = graph
        self.pattern = pattern
        self.time = time
        self.dedup = dedup
        self.max_candidates = max_candidates
        self.stats = MatchStats()      # cumulative
        self.last_op = MatchStats()    # most recent init/update
        self._version = 0
        self._matches: Dict[str, Tuple[Tuple[str, str], ...]] = {}
        self._index: Dict[Tuple[str, str, str], Set[str]] = {}
        self._automorphisms: Optional[List[Dict[str, str]]] = None
        self._order = self._plan_order(frozenset())
        self._recompute()

    # ---------------- public API ----------------

    @property
    def version(self) -> int:
        return self._version

    def results(self, dedup: Optional[str] = None) -> List[str]:
        """Stable-ordered result mapping ids under the dedup strategy."""
        strategy = dedup or self.dedup
        if strategy == DedupStrategy.NONE:
            return sorted(self._matches)
        if strategy != DedupStrategy.AUTOMORPHISM:
            raise ValueError(f"unknown dedup strategy: {strategy}")
        canonical = set()
        for pairs in self._matches.values():
            m = dict(pairs)
            canonical.add(min(
                mapping_id({pv: m[pi[pv]] for pv in m})
                for pi in self._pattern_automorphisms()))
        return sorted(canonical)

    def get_mapping(self, mid: str) -> Dict[str, str]:
        return dict(self._matches[mid])

    def enumerate(self, limit: Optional[int] = None,
                  cursor: Optional[str] = None,
                  dedup: Optional[str] = None
                  ) -> Tuple[List[str], Optional[str]]:
        """Page through results; resume with the cursor from a previous call.

        The cursor pins the graph version: resuming after an update raises
        VersionMismatchError, so enumeration always reflects one version.
        """
        ids = self.results(dedup)
        start = 0
        if cursor is not None:
            ver, _, last = cursor.partition("|")
            if int(ver) != self._version:
                raise VersionMismatchError(
                    f"cursor is for version {ver}, graph is at {self._version}")
            start = bisect_right(ids, last)
        page = ids[start:] if limit is None else ids[start:start + limit]
        next_cursor = None
        if page and start + len(page) < len(ids):
            next_cursor = f"{self._version}|{page[-1]}"
        return page, next_cursor

    def apply_update(self, added_edges: Iterable[Edge] = (),
                     removed_edge_ids: Iterable[str] = ()) -> Delta:
        """Apply a batch edge add/remove and incrementally maintain matches."""
        added_edges = list(added_edges)
        removed = [self.graph.edges[eid] for eid in removed_edge_ids]
        for e in removed:
            self.graph.remove_edge(e.id)
        for e in added_edges:
            self.graph.add_edge(e)

        stats = MatchStats()
        enum_anchors: Set[Tuple[str, str, str]] = set()   # may create matches
        check_anchors: Set[Tuple[str, str, str]] = set()  # may revoke matches
        for e in removed:
            for pe in self._compatible_pattern_edges(e):
                (check_anchors if pe.exists else enum_anchors).add(
                    (pe.id, e.src, e.dst))
        for e in added_edges:
            for pe in self._compatible_pattern_edges(e):
                (enum_anchors if pe.exists else check_anchors).add(
                    (pe.id, e.src, e.dst))

        changes: Dict[str, bool] = {}

        def reconcile(mid: str, m: Dict[str, str]) -> None:
            present = changes[mid] if mid in changes else mid in self._matches
            stats.candidates_touched += 1
            if stats.candidates_touched > self.max_candidates:
                raise _CandidateLimitExceeded
            if self._mapping_valid(m, stats):
                if not present:
                    self._put_match(mid, m)
                    changes[mid] = True
            elif present:
                self._drop_match(mid)
                changes[mid] = False

        try:
            for peid, u, v in sorted(check_anchors):
                for mid in sorted(self._index.get((peid, u, v), ())):
                    if mid in self._matches:
                        reconcile(mid, dict(self._matches[mid]))
            for peid, u, v in sorted(enum_anchors):
                pe = self.pattern.edges_by_id[peid]
                self._enumerate({pe.src: u, pe.dst: v}, reconcile, stats)
        except _CandidateLimitExceeded:
            stats.truncated = True

        self._version += 1
        self._accumulate(stats)
        return Delta(
            version=self._version,
            added=tuple(sorted(mid for mid, c in changes.items() if c)),
            removed=tuple(sorted(mid for mid, c in changes.items() if not c)),
        )

    # ---------------- match store ----------------

    def _put_match(self, mid: str, m: Dict[str, str]) -> None:
        self._matches[mid] = tuple(sorted(m.items()))
        for pe in self.pattern.edges:
            self._index.setdefault((pe.id, m[pe.src], m[pe.dst]),
                                   set()).add(mid)

    def _drop_match(self, mid: str) -> None:
        m = dict(self._matches.pop(mid))
        for pe in self.pattern.edges:
            key = (pe.id, m[pe.src], m[pe.dst])
            bucket = self._index.get(key)
            if bucket is not None:
                bucket.discard(mid)
                if not bucket:
                    del self._index[key]

    def _compatible_pattern_edges(self, edge: Edge):
        for pe in self.pattern.edges:
            if pe.type is None or pe.type == edge.type:
                if pe.src == pe.dst and edge.src != edge.dst:
                    continue  # self-loop constraint unaffected by non-loop
                yield pe

    # ---------------- internals ----------------

    def _accumulate(self, stats: MatchStats) -> None:
        self.last_op = stats
        self.stats.candidates_touched += stats.candidates_touched
        self.stats.validity_checks += stats.validity_checks
        self.stats.truncated = self.stats.truncated or stats.truncated

    def _recompute(self) -> None:
        self._matches = {}
        self._index = {}
        stats = MatchStats()

        def visit(mid: str, m: Dict[str, str]) -> None:
            if self._mapping_valid(m, stats):
                self._put_match(mid, m)

        try:
            self._enumerate({}, visit, stats)
        except _CandidateLimitExceeded:
            stats.truncated = True
        self._accumulate(stats)

    def _plan_order(self, anchored: frozenset) -> List[str]:
        adj: Dict[str, set] = {pid: set() for pid in self.pattern.vertices}
        for e in self.pattern.edges:
            adj[e.src].add(e.dst)
            adj[e.dst].add(e.src)
        order: List[str] = sorted(anchored)
        remaining = set(self.pattern.vertices)
        remaining.difference_update(order)
        while remaining:
            done = set(order)
            nxt = min(remaining, key=lambda p: (-len(adj[p] & done), p))
            order.append(nxt)
            remaining.discard(nxt)
        return order

    def _vertex_ok(self, pvid: str, tvid: str) -> bool:
        pv = self.pattern.vertices[pvid]
        tv = self.graph.vertices[tvid]
        if pv.type is not None and pv.type != tv.type:
            return False
        return all(p.test(tv.attrs) for p in pv.predicates)

    def _edge_witness_exists(self, pe, u: str, v: str) -> bool:
        for e in self.graph.active_edges_between(u, v, self.time):
            if pe.type is not None and e.type != pe.type:
                continue
            if all(p.test(e.attrs) for p in pe.predicates):
                return True
        return False

    def _mapping_valid(self, m: Dict[str, str], stats: MatchStats) -> bool:
        stats.validity_checks += 1
        for pe in self.pattern.edges:
            witness = self._edge_witness_exists(pe, m[pe.src], m[pe.dst])
            if pe.exists and not witness:
                return False
            if not pe.exists and witness:
                return False
        return True

    def _enumerate(self, anchors: Dict[str, str],
                   visit: Callable[[str, Dict[str, str]], None],
                   stats: MatchStats) -> None:
        order = (self._order if not anchors
                 else self._plan_order(frozenset(anchors)))
        mapping: Dict[str, str] = {}
        used: set = set()

        def candidates_for(pvid: str) -> Tuple[str, ...]:
            if pvid in anchors:
                return (anchors[pvid],)
            best: Optional[set] = None
            for pe in self.pattern.edges:
                if not pe.exists:
                    continue
                cands = None
                if pe.src == pvid and pe.dst in mapping:
                    cands = {e.src for e in
                             self.graph.active_in(mapping[pe.dst], self.time)
                             if (pe.type is None or e.type == pe.type)
                             and all(p.test(e.attrs) for p in pe.predicates)}
                elif pe.dst == pvid and pe.src in mapping:
                    cands = {e.dst for e in
                             self.graph.active_out(mapping[pe.src], self.time)
                             if (pe.type is None or e.type == pe.type)
                             and all(p.test(e.attrs) for p in pe.predicates)}
                if cands is not None and (best is None or len(cands) < len(best)):
                    best = cands
            if best is None:
                pv = self.pattern.vertices[pvid]
                return tuple(vid for vid in sorted(self.graph.vertices)
                             if pv.type is None
                             or self.graph.vertices[vid].type == pv.type)
            return tuple(sorted(best))

        def rec(i: int) -> None:
            if i == len(order):
                visit(mapping_id(mapping), dict(mapping))
                return
            pvid = order[i]
            for tvid in candidates_for(pvid):
                if tvid in used:
                    continue
                stats.candidates_touched += 1
                if stats.candidates_touched > self.max_candidates:
                    raise _CandidateLimitExceeded
                if not self._vertex_ok(pvid, tvid):
                    continue
                mapping[pvid] = tvid
                used.add(tvid)
                rec(i + 1)
                del mapping[pvid]
                used.discard(tvid)

        rec(0)

    def _pattern_automorphisms(self) -> List[Dict[str, str]]:
        if self._automorphisms is None:
            pvs = sorted(self.pattern.vertices)
            self._automorphisms = [
                dict(zip(pvs, perm))
                for perm in itertools.permutations(pvs)
                if self._is_automorphism(dict(zip(pvs, perm)))
            ]
        return self._automorphisms

    def _is_automorphism(self, pi: Dict[str, str]) -> bool:
        for pv in self.pattern.vertices.values():
            qv = self.pattern.vertices[pi[pv.id]]
            if pv.type != qv.type or pv.predicates != qv.predicates:
                return False

        def signature(apply_pi: bool) -> List[str]:
            rows = []
            for e in self.pattern.edges:
                s, d = (pi[e.src], pi[e.dst]) if apply_pi else (e.src, e.dst)
                rows.append(repr((s, d, e.type, e.exists, e.predicates)))
            return sorted(rows)

        return signature(False) == signature(True)
