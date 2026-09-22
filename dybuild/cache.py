"""Content-addressed cache records."""
from __future__ import annotations

from dataclasses import dataclass

from .taskapi import Observation
from .workspace import Workspace


@dataclass(frozen=True)
class CacheEntry:
    node: str
    key: str
    observations: tuple[Observation, ...]
    outputs: tuple[tuple[str, str], ...]  # (identity, digest)
    generation: int

    def valid_against(self, workspace: Workspace) -> bool:
        """True only if every recorded version matches the live snapshot."""
        for obs in self.observations:
            rule = obs.version[1] if obs.kind == "dir" else "lex"
            current = workspace.token(obs.identity, rule)
            if current != tuple(obs.version):
                return False
        for identity, digest in self.outputs:
            if not workspace.exists(identity):
                return False
            if workspace.file_digest(identity) != digest:
                return False
        return True


class CacheStore:
    def __init__(self) -> None:
        self._entries: dict[str, CacheEntry] = {}  # node -> newest entry

    def put(self, entry: CacheEntry) -> None:
        self._entries[entry.node] = entry

    def get(self, node: str, key: str, workspace: Workspace) -> CacheEntry | None:
        entry = self._entries.get(node)
        if entry is None or entry.key != key:
            return None
        if not entry.valid_against(workspace):
            return None
        return entry

    def lookup_any(self, node: str, workspace: Workspace) -> CacheEntry | None:
        """Validity check without a key (used after input-only invalidation)."""
        entry = self._entries.get(node)
        if entry is None:
            return None
        return entry if entry.valid_against(workspace) else None

    def clear(self, node: str) -> None:
        self._entries.pop(node, None)
