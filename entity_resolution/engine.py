"""Evidence-driven entity resolution engine.

Design guarantees:

* Entities merge only through explicit, active EQUIV facts. Names or any
  other attributes never trigger a merge.
* Retracting an equiv fact recomputes components from scratch, so splits
  are a natural consequence of replaying the remaining active evidence.
* Any state where a NEQUIV edge's endpoints are joined by an equiv path
  is rejected with a :class:`ConflictError` carrying the evidence ring.
* The representative ID of a component is deterministic: the smallest
  entity key (rule-representative-min-key).
* Every mutation is recorded as a versioned Step; ``Engine.replay``
  rebuilds identical state from the step log alone.
"""
from __future__ import annotations

from collections import deque

from .model import EvidenceRing, Fact, FactKind, Step, StepAction
from .rules import (
    RULE_CONFLICT_RING,
    RULE_EQUIV_TRANSITIVE,
    RULE_NEQUIV_SYMMETRIC,
)

STEP_VERSION = 1


class ConflictError(Exception):
    """Raised when asserting a fact would create an evidence ring."""

    def __init__(self, ring: EvidenceRing):
        self.ring = ring
        super().__init__(
            "conflict: evidence ring formed by facts " + ", ".join(ring.fact_ids)
        )


class UnknownFactError(KeyError):
    pass


class DuplicateFactError(ValueError):
    pass


class FactNotActiveError(ValueError):
    pass


class Engine:
    SCHEMA_VERSION = 1

    def __init__(self) -> None:
        self._facts: dict[str, Fact] = {}
        self._retracted: set[str] = set()
        self._steps: list[Step] = []
        self._names: dict[str, str] = {}

    # ------------------------------------------------------------------
    # metadata (attributes only; never used for identity decisions)
    # ------------------------------------------------------------------
    def register_entity(self, key: str, name: str) -> Step | None:
        if not key:
            raise ValueError("entity key must be non-empty")
        if key in self._names:
            return None
        self._names[key] = name
        return self._record_step(
            StepAction.REGISTER_ENTITY, entity_key=key, entity_name=name
        )

    def name_of(self, key: str) -> str | None:
        return self._names.get(key)

    # ------------------------------------------------------------------
    # evidence mutations
    # ------------------------------------------------------------------
    def assert_fact(self, fact: Fact, rule_ids: tuple[str, ...] | None = None) -> Step:
        if fact.fact_id in self._facts:
            raise DuplicateFactError(f"fact {fact.fact_id!r} already asserted")
        self._facts[fact.fact_id] = fact
        ring = self._find_conflict()
        if ring is not None:
            del self._facts[fact.fact_id]
            raise ConflictError(ring)
        if rule_ids is None:
            rule_ids = self._default_rules_for(fact)
        return self._record_step(StepAction.ASSERT_FACT, fact=fact, rule_ids=rule_ids)

    def retract_fact(self, fact_id: str, rule_ids: tuple[str, ...] = ()) -> Step:
        if fact_id not in self._facts:
            raise UnknownFactError(fact_id)
        if fact_id in self._retracted:
            raise FactNotActiveError(f"fact {fact_id!r} is already retracted")
        self._retracted.add(fact_id)
        return self._record_step(
            StepAction.RETRACT_FACT, retract_fact_id=fact_id, rule_ids=rule_ids
        )

    # ------------------------------------------------------------------
    # queries
    # ------------------------------------------------------------------
    @property
    def steps(self) -> tuple[Step, ...]:
        return tuple(self._steps)

    def active_facts(self) -> dict[str, Fact]:
        return {
            fid: f for fid, f in self._facts.items() if fid not in self._retracted
        }

    def is_active(self, fact_id: str) -> bool:
        return fact_id in self._facts and fact_id not in self._retracted

    def components(self) -> dict[str, frozenset[str]]:
        """Map representative ID -> member keys, recomputed deterministically."""
        parent = {key: key for key in self._entity_keys()}

        def find(key: str) -> str:
            while parent[key] != key:
                parent[key] = parent[parent[key]]
                key = parent[key]
            return key

        for fact in sorted(self._active_equiv_facts(), key=lambda f: f.fact_id):
            root_left, root_right = find(fact.left), find(fact.right)
            if root_left == root_right:
                continue
            if root_right < root_left:
                root_left, root_right = root_right, root_left
            parent[root_right] = root_left

        grouped: dict[str, set[str]] = {}
        for key in parent:
            grouped.setdefault(find(key), set()).add(key)
        return {root: frozenset(members) for root, members in sorted(grouped.items())}

    def representative(self, key: str) -> str:
        for root, members in self.components().items():
            if key in members:
                return root
        raise KeyError(key)

    def component_of(self, key: str) -> frozenset[str]:
        return self.components()[self.representative(key)]

    def find_equiv_path(self, start: str, goal: str) -> tuple[str, ...] | None:
        """Deterministic BFS over active equiv edges; returns fact IDs."""
        if start == goal:
            return ()
        adjacency: dict[str, list[tuple[str, str]]] = {}
        for fact in self._active_equiv_facts():
            adjacency.setdefault(fact.left, []).append((fact.right, fact.fact_id))
            adjacency.setdefault(fact.right, []).append((fact.left, fact.fact_id))
        for edges in adjacency.values():
            edges.sort()

        previous: dict[str, tuple[str, str] | None] = {start: None}
        queue: deque[str] = deque([start])
        while queue:
            current = queue.popleft()
            for neighbor, fact_id in adjacency.get(current, ()):
                if neighbor in previous:
                    continue
                previous[neighbor] = (current, fact_id)
                if neighbor == goal:
                    path: list[str] = []
                    node = goal
                    while node != start:
                        parent, edge_id = previous[node]  # type: ignore[misc]
                        path.append(edge_id)
                        node = parent
                    return tuple(reversed(path))
                queue.append(neighbor)
        return None

    # ------------------------------------------------------------------
    # replay
    # ------------------------------------------------------------------
    @classmethod
    def replay(cls, steps: tuple[Step, ...] | list[Step]) -> "Engine":
        """Rebuild an engine purely from a step log (evidence is replayable)."""
        engine = cls()
        for step in steps:
            if step.action is StepAction.REGISTER_ENTITY:
                if step.entity_key is None or step.entity_name is None:
                    raise ValueError(f"{step.step_id}: register step without entity")
                engine.register_entity(step.entity_key, step.entity_name)
            elif step.action is StepAction.ASSERT_FACT:
                if step.fact is None:
                    raise ValueError(f"{step.step_id}: assert step without fact")
                engine.assert_fact(step.fact, rule_ids=step.rule_ids)
            elif step.action is StepAction.RETRACT_FACT:
                if step.retract_fact_id is None:
                    raise ValueError(f"{step.step_id}: retract step without fact id")
                engine.retract_fact(step.retract_fact_id, rule_ids=step.rule_ids)
            else:  # pragma: no cover - defensive
                raise ValueError(f"unknown step action {step.action!r}")
        return engine

    def replayed(self) -> "Engine":
        return Engine.replay(self._steps)

    # ------------------------------------------------------------------
    # internals
    # ------------------------------------------------------------------
    def _record_step(
        self,
        action: StepAction,
        fact: Fact | None = None,
        retract_fact_id: str | None = None,
        entity_key: str | None = None,
        entity_name: str | None = None,
        rule_ids: tuple[str, ...] = (),
    ) -> Step:
        seq = len(self._steps) + 1
        step = Step(
            step_id=f"step-{seq:04d}",
            version=STEP_VERSION,
            seq=seq,
            action=action,
            fact=fact,
            retract_fact_id=retract_fact_id,
            entity_key=entity_key,
            entity_name=entity_name,
            rule_ids=tuple(rule_ids),
        )
        self._steps.append(step)
        return step

    def _entity_keys(self) -> set[str]:
        keys = set(self._names)
        for fact in self._facts.values():
            keys.add(fact.left)
            keys.add(fact.right)
        return keys

    def _active_equiv_facts(self) -> list[Fact]:
        return [
            fact
            for fact in self._facts.values()
            if fact.kind is FactKind.EQUIV and fact.fact_id not in self._retracted
        ]

    def _find_conflict(self) -> EvidenceRing | None:
        nequiv_facts = sorted(
            (
                fact
                for fact in self._facts.values()
                if fact.kind is FactKind.NEQUIV and fact.fact_id not in self._retracted
            ),
            key=lambda f: f.fact_id,
        )
        for fact in nequiv_facts:
            path = self.find_equiv_path(fact.left, fact.right)
            if path is not None:
                return EvidenceRing(
                    equiv_path_fact_ids=path, nequiv_fact_id=fact.fact_id
                )
        return None

    @staticmethod
    def _default_rules_for(fact: Fact) -> tuple[str, ...]:
        if fact.kind is FactKind.EQUIV:
            return (RULE_EQUIV_TRANSITIVE.rule_id, RULE_CONFLICT_RING.rule_id)
        return (RULE_NEQUIV_SYMMETRIC.rule_id, RULE_CONFLICT_RING.rule_id)
