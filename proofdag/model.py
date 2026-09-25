"""Data model for equivalence proof DAGs.

Facts, rules and steps all carry a stable identity (``stable_id``) plus a
``version``.  Internal DAG node ids are arbitrary and may be renamed freely:
they never participate in canonical identity.
"""

from __future__ import annotations

from dataclasses import dataclass


@dataclass(frozen=True)
class Fact:
    """A leaf fact.  Identity includes the source: two facts carrying the
    same payload but coming from different sources are distinct nodes and
    are never merged."""

    stable_id: str
    version: int
    source_id: str
    payload: str

    @property
    def node_id(self) -> str:
        return f"fact:{self.stable_id}:{self.version}:{self.source_id}"

    def key(self) -> tuple:
        return ("F", self.stable_id, str(self.version), self.source_id, self.payload)


@dataclass(frozen=True)
class Rule:
    """An inference rule.  ``commutative=True`` means the premise list is an
    unordered (conjunctive) set; ``False`` means the premise order is
    semantically significant."""

    stable_id: str
    version: int
    name: str
    commutative: bool = False


@dataclass
class Step:
    """An internal proof node applying ``rule`` to ``premises``.

    ``node_id`` is an internal, renamable identifier.  ``stable_id`` and
    ``version`` are the durable identity recorded in exported evidence.
    """

    node_id: str
    stable_id: str
    version: int
    rule: Rule
    premises: tuple
    conclusion: Fact
