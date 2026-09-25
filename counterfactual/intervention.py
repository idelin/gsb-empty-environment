"""Atomic, isolated interventions over an Engine's baseline facts.

Guarantees:
- Contradictory operation sets are rejected *before* anything is applied.
- Application is atomic: validation runs on the whole op set first, then a
  brand-new fact tuple is built; the baseline is never mutated.
- Replacing a fact never keeps the old value: the replacement carries the
  same fact ID with a bumped version, and the old value is gone.
- Each session derives from its own intervened fact set, so concurrent
  interventions are isolated from each other.
"""
from __future__ import annotations

from dataclasses import dataclass

from .core import Derivation, Engine, Fact


class InterventionError(Exception):
    pass


@dataclass(frozen=True)
class RemoveFact:
    fact_id: str


@dataclass(frozen=True)
class ReplaceFact:
    fact_id: str
    new_value: str


class Intervention:
    """A validated, immutable set of operations applied as one atomic unit."""

    def __init__(self, ops: tuple[object, ...]):
        self.ops = tuple(ops)
        self._check_contradictions()

    def _check_contradictions(self) -> None:
        removed: set[str] = set()
        replaced: dict[str, str] = {}
        for op in self.ops:
            if isinstance(op, RemoveFact):
                if op.fact_id in removed:
                    raise InterventionError(
                        f"contradictory intervention: {op.fact_id} removed twice"
                    )
                if op.fact_id in replaced:
                    raise InterventionError(
                        f"contradictory intervention: {op.fact_id} both "
                        "replaced and removed"
                    )
                removed.add(op.fact_id)
            elif isinstance(op, ReplaceFact):
                if op.fact_id in removed:
                    raise InterventionError(
                        f"contradictory intervention: {op.fact_id} both "
                        "removed and replaced"
                    )
                if op.fact_id in replaced:
                    raise InterventionError(
                        f"contradictory intervention: {op.fact_id} replaced "
                        "with two different values"
                    )
                replaced[op.fact_id] = op.new_value
            else:
                raise InterventionError(f"unknown operation: {op!r}")

    def apply(self, facts: tuple[Fact, ...]) -> tuple[Fact, ...]:
        """Validate against the actual fact set, then build a new tuple.
        Nothing is mutated; on any error the input is returned untouched."""
        by_id = {f.fact_id: f for f in facts}
        # Phase 1: validate everything (atomic pre-check).
        for op in self.ops:
            target = by_id.get(op.fact_id)
            if target is None:
                raise InterventionError(f"unknown fact id: {op.fact_id}")
            if isinstance(op, ReplaceFact) and op.new_value == target.value:
                raise InterventionError(
                    f"replacement of {op.fact_id} keeps the old value "
                    f"{op.new_value!r}; refusing no-op replace"
                )
        # Phase 2: build the intervened fact set.
        result: list[Fact] = []
        for fact in facts:
            op = next((o for o in self.ops if o.fact_id == fact.fact_id), None)
            if op is None:
                result.append(fact)
            elif isinstance(op, ReplaceFact):
                result.append(fact.replaced(op.new_value))  # old value dropped
            # RemoveFact: fact is simply omitted.
        return tuple(result)


class InterventionSession:
    """Context manager scoping one isolated intervention.

    On exit it verifies the engine's baseline summary is unchanged.
    """

    def __init__(self, engine: Engine, intervention: Intervention):
        self._engine = engine
        self._intervention = intervention
        self._summary_before = engine.baseline_summary()
        self.derivation: Derivation | None = None

    def __enter__(self) -> "InterventionSession":
        intervened = self._intervention.apply(self._engine.facts)
        self.derivation = self._engine.derive_with(intervened)
        return self

    def __exit__(self, *exc_info: object) -> bool:
        if self._engine.baseline_summary() != self._summary_before:
            raise InterventionError(
                "baseline summary changed during intervention; isolation broken"
            )
        return False
