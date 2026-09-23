from __future__ import annotations

from dataclasses import dataclass, field
import heapq
from typing import Any, Iterable


@dataclass(frozen=True)
class ExplanationStep:
    source: str
    edge_id: str
    target: str
    facts: tuple[str, ...]


@dataclass(frozen=True)
class Explanation:
    vertex: str
    root: str
    chain: tuple[str, ...]
    edges: tuple[str, ...]
    facts: tuple[str, ...]
    steps: tuple[ExplanationStep, ...]


class Condition:
    def evaluate(self, facts: set[str]) -> tuple[bool, tuple[str, ...]]:
        raise NotImplementedError


@dataclass(frozen=True)
class Always(Condition):
    def evaluate(self, facts: set[str]) -> tuple[bool, tuple[str, ...]]:
        return True, ()


@dataclass(frozen=True)
class Never(Condition):
    def evaluate(self, facts: set[str]) -> tuple[bool, tuple[str, ...]]:
        return False, ()


@dataclass(frozen=True)
class FactCondition(Condition):
    name: str

    def evaluate(self, facts: set[str]) -> tuple[bool, tuple[str, ...]]:
        if self.name in facts:
            return True, (self.name,)
        return False, ()


@dataclass(frozen=True)
class AllCondition(Condition):
    children: tuple[Condition, ...]

    def evaluate(self, facts: set[str]) -> tuple[bool, tuple[str, ...]]:
        witnesses: set[str] = set()
        for child in self.children:
            satisfied, witness = child.evaluate(facts)
            if not satisfied:
                return False, ()
            witnesses.update(witness)
        return True, tuple(sorted(witnesses))


@dataclass(frozen=True)
class AnyCondition(Condition):
    children: tuple[Condition, ...]

    def evaluate(self, facts: set[str]) -> tuple[bool, tuple[str, ...]]:
        alternatives: list[tuple[str, ...]] = []
        for child in self.children:
            satisfied, witness = child.evaluate(facts)
            if satisfied:
                alternatives.append(tuple(sorted(set(witness))))
        if not alternatives:
            return False, ()
        return True, min(alternatives)


@dataclass(frozen=True)
class NotCondition(Condition):
    child: Condition

    def evaluate(self, facts: set[str]) -> tuple[bool, tuple[str, ...]]:
        satisfied, _ = self.child.evaluate(facts)
        return not satisfied, ()


def condition(value: Any) -> Condition:
    if value is None:
        return Always()
    if isinstance(value, Condition):
        return value
    if isinstance(value, str):
        return FactCondition(value)
    if not isinstance(value, tuple) or not value:
        raise ValueError("condition must be None, a string, or a non-empty tuple")

    operator, *raw_children = value
    children = tuple(condition(child) for child in raw_children)
    if operator == "all":
        return AllCondition(children) if children else Always()
    if operator == "any":
        return AnyCondition(children) if children else Never()
    if operator == "not":
        if len(children) != 1:
            raise ValueError("not requires exactly one child")
        return NotCondition(children[0])
    raise ValueError(f"unsupported condition operator: {operator!r}")


@dataclass
class Edge:
    id: str
    source: str
    target: str
    condition: Condition = field(default_factory=Always)
    start: Any = None
    end: Any = None
    active: bool = False
    witness: tuple[str, ...] = ()

    def __post_init__(self) -> None:
        require_id(self.id, "edge")
        require_id(self.source, "source vertex")
        require_id(self.target, "target vertex")
        if self.start is not None and self.end is not None and not self.start < self.end:
            raise ValueError("time interval must satisfy start < end")
        self.condition = condition(self.condition)


def require_id(value: str, description: str) -> None:
    if not isinstance(value, str) or not value:
        raise ValueError(f"{description} id must be a non-empty string")


class ImpactGraph:
    def __init__(self) -> None:
        self.vertices: set[str] = set()
        self.changes: set[str] = set()
        self.facts: set[str] = set()
        self.time: Any = None
        self.edges: dict[str, Edge] = {}
        self.outgoing: dict[str, list[str]] = {}
        self.parents: dict[str, tuple[str, str, tuple[str, ...]]] = {}
        self.path_keys: dict[str, tuple[Any, ...]] = {}

    def add_vertex(self, vertex_id: str) -> None:
        require_id(vertex_id, "vertex")
        self.vertices.add(vertex_id)
        self.outgoing.setdefault(vertex_id, [])

    def add_edge(
        self,
        edge_id: str,
        source: str,
        target: str,
        condition_value: Any = None,
        start: Any = None,
        end: Any = None,
    ) -> None:
        if edge_id in self.edges:
            raise ValueError(f"duplicate edge id: {edge_id}")
        edge = Edge(edge_id, source, target, condition(condition_value), start, end)
        if source not in self.vertices or target not in self.vertices:
            raise KeyError("edge endpoints must be added before the edge")
        self.edges[edge_id] = edge
        self.outgoing[source].append(edge_id)
        self._evaluate_edge(edge)
        if edge.active:
            self._relax_from_edge(edge_id)

    def add_change(self, vertex_id: str) -> None:
        require_id(vertex_id, "vertex")
        if vertex_id not in self.vertices:
            raise KeyError(f"unknown vertex: {vertex_id}")
        self.changes.add(vertex_id)
        if self.parents.get(vertex_id, (None,))[0] != vertex_id:
            self.parents[vertex_id] = (vertex_id, "", ())
            self.path_keys[vertex_id] = (0, vertex_id)
            self._relax_from_vertex(vertex_id)

    def remove_change(self, vertex_id: str) -> None:
        if vertex_id not in self.changes:
            return
        self.changes.remove(vertex_id)
        if self.parents.get(vertex_id, (None,))[0] == vertex_id:
            self._invalidate_targets([vertex_id])
            self._repair_invalid()

    def update_facts(self, add: Iterable[str] = (), remove: Iterable[str] = ()) -> None:
        added = set(add)
        removed = set(remove)
        added.difference_update(removed)
        self.facts.update(added)
        self.facts.difference_update(removed)
        self._reconcile_edges(self.edges.values())

    def set_time(self, time: Any) -> None:
        if time != self.time:
            self.time = time
            self._reconcile_edges(self.edges.values())

    def affected_vertices(self) -> tuple[str, ...]:
        return tuple(sorted(self.parents))

    def explain(self, vertex_id: str) -> Explanation | None:
        if vertex_id not in self.parents:
            return None

        steps_reversed: list[ExplanationStep] = []
        facts: set[str] = set()
        current = vertex_id
        while self.parents[current][0] != current:
            parent, edge_id, witness = self.parents[current]
            steps_reversed.append(ExplanationStep(parent, edge_id, current, witness))
            facts.update(witness)
            current = parent

        steps = tuple(reversed(steps_reversed))
        chain = [current]
        chain.extend(step.target for step in steps)
        return Explanation(
            vertex_id,
            current,
            tuple(chain),
            tuple(step.edge_id for step in steps),
            tuple(sorted(facts)),
            steps,
        )

    def explain_all(self) -> dict[str, Explanation]:
        return {
            vertex_id: explanation
            for vertex_id in self.affected_vertices()
            if (explanation := self.explain(vertex_id)) is not None
        }

    def _reconcile_edges(self, edges: Iterable[Edge]) -> None:
        invalid_targets: set[str] = set()
        changed_edges: list[Edge] = []
        for edge in edges:
            was_active = edge.active
            old_witness = edge.witness
            self._evaluate_edge(edge)
            selected = self.parents.get(edge.target, (None, ""))[1] == edge.id
            if was_active and not edge.active and selected:
                invalid_targets.add(edge.target)
            elif edge.active and selected and (not was_active or edge.witness != old_witness):
                invalid_targets.add(edge.target)
                changed_edges.append(edge)
            elif edge.active and not was_active:
                changed_edges.append(edge)

        if invalid_targets:
            self._invalidate_targets(invalid_targets)
            self._repair_invalid()

        relaxed_edges = [
            edge.id for edge in changed_edges
            if edge.active and edge.source in self.parents
        ]
        if relaxed_edges:
            self._relax_edges(relaxed_edges)

    def _evaluate_edge(self, edge: Edge) -> None:
        if not self._in_interval(edge):
            edge.active = False
            edge.witness = ()
            return
        satisfied, witness = edge.condition.evaluate(self.facts)
        edge.active = satisfied
        edge.witness = witness

    def _in_interval(self, edge: Edge) -> bool:
        if edge.start is None and edge.end is None:
            return True
        if self.time is None:
            return False
        if edge.start is not None and self.time < edge.start:
            return False
        if edge.end is not None and self.time >= edge.end:
            return False
        return True

    def _candidate_key(self, edge: Edge) -> tuple[Any, ...]:
        source_key = self.path_keys[edge.source]
        return (source_key[0] + 1,) + source_key[1:] + (edge.id, edge.target)

    def _relax_from_vertex(self, source: str) -> None:
        self._relax_edges(self.outgoing[source])

    def _relax_from_edge(self, edge_id: str) -> None:
        self._relax_edges([edge_id])

    def _relax_edges(self, initial_edge_ids: Iterable[str]) -> None:
        heap: list[tuple[tuple[Any, ...], str]] = []

        def offer(edge_id: str) -> None:
            edge = self.edges[edge_id]
            if not edge.active or edge.source not in self.parents:
                return
            heapq.heappush(heap, (self._candidate_key(edge), edge_id))

        for edge_id in initial_edge_ids:
            offer(edge_id)

        while heap:
            key, edge_id = heapq.heappop(heap)
            edge = self.edges[edge_id]
            if edge.source not in self.parents or self._candidate_key(edge) != key:
                continue
            current = self.path_keys.get(edge.target)
            if current is not None and current <= key:
                continue
            self.parents[edge.target] = (edge.source, edge.id, edge.witness)
            self.path_keys[edge.target] = key
            for next_edge_id in self.outgoing[edge.target]:
                offer(next_edge_id)

    def _invalidate_targets(self, targets: Iterable[str]) -> set[str]:
        pending = set(targets)
        invalid: set[str] = set()
        while pending:
            target = pending.pop()
            if target in invalid or target not in self.parents:
                continue
            invalid.add(target)
            del self.parents[target]
            del self.path_keys[target]
            for child, relation in list(self.parents.items()):
                if relation[0] == target and child != target:
                    pending.add(child)
        return invalid

    def _repair_invalid(self) -> None:
        local_keys: dict[str, tuple[Any, ...]] = {}
        heap: list[tuple[tuple[Any, ...], str]] = []

        def offer(edge_id: str) -> None:
            edge = self.edges[edge_id]
            if not edge.active or edge.source not in self.parents or edge.target in self.parents:
                return
            key = self._candidate_key(edge)
            current = local_keys.get(edge.target)
            if current is None or key < current:
                local_keys[edge.target] = key
                heapq.heappush(heap, (key, edge_id))

        for edge in self.edges.values():
            offer(edge.id)

        while heap:
            key, edge_id = heapq.heappop(heap)
            edge = self.edges[edge_id]
            if edge.target in self.parents:
                continue
            if edge.source not in self.parents:
                continue
            if local_keys.get(edge.target) != key or self._candidate_key(edge) != key:
                continue
            self.parents[edge.target] = (edge.source, edge.id, edge.witness)
            self.path_keys[edge.target] = key
            for next_edge_id in self.outgoing[edge.target]:
                offer(next_edge_id)
