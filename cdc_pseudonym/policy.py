"""Policy model: field strategies, table schemas, versioned policy sets."""

from __future__ import annotations

from dataclasses import dataclass

from .errors import PolicyError

STRATEGIES = ("pseudonym", "redact", "plain", "invisible")


@dataclass(frozen=True)
class FieldPolicy:
    strategy: str
    # Fields sharing a domain (e.g. a PK and its FKs) get identical
    # pseudonyms for identical values, keeping joins stable.
    domain: str | None = None

    def __post_init__(self) -> None:
        if self.strategy not in STRATEGIES:
            raise ValueError(f"unknown strategy: {self.strategy}")

    def scope(self, table: str, field_name: str) -> str:
        return self.domain or f"{table}.{field_name}"


@dataclass(frozen=True)
class TableSchema:
    sensitive_fields: frozenset


@dataclass(frozen=True)
class PolicySet:
    """An immutable, versioned set of field policies and table schemas."""

    version: int
    fields: dict  # table -> {field -> FieldPolicy}
    schemas: dict  # table -> TableSchema
    strict: bool = True

    def field_policy(self, table: str, field_name: str):
        return self.fields.get(table, {}).get(field_name)

    def validate_event(self, table: str, present_fields) -> None:
        """Strict mode rejects any present field without an explicit policy.

        Non-strict mode still rejects sensitive fields without a policy.
        Raises PolicyError carrying only the field name, never a value.
        """
        table_fields = self.fields.get(table, {})
        schema = self.schemas.get(table)
        for name in sorted(present_fields):
            if name in table_fields:
                continue
            sensitive = schema is not None and name in schema.sensitive_fields
            if self.strict or sensitive:
                raise PolicyError(
                    f"no policy for field '{name}' on table '{table}'",
                    table=table,
                    field=name,
                )


class PolicyHistory:
    """Ordered policy snapshots; resolves the policy active at a commit seq."""

    def __init__(self) -> None:
        self._entries: list = []  # list of (effective_seq, PolicySet)

    def append(self, effective_seq: int, policy_set: PolicySet) -> None:
        self._entries.append((effective_seq, policy_set))
        self._entries.sort(key=lambda e: e[0])

    def at(self, commit_seq: int):
        """Policy in effect at commit_seq (historical, not current)."""
        chosen = None
        for effective_seq, policy_set in self._entries:
            if effective_seq <= commit_seq:
                chosen = policy_set
            else:
                break
        return chosen

    @property
    def current(self):
        return self._entries[-1][1] if self._entries else None

    @property
    def versions(self):
        return [ps.version for _, ps in self._entries]

    def copy(self) -> "PolicyHistory":
        clone = PolicyHistory()
        clone._entries = list(self._entries)
        return clone
