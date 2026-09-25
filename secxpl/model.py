"""Core entities.

Every fact, rule and derivation step carries a stable ID and an explicit
version so that evidence is replayable.  All sample data in this
repository is synthetic.
"""

from __future__ import annotations

from dataclasses import dataclass

STEP_SCHEMA_VERSION = 1


@dataclass(frozen=True)
class Fact:
    fact_id: str
    version: int
    level: int  # sensitivity; 0 means public, higher means more restricted
    payload: str


@dataclass(frozen=True)
class Rule:
    rule_id: str
    version: int
    premises: tuple[str, ...]
    conclusion: str


@dataclass(frozen=True)
class Step:
    step_id: str
    version: int
    rule_id: str
    rule_version: int
    premises: tuple[str, ...]
    conclusion: str


@dataclass(frozen=True)
class World:
    facts: tuple[Fact, ...]
    rules: tuple[Rule, ...]
