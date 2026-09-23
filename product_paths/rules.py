"""Versioned forbidden-state rules with left-closed right-open time intervals.

A rule forbids entering a product state (vertex, state) while it is active,
i.e. at moments t with ``valid_from <= t < valid_to``. Every mutation of the
rule set bumps the version and keeps an immutable snapshot, so queries can be
replayed against an older version and cursors can be bound to a version.
"""

from __future__ import annotations

from dataclasses import dataclass


@dataclass(frozen=True)
class ForbiddenRule:
    rule_id: str
    vertex: str
    state: str
    valid_from: float = 0.0
    valid_to: float | None = None  # None means "+infinity"

    def active_at(self, moment: float) -> bool:
        """Left-closed right-open membership: [valid_from, valid_to)."""
        if moment < self.valid_from:
            return False
        if self.valid_to is not None and moment >= self.valid_to:
            return False
        return True


class RuleSet:
    """Append-only history of forbidden-rule snapshots."""

    def __init__(self) -> None:
        self._rules: dict[str, ForbiddenRule] = {}
        self._version = 0
        self._snapshots: dict[int, dict[str, ForbiddenRule]] = {0: {}}

    @property
    def version(self) -> int:
        return self._version

    def _commit(self) -> int:
        self._version += 1
        self._snapshots[self._version] = dict(self._rules)
        return self._version

    def add_rule(self, rule: ForbiddenRule) -> int:
        if rule.rule_id in self._rules:
            raise ValueError(f"duplicate rule id {rule.rule_id!r}")
        self._rules[rule.rule_id] = rule
        return self._commit()

    def remove_rule(self, rule_id: str) -> int:
        if rule_id not in self._rules:
            raise KeyError(f"unknown rule id {rule_id!r}")
        del self._rules[rule_id]
        return self._commit()

    def rules(self, version: int | None = None) -> tuple[ForbiddenRule, ...]:
        version = self._version if version is None else version
        try:
            snapshot = self._snapshots[version]
        except KeyError:
            raise ValueError(f"unknown ruleset version {version}") from None
        return tuple(snapshot.values())

    def is_forbidden(
        self,
        vertex: str,
        state: str,
        *,
        at: float = 0.0,
        version: int | None = None,
    ) -> bool:
        return any(
            rule.vertex == vertex and rule.state == state and rule.active_at(at)
            for rule in self.rules(version)
        )
