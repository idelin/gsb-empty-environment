"""In-memory table store. Changes are applied atomically per transaction."""

from __future__ import annotations

from typing import Any, Optional


class TableStore:
    """Tables mapping primary keys to row dicts."""

    def __init__(self) -> None:
        self._tables: dict[str, dict[str, dict]] = {}

    def get(self, table: str, primary_key: str) -> Optional[dict]:
        row = self._tables.get(table, {}).get(primary_key)
        return dict(row) if row is not None else None

    def tables(self) -> dict[str, dict[str, dict]]:
        return {
            table: {pk: dict(row) for pk, row in rows.items()}
            for table, rows in self._tables.items()
        }

    def apply_all(self, changes) -> None:
        """Apply every change or none. Validates before mutating anything."""
        staged: dict[str, dict[str, dict]] = {
            table: dict(rows) for table, rows in self._tables.items()
        }
        for change in changes:
            rows = staged.setdefault(change.table, {})
            current = rows.get(change.primary_key)
            if change.before is not None and current != change.before:
                raise ValueError(
                    f"optimistic check failed for {change.table}/"
                    f"{change.primary_key}: expected {change.before!r}, "
                    f"found {current!r}"
                )
            if change.after is None:
                rows.pop(change.primary_key, None)
            else:
                rows[change.primary_key] = dict(change.after)
        self._tables = staged
