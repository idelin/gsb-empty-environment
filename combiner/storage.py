"""Local-file state for the combiner.

Layout under the storage root::

    journal.log          append-only event journal (one JSON object per line)
    acks.log             one acked result id per line
    published/<id>.json  checksummed publish records (atomic tmp + rename)
    snapshots/<w>-<seq>.json  window state snapshots tied to a journal offset

Every write is flushed and fsynced; no external message platform is involved,
so consistency can be verified by replaying these files alone.
"""
from __future__ import annotations

import hashlib
import json
import os
from pathlib import Path


def _fsync_dir(path: Path) -> None:
    fd = os.open(path, os.O_RDONLY)
    try:
        os.fsync(fd)
    finally:
        os.close(fd)


class Storage:
    def __init__(self, root: str | Path):
        self.root = Path(root)
        self.published_dir = self.root / "published"
        self.snapshots_dir = self.root / "snapshots"
        self.published_dir.mkdir(parents=True, exist_ok=True)
        self.snapshots_dir.mkdir(parents=True, exist_ok=True)
        self.journal_path = self.root / "journal.log"
        self.acks_path = self.root / "acks.log"

    # ---------------------------------------------------------------- journal
    def append_journal(self, record: dict) -> None:
        line = json.dumps(record, sort_keys=True)
        with open(self.journal_path, "a", encoding="utf-8") as fh:
            fh.write(line + "\n")
            fh.flush()
            os.fsync(fh.fileno())

    def journal_offset(self) -> int:
        """Current journal length in lines (used to anchor snapshots)."""
        if not self.journal_path.exists():
            return 0
        with open(self.journal_path, "rb") as fh:
            return sum(1 for _ in fh)

    def read_journal(self) -> list[dict]:
        """Read journal records; a torn final line (crashed write) is dropped."""
        records: list[dict] = []
        if not self.journal_path.exists():
            return records
        with open(self.journal_path, "r", encoding="utf-8") as fh:
            for line in fh:
                line = line.strip()
                if not line:
                    continue
                try:
                    records.append(json.loads(line))
                except json.JSONDecodeError:
                    break  # torn tail from a crashed write
        return records

    # -------------------------------------------------------------- published
    def write_published(self, record: dict) -> None:
        """Atomically write a checksummed publish record."""
        payload = json.dumps(record, sort_keys=True).encode()
        checksum = hashlib.sha256(payload).hexdigest()
        envelope = json.dumps({"record": record, "sha256": checksum},
                              sort_keys=True, indent=2)
        tmp = self.published_dir / f"{record['result_id']}.json.tmp"
        final = self.published_dir / f"{record['result_id']}.json"
        with open(tmp, "w", encoding="utf-8") as fh:
            fh.write(envelope)
            fh.flush()
            os.fsync(fh.fileno())
        os.replace(tmp, final)
        _fsync_dir(self.published_dir)

    def read_published(self) -> dict[str, dict]:
        out: dict[str, dict] = {}
        for path in sorted(self.published_dir.glob("*.json")):
            envelope = json.loads(path.read_text(encoding="utf-8"))
            payload = json.dumps(envelope["record"], sort_keys=True).encode()
            if hashlib.sha256(payload).hexdigest() != envelope["sha256"]:
                raise ValueError(f"checksum mismatch in {path}")
            out[envelope["record"]["result_id"]] = envelope["record"]
        return out

    # -------------------------------------------------------------------- ack
    def append_ack(self, result_id: str) -> None:
        with open(self.acks_path, "a", encoding="utf-8") as fh:
            fh.write(result_id + "\n")
            fh.flush()
            os.fsync(fh.fileno())

    def read_acks(self) -> list[str]:
        if not self.acks_path.exists():
            return []
        return [line.strip() for line in
                self.acks_path.read_text(encoding="utf-8").splitlines()
                if line.strip()]

    # --------------------------------------------------------------- snapshot
    def write_snapshot(self, window_id: str, seq: int, state: dict,
                       journal_offset: int) -> None:
        payload = {"window_id": window_id, "seq": seq,
                   "journal_offset": journal_offset, "state": state}
        final = self.snapshots_dir / f"{window_id}-{seq:04d}.json"
        tmp = final.with_suffix(".json.tmp")
        with open(tmp, "w", encoding="utf-8") as fh:
            fh.write(json.dumps(payload, sort_keys=True, indent=2))
            fh.flush()
            os.fsync(fh.fileno())
        os.replace(tmp, final)
        _fsync_dir(self.snapshots_dir)

    def read_snapshots(self) -> list[dict]:
        return [json.loads(path.read_text(encoding="utf-8"))
                for path in sorted(self.snapshots_dir.glob("*.json"))]
