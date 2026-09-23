"""Mapping store with atomic migration between pseudonym versions."""

from __future__ import annotations

from .errors import MigrationError


class MappingStore:
    """Holds (scope, value) -> pseudonym entries for one version.

    Raw values live only in memory and are never written to logs.
    """

    def __init__(self, version: int) -> None:
        self.version = version
        self._entries: dict = {}

    def get_or_create(self, pseudonymizer, scope: str, value) -> str:
        key = (scope, _hashable(value))
        existing = self._entries.get(key)
        if existing is not None:
            return existing
        pseudonym = pseudonymizer.pseudonym(scope, value)
        self._entries[key] = pseudonym
        return pseudonym

    def migrate(self, new_pseudonymizer) -> "MappingStore":
        """Build a fully migrated store; on any failure raise MigrationError
        and leave this store untouched (atomic all-or-nothing swap)."""
        new_entries: dict = {}
        try:
            for scope, value in self._entries:
                new_entries[(scope, value)] = new_pseudonymizer.pseudonym(scope, value)
        except Exception as exc:  # noqa: BLE001 - wrapped, never logged raw
            raise MigrationError(
                f"mapping migration to version {new_pseudonymizer.version} failed"
            ) from exc
        migrated = MappingStore(new_pseudonymizer.version)
        migrated._entries = new_entries
        return migrated

    def __len__(self) -> int:
        return len(self._entries)


def _hashable(value):
    if isinstance(value, (str, int, float, bool, type(None))):
        return value
    if isinstance(value, (list, tuple)):
        return tuple(_hashable(v) for v in value)
    if isinstance(value, dict):
        return tuple(sorted((k, _hashable(v)) for k, v in value.items()))
    return repr(value)
