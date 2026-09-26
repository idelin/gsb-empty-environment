"""Inverted index with atomic batch commits and version-idempotent apply."""
from __future__ import annotations

import threading
from dataclasses import dataclass, field

from .events import OP_DELETE, OP_UPSERT, Event
from .tokenizer import term_counts


@dataclass(frozen=True)
class BatchResult:
    applied: tuple  # events actually applied
    skipped: tuple  # (event, reason) pairs
    commit_version: int


@dataclass(frozen=True)
class IndexSnapshot:
    """Immutable, consistent read view taken at one commit_version."""

    postings: dict  # term -> {doc_id: tf}
    doc_count: int
    commit_version: int

    def search(self, term: str) -> dict:
        return dict(self.postings.get(term.lower(), {}))


class InvertedIndex:
    """term -> {doc_id: tf} inverted index.

    Atomicity: a batch is validated and applied under a single lock hold
    and ``commit_version`` is bumped exactly once, so readers (``snapshot``
    / ``search``) observe either the whole batch or none of it.

    Idempotency: term frequencies are *derived* from document text (set,
    never incremented), and an event is applied to a document only when its
    effective version is strictly newer. Re-applying the same event is a
    no-op, so restarts/replays cannot double-count word frequencies.
    """

    def __init__(self):
        self._lock = threading.RLock()
        self._docs: dict[str, dict] = {}  # doc_id -> {"version": int, "terms": {term: tf}}
        self._tombstones: dict[str, int] = {}  # doc_id -> version of the delete
        self._postings: dict[str, dict[str, int]] = {}
        self.commit_version = 0

    # ---------------- write path ----------------

    def apply_batch(self, events: list) -> BatchResult:
        applied: list[Event] = []
        skipped: list[tuple[Event, str]] = []
        with self._lock:
            for event in events:
                reason = self._check_stale(event)
                if reason is not None:
                    skipped.append((event, reason))
                    continue
                self._apply_one(event)
                applied.append(event)
            if applied:
                self.commit_version += 1
            return BatchResult(tuple(applied), tuple(skipped), self.commit_version)

    def _current_version(self, doc_id: str) -> int | None:
        if doc_id in self._docs:
            return self._docs[doc_id]["version"]
        return self._tombstones.get(doc_id)

    def _check_stale(self, event: Event) -> str | None:
        current = self._current_version(event.doc_id)
        if current is not None and event.effective_version <= current:
            return f"stale-or-duplicate version {event.effective_version} <= {current}"
        return None

    def _apply_one(self, event: Event) -> None:
        if event.op == OP_UPSERT:
            terms = term_counts(event.text)
            self._remove_postings(event.doc_id)
            self._tombstones.pop(event.doc_id, None)
            self._docs[event.doc_id] = {"version": event.effective_version, "terms": terms}
            for term, tf in terms.items():
                self._postings.setdefault(term, {})[event.doc_id] = tf
        elif event.op == OP_DELETE:
            self._remove_postings(event.doc_id)
            self._docs.pop(event.doc_id, None)
            # Tombstone blocks older-versioned upserts from resurrecting the doc.
            self._tombstones[event.doc_id] = event.effective_version
        else:  # pragma: no cover - Event validates op
            raise ValueError(event.op)

    def _remove_postings(self, doc_id: str) -> None:
        doc = self._docs.get(doc_id)
        if not doc:
            return
        for term in doc["terms"]:
            posting = self._postings.get(term)
            if posting and posting.pop(doc_id, None) is not None and not posting:
                del self._postings[term]

    # ---------------- read path ----------------

    def snapshot(self) -> IndexSnapshot:
        with self._lock:
            return IndexSnapshot(
                postings={t: dict(p) for t, p in self._postings.items()},
                doc_count=len(self._docs),
                commit_version=self.commit_version,
            )

    def search(self, term: str) -> dict:
        with self._lock:
            return dict(self._postings.get(term.lower(), {}))

    def doc_count(self) -> int:
        with self._lock:
            return len(self._docs)

    # ---------------- persistence ----------------

    def to_dict(self) -> dict:
        with self._lock:
            return {
                "commit_version": self.commit_version,
                "docs": {
                    d: {"version": v["version"], "terms": dict(v["terms"])}
                    for d, v in self._docs.items()
                },
                "tombstones": dict(self._tombstones),
                "postings": {t: dict(p) for t, p in self._postings.items()},
            }

    @classmethod
    def from_dict(cls, data: dict) -> "InvertedIndex":
        index = cls()
        index.commit_version = data["commit_version"]
        index._docs = {
            d: {"version": v["version"], "terms": dict(v["terms"])}
            for d, v in data["docs"].items()
        }
        index._tombstones = dict(data["tombstones"])
        index._postings = {t: dict(p) for t, p in data["postings"].items()}
        return index
