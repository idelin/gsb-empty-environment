"""版本化行存储：当前值 + 每键版本历史，用于区分迟到旧版本与缺事件。"""

from __future__ import annotations

import copy
from typing import Any, Mapping

from .models import MISSING, Key


class VersionedStore:
    def __init__(self) -> None:
        self._rows: dict[Key, dict[str, Any]] = {}
        self._versions: dict[Key, int] = {}
        self._history: dict[Key, list[tuple[int, dict[str, Any]]]] = {}

    def get(self, key: Key) -> dict[str, Any] | None:
        row = self._rows.get(key)
        return copy.deepcopy(row) if row is not None else None

    def version(self, key: Key) -> int:
        return self._versions.get(key, 0)

    def field_value(self, key: Key, name: str) -> Any:
        row = self._rows.get(key)
        if row is None or name not in row:
            return MISSING
        return row[name]

    def matches(self, key: Key, before: Mapping[str, Any]) -> bool:
        """before 的所有提供字段是否与当前行一致（缺失字段不等于 null）。"""
        row = self._rows.get(key)
        for name, expected in before.items():
            if row is None or name not in row or row[name] != expected:
                return False
        return True

    def matches_history(self, key: Key, before: Mapping[str, Any]) -> int | None:
        """before 匹配哪个历史版本；都不匹配返回 None。用于识别迟到旧版本。"""
        for version, snapshot in self._history.get(key, []):
            if all(snapshot.get(name, MISSING) == expected for name, expected in before.items()):
                return version
        return None

    def apply(self, key: Key, after: Mapping[str, Any]) -> None:
        """应用部分后值：仅覆盖提供字段，未提供字段保留当前值。"""
        row = self._rows.setdefault(key, {})
        row.update(copy.deepcopy(dict(after)))
        version = self._versions.get(key, 0) + 1
        self._versions[key] = version
        self._history.setdefault(key, []).append((version, copy.deepcopy(row)))

    def snapshot(self) -> dict[Key, dict[str, Any]]:
        return copy.deepcopy(self._rows)
