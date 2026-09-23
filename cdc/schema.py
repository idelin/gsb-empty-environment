"""Schema registry: versions take effect at a commit sequence number."""

from __future__ import annotations

from dataclasses import dataclass


@dataclass(frozen=True)
class FieldDef:
    """A field definition. ``stable_id`` survives renames across versions."""

    stable_id: int
    name: str
    type: str


@dataclass(frozen=True)
class SchemaVersion:
    """One version of a table schema, effective from ``effective_from``."""

    table: str
    version: int
    effective_from: int
    fields: tuple[FieldDef, ...]

    def __post_init__(self) -> None:
        names = [f.name for f in self.fields]
        if len(names) != len(set(names)):
            raise ValueError(
                f"duplicate field name in {self.table} v{self.version}"
            )
        ids = [f.stable_id for f in self.fields]
        if len(ids) != len(set(ids)):
            raise ValueError(
                f"duplicate stable id in {self.table} v{self.version}"
            )

    @property
    def by_name(self) -> dict[str, FieldDef]:
        return {f.name: f for f in self.fields}


class SchemaRegistry:
    """Holds schema versions per table, ordered by effective commit seq."""

    def __init__(self) -> None:
        self._versions: dict[str, list[SchemaVersion]] = {}

    def register(self, version: SchemaVersion) -> None:
        versions = self._versions.setdefault(version.table, [])
        for existing in versions:
            if existing.version == version.version:
                raise ValueError(
                    f"{version.table} v{version.version} already registered"
                )
            if existing.effective_from == version.effective_from:
                raise ValueError(
                    f"{version.table} already has a version effective from "
                    f"commit_seq={version.effective_from}"
                )
        versions.append(version)
        versions.sort(key=lambda v: v.effective_from)

    def resolve(self, table: str, commit_seq: int) -> SchemaVersion | None:
        """Latest registered version with effective_from <= commit_seq.

        Returns None when no registered version covers the commit seq;
        callers must not guess in that case.
        """
        best: SchemaVersion | None = None
        for version in self._versions.get(table, ()):
            if version.effective_from <= commit_seq:
                best = version
            else:
                break
        return best
