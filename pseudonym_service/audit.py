"""Audit log that records pseudonyms and event metadata only.

Raw subjects and key material must never be written here. The service only
ever passes pseudonyms; scan_forbidden() lets tests and demos verify that
invariant over the whole log.
"""
from __future__ import annotations

import itertools
from dataclasses import dataclass, field


@dataclass(frozen=True)
class AuditEntry:
    seq: int
    event: str
    pseudonym: str | None
    detail: tuple[tuple[str, str], ...] = field(default_factory=tuple)

    def render(self) -> str:
        detail = ", ".join(f"{k}={v}" for k, v in self.detail)
        return f"#{self.seq} {self.event} pseudonym={self.pseudonym} {detail}".strip()


class AuditLog:
    def __init__(self):
        self._counter = itertools.count(1)
        self._entries: list[AuditEntry] = []

    def record(self, event: str, pseudonym: str | None, **detail: object) -> AuditEntry:
        entry = AuditEntry(
            seq=next(self._counter),
            event=event,
            pseudonym=pseudonym,
            detail=tuple(sorted((k, str(v)) for k, v in detail.items())),
        )
        self._entries.append(entry)
        return entry

    @property
    def entries(self) -> tuple[AuditEntry, ...]:
        return tuple(self._entries)

    def scan_forbidden(self, forbidden: list[str]) -> list[AuditEntry]:
        """Return any entries that contain a forbidden string (key or raw subject)."""
        hits = []
        for entry in self._entries:
            rendered = entry.render()
            if any(token and token in rendered for token in forbidden):
                hits.append(entry)
        return hits
