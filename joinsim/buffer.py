"""Spill buffer for the BUFFER strategy: bounded memory + bounded temp disk.

Rows that cannot be processed yet (filter incomplete) are held in memory up
to ``mem_quota_rows``, then spilled to JSON-lines files under ``tmpdir`` up
to ``disk_quota_bytes``. When BOTH quotas are exhausted, ``try_add`` returns
False -- backpressure. The engine then keeps the row pending and retries
later; rows are never dropped.

Spill files are deleted as soon as they are fully drained, and the directory
is empty again once every buffered row has been processed.
"""
from __future__ import annotations

import json
import os
from typing import Any, Iterator, Optional


class SpillBuffer:
    def __init__(self, tmpdir: str, mem_quota_rows: int = 8,
                 disk_quota_bytes: int = 1 << 20) -> None:
        self.tmpdir = tmpdir
        self.mem_quota_rows = mem_quota_rows
        self.disk_quota_bytes = disk_quota_bytes
        self._mem: list[dict] = []            # up to mem_quota_rows records
        self._files: list[str] = []           # spill file paths, in write order
        self._disk_bytes = 0
        self._seq = 0
        os.makedirs(tmpdir, exist_ok=True)

    # ------------------------------------------------------------------ add
    def try_add(self, record: dict) -> bool:
        """Buffer one record. False => both quotas exhausted (backpressure)."""
        if len(self._mem) < self.mem_quota_rows:
            self._mem.append(record)
            return True
        line = (json.dumps(record) + "\n").encode()
        if self._disk_bytes + len(line) > self.disk_quota_bytes:
            return False  # memory full AND disk full: apply backpressure
        path = self._current_file()
        with open(path, "ab") as fh:
            fh.write(line)
        self._disk_bytes += len(line)
        return True

    def _current_file(self) -> str:
        if not self._files:
            self._seq += 1
            self._files.append(os.path.join(self.tmpdir, f"spill-{self._seq}.jsonl"))
        return self._files[-1]

    # ----------------------------------------------------------------- drain
    def __len__(self) -> int:
        return len(self._mem) + sum(self._count_lines(p) for p in self._files)

    @staticmethod
    def _count_lines(path: str) -> int:
        with open(path, "rb") as fh:
            return sum(1 for _ in fh)

    def pop_all(self) -> Iterator[dict]:
        """Yield every buffered record (memory first, then spill files in
        order), deleting each spill file as soon as it is fully consumed."""
        while self._mem:
            yield self._mem.pop(0)
        for path in self._files:
            with open(path, "rb") as fh:
                for line in fh:
                    yield json.loads(line)
            os.remove(path)  # temp file released immediately after drain
        self._files = []
        self._disk_bytes = 0

    def peek_all(self) -> list[dict]:
        """Non-destructive snapshot (used for checkpointing)."""
        out = list(self._mem)
        for path in self._files:
            with open(path, "rb") as fh:
                out.extend(json.loads(line) for line in fh)
        return out

    # ------------------------------------------------------------ checkpoint
    def to_dict(self) -> dict:
        return {"records": self.peek_all()}

    @classmethod
    def restore(cls, tmpdir: str, data: dict, mem_quota_rows: int,
                disk_quota_bytes: int) -> "SpillBuffer":
        buf = cls(tmpdir, mem_quota_rows, disk_quota_bytes)
        for rec in data.get("records", []):
            ok = buf.try_add(rec)
            assert ok, "restored buffer must fit within quotas"
        return buf

    def leftover_files(self) -> list[str]:
        return [f for f in os.listdir(self.tmpdir) if f.startswith("spill-")]
