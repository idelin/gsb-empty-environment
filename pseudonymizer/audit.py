"""Audit log that records events by pseudonym only.

Raw subjects and key material must never appear in audit records.
``record`` refuses the field names ``subject`` and ``key`` outright, and
the store layer only ever passes pseudonyms, domains, versions and
counters.
"""

from __future__ import annotations

import threading
import time

FORBIDDEN_FIELDS = frozenset({"subject", "key", "secret", "raw_subject"})


class AuditLog:
    def __init__(self) -> None:
        self._lock = threading.Lock()
        self._records: list[dict] = []

    def record(self, event: str, **fields) -> dict:
        bad = FORBIDDEN_FIELDS & fields.keys()
        if bad:
            raise ValueError(
                f"audit record may not contain field(s): {sorted(bad)}"
            )
        entry = {"event": event, "ts": time.time(), **fields}
        with self._lock:
            self._records.append(entry)
        return entry

    @property
    def records(self) -> list[dict]:
        with self._lock:
            return [dict(r) for r in self._records]

    def dump(self) -> str:
        lines = []
        for r in self.records:
            fields = " ".join(
                f"{k}={v}" for k, v in r.items() if k not in ("event", "ts")
            )
            lines.append(f"[{r['ts']:.3f}] {r['event']} {fields}".rstrip())
        return "\n".join(lines)
