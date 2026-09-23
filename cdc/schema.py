"""Schema registry: versions take effect at a commit sequence number."""

from __future__ import annotations

from dataclasses import dataclass, field


class UnknownSchemaError(Exception):
    """No schema version is effective for (table, commit_seq) yet."""

    def __init__(self, table: str, commit_seq: int):
        self.table = table
        self.commit_seq = commit_seq
        super().__init__(
            f"no schema version for table {table!r} effective at commit_seq={commit_seq}"
        )


@dataclass(frozen=True)
class FieldDef:
    """A field definition. field_id is stable across renames."""

    field_id: int
    name: str
    type: str = "any"


@dataclass(frozen=True)
class SchemaVersion:
    """One version of a table schema, effective from commit_seq >= effective_from."""

    table: str
    version: int
    effective_from: int
    fields: tuple[FieldDef, ...] = field(default_factory=tuple)

    def by_name(self) -> dict[str, FieldDef]:
        return {f.name: f for f in self.fields}

    def by_id(self) -> dict[int, FieldDef]:
        return {f.field_id: f for f in self.fields}


class SchemaRegistry:
    """Holds schema versions per table, ordered by effective_from."""

    def __init__(self) -> None:
        self._versions: dict[str, list[SchemaVersion]] = {}

    def register(self, version: SchemaVersion) -> None:
        versions = self._versions.setdefault(version.table, [])
        if any(v.version == version.version for v in versions):
            raise ValueError(
                f"duplicate schema version {version.version} for table {version.table!r}"
            )
        if any(v.effective_from == version.effective_from for v in versions):
            raise ValueError(
                f"conflicting effective_from={version.effective_from} "
                f"for table {version.table!r}"
            )
        versions.append(version)
        versions.sort(key=lambda v: v.effective_from)

    def resolve(self, table: str, commit_seq: int) -> SchemaVersion:
        """Return the newest version whose effective_from <= commit_seq."""
        candidates = [
            v
            for v in self._versions.get(table, [])
            if v.effective_from <= commit_seq
        ]
        if not candidates:
            raise UnknownSchemaError(table, commit_seq)
        return candidates[-1]

    def deleted_field_ids(self, version: SchemaVersion) -> frozenset[int]:
        """Stable ids present in the previous version but dropped in this one."""
        versions = self._versions.get(version.table, [])
        earlier = [v for v in versions if v.effective_from < version.effective_from]
        if not earlier:
            return frozenset()
        previous_ids = set(earlier[-1].by_id())
        return frozenset(previous_ids - set(version.by_id()))

    def versions(self, table: str) -> tuple[SchemaVersion, ...]:
        return tuple(self._versions.get(table, ()))
