"""Core data model.

Every artifact - fact, rule, derivation step - carries a stable
identifier and a version so that evidence can be replayed and audited.
All sample data shipped with this package is synthetic.
"""
from __future__ import annotations

from dataclasses import dataclass

PUBLIC = 0
INTERNAL = 1
SECRET = 2

LEVEL_NAMES = {PUBLIC: "public", INTERNAL: "internal", SECRET: "secret"}


@dataclass(frozen=True)
class Fact:
    fact_id: str
    version: int
    key: str
    value: str
    level: int


@dataclass(frozen=True)
class Rule:
    rule_id: str
    version: int
    premises: tuple[str, ...]  # fact keys required to fire
    conclusion_key: str
    conclusion_level: int
    level: int


@dataclass(frozen=True)
class Step:
    step_id: str
    version: int
    rule_id: str
    rule_version: int
    inputs: tuple[str, ...]  # fact ids
    output: str  # fact id
