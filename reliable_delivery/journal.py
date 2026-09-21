"""Append-only journal with explicit flush, so tests can crash mid-flight.

Records sit in a pending buffer until ``flush()``; ``crash()`` drops the
pending buffer, simulating a process death before the records hit disk.
"""
from __future__ import annotations

import json
from pathlib import Path
from typing import Iterator


class Journal:
    def __init__(self, path: Path):
        self.path = Path(path)
        self._pending: list[dict] = []

    def append(self, record: dict) -> None:
        self._pending.append(record)

    def flush(self) -> None:
        if not self._pending:
            return
        self.path.parent.mkdir(parents=True, exist_ok=True)
        with open(self.path, "a", encoding="utf-8") as fh:
            for record in self._pending:
                fh.write(json.dumps(record, sort_keys=True) + "\n")
        self._pending.clear()

    def crash(self) -> None:
        """Lose everything not yet flushed."""
        self._pending.clear()

    def replay(self) -> Iterator[dict]:
        if not self.path.exists():
            return
        with open(self.path, "r", encoding="utf-8") as fh:
            for line in fh:
                line = line.strip()
                if line:
                    yield json.loads(line)
