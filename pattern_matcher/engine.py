"""Incremental match maintenance over a mutable target graph.

The engine keeps the full match set and, for each batch of edge
additions/removals, emits an add/remove diff of matches with stable mapping
ids. It never rescans the whole target graph:

- Revocation: only existing matches whose image touches an endpoint of a
  changed edge are re-validated. (A removed positive witness or an added
  negative witness must touch the match's image.)
- Discovery: new matches are grown from small seed mappings:
    * an added edge can only create matches that use it as a witness for
      some positive pattern edge;
    * a removed edge can only create matches that it previously blocked as
      a witness of some negative pattern edge.
  Each seed fixes the corresponding pattern-edge endpoints, so completion
  enumeration stays local to the changed neighbourhood.

`Metrics.candidates_touched` / `matches_revalidated` record the touched
candidate volume of every update.
"""
from __future__ import annotations

from dataclasses import dataclass, field

from .graphs import Edge, PatternGraph, TargetGraph, match_predicates
from .matcher import (DedupStrategy, Match, Metrics, _edge_witnesses,
                      _expand_mapping, iter_vertex_mappings, match_sort_key)


@dataclass(frozen=True)
class Diff:
    added: tuple          # Match, stable sorted order
    removed: tuple        # Match, stable sorted order
    version: int
    metrics: Metrics


def _make_seed(pattern_edge, edge: Edge):
    """Seed mapping pinning a pattern edge onto a concrete target edge."""
    if pattern_edge.type is not None and edge.type != pattern_edge.type:
        return None
    if not match_predicates(pattern_edge.preds, edge.props):
        return None
    seed = {pattern_edge.src: edge.src, pattern_edge.dst: edge.dst}
    if len(set(seed.values())) != len(seed):
        return None  # would violate injectivity
    return seed


class IncrementalMatchEngine:
    def __init__(self, target: TargetGraph, pattern: PatternGraph, *,
                 at_time=None, dedup=DedupStrategy.VERTEX_MAPPING,
                 max_candidates=None):
        self.target = target
        self.pattern = pattern
        self.at_time = at_time
        self.dedup = dedup
        self.max_candidates = max_candidates
        self._matches: dict[str, Match] = {}
        self._version = 0
        self.last_metrics = Metrics()
        self._initial_load()

    # -- introspection -----------------------------------------------------
    @property
    def version(self) -> int:
        return self._version

    @property
    def matches(self) -> tuple:
        return tuple(sorted(self._matches.values(),
                            key=lambda m: match_sort_key(self.pattern, m)))

    def _initial_load(self) -> None:
        metrics = Metrics()
        for vm in iter_vertex_mappings(self.target, self.pattern,
                                       at_time=self.at_time,
                                       max_candidates=self.max_candidates,
                                       metrics=metrics):
            for m in _expand_mapping(self.target, self.pattern, vm,
                                     self.at_time, self.dedup):
                self._matches[m.map_id] = m
        self.last_metrics = metrics

    # -- validity ----------------------------------------------------------
    def _still_valid(self, m: Match) -> bool:
        vm = dict(m.mapping)
        if m.edge_assignment is not None:
            for _pe_id, te_id in m.edge_assignment:
                e = self.target.edges.get(te_id)
                if e is None:
                    return False
                if self.at_time is not None and not e.active_at(self.at_time):
                    return False
        for pe in self.pattern.negative_edges:
            if _edge_witnesses(self.target, pe, vm[pe.src], vm[pe.dst],
                               self.at_time):
                return False
        if m.edge_assignment is None:
            for pe in self.pattern.positive_edges:
                if not _edge_witnesses(self.target, pe, vm[pe.src], vm[pe.dst],
                                       self.at_time):
                    return False
        return True

    # -- updates -------------------------------------------------------------
    def apply_updates(self, *, add_edges=(), remove_edge_ids=()) -> Diff:
        metrics = Metrics()
        removed_edges = self.target.remove_edges(remove_edge_ids)
        added_edges = self.target.add_edges(add_edges)

        affected = set()
        for e in removed_edges + added_edges:
            affected.add(e.src)
            affected.add(e.dst)

        # 1. Revoke: re-validate only matches touching affected vertices.
        removed_matches = []
        for m in list(self._matches.values()):
            if affected.isdisjoint(tv for _pv, tv in m.mapping):
                continue
            metrics.matches_revalidated += 1
            if not self._still_valid(m):
                del self._matches[m.map_id]
                removed_matches.append(m)

        # 2. Discover: grow seeds from changed edges.
        seeds = []
        for e in added_edges:
            for pe in self.pattern.positive_edges:
                s = _make_seed(pe, e)
                if s is not None:
                    seeds.append(s)
        for e in removed_edges:
            for pe in self.pattern.negative_edges:
                s = _make_seed(pe, e)
                if s is not None:
                    seeds.append(s)

        added_matches = []
        seen = set()
        for s in seeds:
            for vm in iter_vertex_mappings(
                    self.target, self.pattern, at_time=self.at_time,
                    max_candidates=self.max_candidates, metrics=metrics,
                    seed=s):
                for m in _expand_mapping(self.target, self.pattern, vm,
                                         self.at_time, self.dedup):
                    if m.map_id in self._matches or m.map_id in seen:
                        continue
                    seen.add(m.map_id)
                    self._matches[m.map_id] = m
                    added_matches.append(m)

        self._version += 1
        self.last_metrics = metrics
        key = lambda m: match_sort_key(self.pattern, m)
        return Diff(added=tuple(sorted(added_matches, key=key)),
                    removed=tuple(sorted(removed_matches, key=key)),
                    version=self._version, metrics=metrics)
