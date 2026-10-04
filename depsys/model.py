"""Core data model: facts, steps, rules and dependency edges.

Every fact, rule and step carries a stable string id plus an integer
version.  Dependency edges are recorded while a step executes and
distinguish three kinds of access:

* ``READ``     - the step actually read a field value.
* ``BRANCH``   - the field value was used to decide a conditional branch.
* ``NEGATIVE`` - the step probed for a fact that did not exist (a "miss").
"""
from __future__ import annotations

from dataclasses import dataclass, field
from enum import Enum
from typing import Any, Callable


class DepKind(str, Enum):
    READ = "read"
    BRANCH = "branch"
    NEGATIVE = "negative"


@dataclass
class Fact:
    fact_id: str
    version: int
    fields: dict[str, Any]
    producer: str | None = None  # step_id of producer, None for base inputs


@dataclass(frozen=True)
class DepEdge:
    """One recorded access of ``step_id`` to ``fact_id``.

    ``fact_version``/``field`` are ``None`` for NEGATIVE edges (the fact did
    not exist when probed).
    """

    step_id: str
    step_version: int
    fact_id: str
    fact_version: int | None
    field: str | None
    kind: DepKind


StepFn = Callable[["ReadContext"], dict[str, dict[str, Any]]]


@dataclass
class Step:
    step_id: str
    version: int
    rule_id: str
    rule_version: int
    fn: StepFn
    outputs: tuple[str, ...] = ()


# ---------------------------------------------------------------------------
# Batch change descriptions
# ---------------------------------------------------------------------------


@dataclass
class FactUpdate:
    """An existing base fact is revised; ``changed_fields`` names the fields
    whose values actually differ."""

    fact_id: str
    new_fields: dict[str, Any]
    changed_fields: frozenset[str]


@dataclass
class FactAdd:
    """A previously non-existent fact appears (triggers NEGATIVE deps)."""

    fact_id: str
    fields: dict[str, Any]


@dataclass
class RuleUpdate:
    rule_id: str
    new_version: int


@dataclass
class StepCorrection:
    step_id: str
    new_fn: StepFn
    new_version: int
    new_rule_version: int | None = None
