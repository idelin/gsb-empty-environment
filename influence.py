"""Conditional dependency influence propagation.

Given a set of changed seed nodes and conditional dependency edges, compute
the affected node set together with a shortest explanation chain per node.

Design rules:
- Edge conditions are evaluated against an explicit fact set.
- Fact changes re-derive explanations (incremental update semantics):
  revoking a fact that made an edge hold removes every node that loses its
  last piece of evidence; nodes with alternative evidence stay affected.
- Vertices and edges have string IDs; isolated vertices and parallel edges
  are preserved.
- Time intervals are half-open: [start, end).
- Cyclic dependencies cannot self-prove: only chains grounded in a seed
  node count as evidence.
"""

from __future__ import annotations

from collections import deque
from dataclasses import dataclass
from types import MappingProxyType
from typing import Iterable, Mapping


@dataclass(frozen=True)
class Condition:
    """Edge condition over the explicit fact set.

    Holds when every fact in ``all_of`` is present and at least one fact
    in ``any_of`` is present (an empty ``any_of`` imposes no requirement).
    """

    all_of: frozenset[str] = frozenset()
    any_of: frozenset[str] = frozenset()

    def __init__(self, all_of: Iterable[str] = (), any_of: Iterable[str] = ()) -> None:
        object.__setattr__(self, "all_of", frozenset(all_of))
        object.__setattr__(self, "any_of", frozenset(any_of))

    def holds(self, facts: frozenset[str] | set[str]) -> bool:
        return self.all_of <= facts and (not self.any_of or not self.any_of.isdisjoint(facts))

    def satisfied_by(self, facts: frozenset[str] | set[str]) -> frozenset[str]:
        """Return the facts that witness this condition under ``facts``."""
        return self.all_of | (self.any_of & facts)


@dataclass(frozen=True)
class Edge:
    """A conditional dependency edge: ``src`` influences ``dst``.

    ``start`` is inclusive, ``end`` is exclusive (half-open interval).
    ``None`` means unbounded on that side.
    """

    eid: str
    src: str
    dst: str
    condition: Condition = Condition()
    start: float | None = None
    end: float | None = None

    def active(self, facts: frozenset[str] | set[str], at: float | None) -> bool:
        if at is not None:
            if self.start is not None and at < self.start:
                return False
            if self.end is not None and at >= self.end:
                return False
        return self.condition.holds(facts)


@dataclass(frozen=True)
class Step:
    """One hop of an explanation chain."""

    edge: str
    source: str
    target: str
    facts: frozenset[str]


@dataclass(frozen=True)
class Explanation:
    """Shortest evidence chain grounding ``node`` in a changed seed."""

    node: str
    steps: tuple[Step, ...] = ()

    @property
    def depth(self) -> int:
        return len(self.steps)

    @property
    def facts(self) -> frozenset[str]:
        used: set[str] = set()
        for step in self.steps:
            used |= step.facts
        return frozenset(used)

    def render(self) -> str:
        if not self.steps:
            return f"{self.node} (changed seed)"
        parts = [self.steps[0].source]
        for step in self.steps:
            parts.append(f"-[{step.edge}]-> {step.target}")
        return " ".join(parts)


class InfluenceGraph:
    """Vertices, conditional edges, an explicit fact set and changed seeds."""

    def __init__(self) -> None:
        self._vertices: set[str] = set()
        self._edges: dict[str, Edge] = {}
        self._outgoing: dict[str, list[str]] = {}
        self._facts: set[str] = set()
        self._seeds: set[str] = set()

    # --- structure -------------------------------------------------------
    def add_vertex(self, vid: str) -> None:
        self._vertices.add(vid)

    def add_edge(
        self,
        eid: str,
        src: str,
        dst: str,
        condition: Condition | None = None,
        start: float | None = None,
        end: float | None = None,
    ) -> None:
        if eid in self._edges:
            raise ValueError(f"duplicate edge id: {eid!r}")
        if start is not None and end is not None and not start < end:
            raise ValueError(f"empty interval [{start}, {end})")
        self._vertices.update((src, dst))
        self._edges[eid] = Edge(eid, src, dst, condition or Condition(), start, end)
        self._outgoing.setdefault(src, []).append(eid)

    def remove_edge(self, eid: str) -> None:
        edge = self._edges.pop(eid)
        self._outgoing[edge.src].remove(eid)

    @property
    def vertices(self) -> frozenset[str]:
        return frozenset(self._vertices)

    @property
    def isolated(self) -> frozenset[str]:
        """Vertices that appear in no edge (kept, never auto-affected)."""
        connected = {e.src for e in self._edges.values()} | {e.dst for e in self._edges.values()}
        return frozenset(self._vertices - connected)

    # --- facts -----------------------------------------------------------
    def add_fact(self, *fids: str) -> None:
        self._facts.update(fids)

    def remove_fact(self, *fids: str) -> None:
        self._facts.difference_update(fids)

    @property
    def facts(self) -> frozenset[str]:
        return frozenset(self._facts)

    # --- changed seeds ---------------------------------------------------
    def mark_changed(self, *vids: str) -> None:
        self._vertices.update(vids)
        self._seeds.update(vids)

    def unmark_changed(self, *vids: str) -> None:
        self._seeds.difference_update(vids)

    @property
    def seeds(self) -> frozenset[str]:
        return frozenset(self._seeds)

    # --- query -----------------------------------------------------------
    def affected(self, at: float | None = None) -> Mapping[str, Explanation]:
        """Affected set with shortest explanations, derived from current facts.

        Every call re-derives the result from the live fact set, so fact
        additions and revocations are reflected exactly (no stale appends).
        ``at=None`` ignores time intervals.
        """
        return MappingProxyType(self._propagate(at))

    def _propagate(self, at: float | None) -> dict[str, Explanation]:
        # BFS from the changed seeds: first time a node is reached it is via
        # a shortest chain (fewest edges). A pure cycle never gets seeded,
        # so cycles cannot self-prove.
        explained: dict[str, Explanation] = {
            s: Explanation(s, ()) for s in sorted(self._seeds)
        }
        queue: deque[str] = deque(sorted(self._seeds))
        while queue:
            current = queue.popleft()
            base = explained[current]
            for eid in sorted(self._outgoing.get(current, ())):
                edge = self._edges[eid]
                if edge.dst in explained:
                    continue
                if not edge.active(self._facts, at):
                    continue
                step = Step(
                    edge=edge.eid,
                    source=edge.src,
                    target=edge.dst,
                    facts=edge.condition.satisfied_by(self._facts),
                )
                explained[edge.dst] = Explanation(edge.dst, base.steps + (step,))
                queue.append(edge.dst)
        return explained
