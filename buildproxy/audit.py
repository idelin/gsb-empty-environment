"""Audit events.

Every public operation emits exactly one :class:`AuditEvent`, whether it
succeeded, was denied, or failed.  ``dev``/``ino`` are taken from ``fstat()``
of the *actually opened* file descriptor (never from a check-time string
path), so the event is evidence about the real object that was touched.
For removal-style operations (``unlink``, ``rename``) the victim object
cannot be pinned across the syscall; those events leave ``dev``/``ino`` as
``None`` and instead carry the identity chain of the containing directories
(``chain``), which *is* pinned by open file descriptors.
"""

from __future__ import annotations

import threading
from dataclasses import dataclass, field


@dataclass(frozen=True)
class AuditEvent:
    op: str                       # read | create | replace | rename | unlink | list
    path: str                     # caller-supplied path (evidence of intent)
    path2: str | None = None      # caller-supplied target path (rename)
    resolved: str | None = None   # resolved logical path inside the root
    resolved2: str | None = None  # resolved logical target path (rename)
    dev: int | None = None        # device of the actually accessed object
    ino: int | None = None        # inode of the actually accessed object
    chain: tuple[tuple[int, int], ...] = field(default_factory=tuple)
    result: str = "ok"            # "ok" | "denied" | "error"
    reason: str | None = None


class ListSink:
    """Thread-safe in-memory audit sink (also handy for tests)."""

    def __init__(self):
        self._events: list[AuditEvent] = []
        self._lock = threading.Lock()

    def __call__(self, event: AuditEvent) -> None:
        with self._lock:
            self._events.append(event)

    @property
    def events(self) -> tuple[AuditEvent, ...]:
        with self._lock:
            return tuple(self._events)
