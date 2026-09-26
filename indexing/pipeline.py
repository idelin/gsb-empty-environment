"""Indexing pipeline: dedupe -> reorder buffer -> atomic batch commit -> persist.

Guarantees:
* Ordering:   events are applied in strictly ascending, gap-free seq order.
* Idempotency: seqs at or below the checkpoint (or already buffered /
  pending) are dropped as duplicates; index apply itself is version-based
  and set-semantics, so replays never double-count term frequencies.
* Atomicity:  a commit publishes the whole drained batch in one shot.
* Durability: checkpoint, buffer, pending batch and index are persisted as
  JSON (atomic tmp-file + os.replace) and survive restarts.
* Missing seqs are reported explicitly, never skipped silently.
"""
from __future__ import annotations

import json
import os
from dataclasses import dataclass
from pathlib import Path

from .buffer import SeqBuffer
from .events import Event
from .index import BatchResult, InvertedIndex

STATE_FILE = "state.json"
INDEX_FILE = "index.json"


@dataclass(frozen=True)
class SubmitResult:
    status: str  # "drained" | "buffered" | "duplicate"
    seq: int
    drained: int
    missing: tuple  # inclusive (lo, hi) gap ranges known right now


@dataclass(frozen=True)
class CommitResult:
    committed: int
    checkpoint: int
    batch: BatchResult | None


class IndexingPipeline:
    def __init__(self, state_dir: str | os.PathLike):
        self.state_dir = Path(state_dir)
        self.state_dir.mkdir(parents=True, exist_ok=True)
        self.index = InvertedIndex()
        self.checkpoint = 0  # last contiguous *committed* seq
        self.buffer = SeqBuffer(next_expected=1)
        self.pending_batch: list[Event] = []  # drained, not yet committed
        self.duplicate_count = 0
        self._load()

    # ---------------- ingestion ----------------

    def submit(self, event: Event) -> SubmitResult:
        high_water = self.checkpoint + len(self.pending_batch)
        if event.seq <= high_water:
            self.duplicate_count += 1
            self._save_state()
            return SubmitResult("duplicate", event.seq, 0, tuple(self.missing_seqs()))
        status, drained = self.buffer.push(event)
        if status == "duplicate":
            self.duplicate_count += 1
        self.pending_batch.extend(drained)
        self._save_state()
        return SubmitResult(status, event.seq, len(drained), tuple(self.missing_seqs()))

    def commit(self) -> CommitResult:
        """Atomically publish the drained batch and advance the checkpoint."""
        if not self.pending_batch:
            return CommitResult(0, self.checkpoint, None)
        batch = self.index.apply_batch(self.pending_batch)
        committed = len(self.pending_batch)
        self.checkpoint = self.pending_batch[-1].seq
        self.pending_batch = []
        # Index first, then checkpoint. A crash between the two is safe:
        # the events stay in pending_batch and re-apply is a version no-op.
        self._save_index()
        self._save_state()
        return CommitResult(committed, self.checkpoint, batch)

    # ---------------- reporting ----------------

    def missing_seqs(self) -> list:
        """Inclusive (lo, hi) ranges of seqs known to be missing."""
        return self.buffer.missing_ranges()

    def stats(self) -> dict:
        return {
            "checkpoint": self.checkpoint,
            "commit_version": self.index.commit_version,
            "pending_batch": len(self.pending_batch),
            "buffered": sorted(self.buffer.pending),
            "duplicates_dropped": self.duplicate_count,
            "docs": self.index.doc_count(),
        }

    def search(self, term: str) -> dict:
        return self.index.search(term)

    # ---------------- persistence ----------------

    def _save_state(self) -> None:
        payload = {
            "checkpoint": self.checkpoint,
            "duplicate_count": self.duplicate_count,
            "pending_batch": [e.to_dict() for e in self.pending_batch],
            "buffer": self.buffer.to_dict(),
        }
        _write_json_atomic(self.state_dir / STATE_FILE, payload)

    def _save_index(self) -> None:
        _write_json_atomic(self.state_dir / INDEX_FILE, self.index.to_dict())

    def _load(self) -> None:
        index_path = self.state_dir / INDEX_FILE
        state_path = self.state_dir / STATE_FILE
        if index_path.exists():
            self.index = InvertedIndex.from_dict(_read_json(index_path))
        if state_path.exists():
            state = _read_json(state_path)
            self.checkpoint = state["checkpoint"]
            self.duplicate_count = state["duplicate_count"]
            self.pending_batch = [Event.from_dict(e) for e in state["pending_batch"]]
            self.buffer = SeqBuffer.from_dict(state["buffer"])


def _write_json_atomic(path: Path, payload: dict) -> None:
    tmp = path.with_suffix(path.suffix + ".tmp")
    with open(tmp, "w", encoding="utf-8") as fh:
        json.dump(payload, fh, ensure_ascii=False, indent=2, sort_keys=True)
    os.replace(tmp, path)


def _read_json(path: Path) -> dict:
    with open(path, encoding="utf-8") as fh:
        return json.load(fh)
