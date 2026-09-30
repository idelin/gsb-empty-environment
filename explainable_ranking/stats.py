"""Versioned corpus statistics (document frequencies).

idf(term) = ln((doc_count + 1) / (df(term) + 1)) + 1

The formula is fixed and recomputed by the verifier; snapshots are
kept forever so an explanation bound to ``stats-v1`` remains checkable
even after ``stats-v2`` is published. If a snapshot is missing, the
explanation can only be reported as *unverifiable*.
"""
from __future__ import annotations

import math
from collections import Counter


class StatsSnapshot:
    """Immutable term statistics for one corpus state."""

    def __init__(self, version: str, df: dict[str, int], doc_count: int) -> None:
        self.version = version
        self.df = dict(df)
        self.doc_count = doc_count

    @classmethod
    def build(cls, version: str, tokenized_docs: list[list[str]]) -> "StatsSnapshot":
        df: Counter[str] = Counter()
        for tokens in tokenized_docs:
            df.update(set(tokens))
        return cls(version, dict(df), len(tokenized_docs))

    def idf(self, term: str) -> float:
        return math.log((self.doc_count + 1) / (self.df.get(term, 0) + 1)) + 1.0


class StatsStore:
    def __init__(self) -> None:
        self._snapshots: dict[str, StatsSnapshot] = {}

    def put(self, snapshot: StatsSnapshot) -> None:
        self._snapshots[snapshot.version] = snapshot

    def get(self, version: str) -> StatsSnapshot | None:
        return self._snapshots.get(version)

    def drop(self, version: str) -> None:
        self._snapshots.pop(version, None)
