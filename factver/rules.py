"""Rules and read-tracking fact views."""
from __future__ import annotations

from dataclasses import dataclass
from typing import Any, Callable, Dict, Tuple

from .model import DATA, REMARK, WILDCARD, FactSnapshot


@dataclass(frozen=True, order=True)
class FieldRead:
    """One recorded read: which fact version, what kind, which field."""

    fact_id: str
    version: int
    kind: str    # DATA or REMARK
    field: str   # field name, remark key, or WILDCARD


class FactView:
    """Read-tracking view of one fact snapshot, handed to rule code."""

    def __init__(self, snapshot: FactSnapshot, record: Callable[[FieldRead], None]):
        self._snap = snapshot
        self._record = record

    @property
    def fact_id(self) -> str:
        return self._snap.fact_id

    @property
    def version(self) -> int:
        return self._snap.version

    def _touch(self, kind: str, field: str) -> None:
        self._record(FieldRead(self._snap.fact_id, self._snap.version, kind, field))

    def has(self, field: str) -> bool:
        """Existence check: depends on the field, so deletion invalidates it."""
        self._touch(DATA, field)
        return field in self._snap.fields

    def get(self, field: str, default: Any = None) -> Any:
        self._touch(DATA, field)
        fv = self._snap.fields.get(field)
        return fv.value if fv is not None else default

    def require(self, field: str) -> Any:
        self._touch(DATA, field)
        try:
            return self._snap.fields[field].value
        except KeyError:
            raise KeyError(
                f"fact {self.fact_id!r} v{self.version} has no field {field!r}"
            ) from None

    def source_of(self, field: str) -> str:
        self._touch(DATA, field)
        return self._snap.fields[field].source

    def all(self) -> Dict[str, Any]:
        """Wildcard read: treated as depending on ALL data fields."""
        self._touch(DATA, WILDCARD)
        return {name: fv.value for name, fv in self._snap.fields.items()}

    def remark(self, key: str, default: Any = None) -> Any:
        self._touch(REMARK, key)
        return self._snap.remarks.get(key, default)


@dataclass(frozen=True)
class Rule:
    rule_id: str
    version: int
    func: Callable[[Dict[str, FactView]], Any]


class RuleRegistry:
    """Keeps every registered version of every rule (for replay)."""

    def __init__(self) -> None:
        self._rules: Dict[Tuple[str, int], Rule] = {}

    def register(self, rule_id: str, version: int, func) -> Rule:
        key = (rule_id, version)
        if key in self._rules:
            raise ValueError(f"rule {rule_id!r} v{version} already registered")
        rule = Rule(rule_id, version, func)
        self._rules[key] = rule
        return rule

    def get(self, rule_id: str, version: int = None) -> Rule:
        if version is None:
            candidates = [r for (rid, _), r in self._rules.items() if rid == rule_id]
            if not candidates:
                raise KeyError(f"unknown rule {rule_id!r}")
            return max(candidates, key=lambda r: r.version)
        try:
            return self._rules[(rule_id, version)]
        except KeyError:
            raise KeyError(f"unknown rule {rule_id!r} v{version}") from None
