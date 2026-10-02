"""Core model: versioned facts/rules/steps, changes, and dependency edges.

Everything carries a stable string id plus a monotonically increasing
integer version.  Dependency edges come in three recorded kinds:

* ``FieldDep``    -- an actual read of one field (or "*" for whole object)
* ``BranchDep``   -- a conditional-branch choice taken by a step
* ``NegativeDep`` -- a lookup that missed (adding the key later invalidates)

plus two structural kinds: ``RuleDep`` and ``StepDep``.
"""
from __future__ import annotations

import hashlib
import json
from dataclasses import dataclass
from typing import Any, Optional


def fingerprint(value: Any) -> str:
    """Stable content hash used as the *output version* of a step run."""
    blob = json.dumps(value, sort_keys=True, default=repr).encode("utf-8")
    return hashlib.sha256(blob).hexdigest()[:16]


# ---------------------------------------------------------------------------
# Changes
# ---------------------------------------------------------------------------

@dataclass(frozen=True)
class Change:
    """One atomic revision.  ``changed_fields is None`` means *everything*
    changed (object added or removed)."""

    kind: str                      # fact_upsert | fact_remove | rule_set | step_define
    target: str                    # fact id, rule id, or step id
    old_version: Optional[int]
    new_version: Optional[int]
    changed_fields: Optional[frozenset] = None

    def label(self) -> str:
        if self.kind == "fact_upsert":
            if self.old_version is None:
                return f"add {self.target}@v{self.new_version}"
            fields = ",".join(sorted(self.changed_fields or ()))
            return f"update {self.target} v{self.old_version}->v{self.new_version} [{fields}]"
        if self.kind == "fact_remove":
            return f"remove {self.target}@v{self.old_version}"
        if self.kind == "rule_set":
            return f"rule {self.target} v{self.old_version}->v{self.new_version}"
        return f"step {self.target} corrected v{self.old_version}->v{self.new_version}"


# ---------------------------------------------------------------------------
# Dependency edges
# ---------------------------------------------------------------------------

@dataclass(frozen=True)
class FieldDep:
    """Actual read of ``field`` on ``fact`` at ``version``.

    ``field == "*"`` means the whole object was consumed.  ``version == 0``
    means the fact did not exist when read (a default was returned)."""

    fact: str
    version: int
    field: str

    def label(self) -> str:
        return f"{self.fact}@v{self.version}.{self.field}"


@dataclass(frozen=True)
class BranchDep:
    """A conditional branch choice; recorded for audit/explanation.  Reads
    that happen only on the *not taken* branch are never recorded at all,
    which is what gives the engine branch-level precision."""

    branch: str
    choice: Any

    def label(self) -> str:
        return f"branch {self.branch}={self.choice}"


@dataclass(frozen=True)
class NegativeDep:
    """A lookup that missed.  Adding the key later invalidates the reader."""

    collection: str                # "fact" | "rule"
    key: str

    def label(self) -> str:
        return f"missing {self.collection}:{self.key}"


@dataclass(frozen=True)
class RuleDep:
    rule: str
    version: int

    def label(self) -> str:
        return f"rule {self.rule}@v{self.version}"


@dataclass(frozen=True)
class StepDep:
    """Edge to another step's output, pinned to that output's fingerprint."""

    step: str
    fingerprint: str

    def label(self) -> str:
        return f"step {self.step}@{self.fingerprint}"


# ---------------------------------------------------------------------------
# Stored state
# ---------------------------------------------------------------------------

@dataclass
class FactState:
    version: int
    fields: dict


@dataclass
class RuleState:
    version: int
    value: Any


@dataclass
class StepDef:
    version: int
    func: Any


@dataclass
class Run:
    """One recorded execution of a step: its deps and its output version."""

    step: str
    step_version: int
    deps: frozenset
    value: Any
    fingerprint: str
