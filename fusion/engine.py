"""Query engine: validation, threshold TopK with degradation, verification
against the exhaustive reference, and fixed-snapshot pagination.

Behaviours (public, mirrored in README.md):

* Negative weights make the TopK upper bound invalid.  With
  ``on_negative_weights="reject"`` (default) the engine raises
  :class:`NegativeWeightError`; with ``"degrade"`` it silently falls back to
  the exhaustive reference fusion with the signed weights (documents whose
  present-weight sum is <= 0 are dropped).
* If a sorted-access stream violates the non-increasing assumption, the
  engine degrades to the exhaustive reference for that query and reports
  ``degraded=True`` with the reason in the result metadata.
* ``verify()`` runs both the threshold algorithm and the exhaustive
  reference and compares them exactly.
* Snapshots freeze a full ranking under a token.  Paging a snapshot with
  offset/limit never duplicates or omits items, and updating the index does
  not mutate existing snapshots (they keep serving their frozen baseline);
  new searches explicitly use the new index version and its freshly computed
  normalization statistics.
"""

from __future__ import annotations

from dataclasses import dataclass
from typing import Mapping, Optional

from .fusion import (
    AssumptionViolatedError,
    NegativeWeightError,
    ScoredDoc,
    exhaustive_topk,
    threshold_topk,
    validate_weights,
)
from .index import FieldQueryView, Index
from .tokenizer import tokenize


class SnapshotNotFoundError(KeyError):
    """The pagination token does not name a live snapshot."""


@dataclass(frozen=True)
class SearchResult:
    items: list[ScoredDoc]
    meta: dict


@dataclass(frozen=True)
class Snapshot:
    token: str
    query: str
    weights: Mapping[str, float]
    index_version: int
    total: int


@dataclass(frozen=True)
class Page:
    token: str
    offset: int
    limit: int
    items: list[ScoredDoc]
    next_offset: Optional[int]
    total: int


class Engine:
    def __init__(self, index: Index, *, on_negative_weights: str = "reject"):
        if on_negative_weights not in ("reject", "degrade"):
            raise ValueError("on_negative_weights must be 'reject' or 'degrade'")
        self._index = index
        self._index_version = 1
        self._on_negative = on_negative_weights
        self._snapshots: dict[str, list[ScoredDoc]] = {}
        self._snapshot_counter = 0

    @property
    def index_version(self) -> int:
        return self._index_version

    def update_index(self, index: Index) -> None:
        """Install a new index (an explicit, versioned baseline change).

        Existing snapshots keep serving their frozen data; new searches use
        the new index and its new explicit normalization statistics.
        """
        self._index = index
        self._index_version += 1

    def _build_views(self, query: str, fields) -> dict[str, FieldQueryView]:
        terms = tokenize(query)
        return {f: FieldQueryView(self._index, f, terms) for f in fields}

    def _stats_meta(self, views) -> dict:
        return {
            f: {"min": v.stats.minimum, "max": v.stats.maximum}
            for f, v in sorted(views.items())
        }

    def search(self, query: str, weights: Mapping[str, float], k: int = 10) -> SearchResult:
        active, negatives = validate_weights(weights, self._index.fields)
        meta: dict = {
            "query": query,
            "k": k,
            "index_version": self._index_version,
            "degraded": False,
            "reason": None,
        }
        if negatives:
            if self._on_negative == "reject":
                raise NegativeWeightError(
                    f"negative weights {negatives} invalidate the TopK upper bound"
                )
            signed = {f: float(w) for f, w in weights.items() if w != 0}
            views = self._build_views(query, signed)
            items, ref_meta = exhaustive_topk(views, signed, k)
            meta.update(
                mode="exhaustive",
                degraded=True,
                reason="negative weights: upper bound invalid, degraded to exhaustive fusion",
                fields=sorted(signed),
                stats=self._stats_meta(views),
                candidates_scored=ref_meta["candidates_scored"],
            )
            return SearchResult(items, meta)
        if not active:
            meta.update(mode="none", reason="all weights are zero: empty ranking")
            return SearchResult([], meta)
        views = self._build_views(query, active)
        try:
            items, ta_meta = threshold_topk(views, active, k)
            meta.update(mode="ta", **ta_meta)
        except AssumptionViolatedError as exc:
            items, ref_meta = exhaustive_topk(views, active, k)
            meta.update(
                mode="exhaustive",
                degraded=True,
                reason=f"upper-bound assumption violated ({exc}); degraded to exhaustive fusion",
                fields=sorted(active),
                candidates_scored=ref_meta["candidates_scored"],
            )
        meta["stats"] = self._stats_meta(views)
        return SearchResult(items, meta)

    def exhaustive(
        self, query: str, weights: Mapping[str, float], k: Optional[int] = 10
    ) -> SearchResult:
        """Reference fusion over all candidates (ground truth for verify())."""
        validate_weights(weights, self._index.fields)
        nonzero = {f: float(w) for f, w in weights.items() if w != 0}
        if not nonzero:
            return SearchResult([], {"query": query, "mode": "none"})
        views = self._build_views(query, nonzero)
        items, ref_meta = exhaustive_topk(views, nonzero, k)
        meta = {
            "query": query,
            "k": k,
            "index_version": self._index_version,
            "mode": "exhaustive",
            "degraded": False,
            "reason": None,
            "stats": self._stats_meta(views),
            **ref_meta,
        }
        return SearchResult(items, meta)

    def verify(self, query: str, weights: Mapping[str, float], k: int = 10) -> dict:
        """Cross-check the (possibly degraded) search against the reference."""
        result = self.search(query, weights, k)
        reference = self.exhaustive(query, weights, k)
        got = [(sd.doc_id, sd.score) for sd in result.items]
        want = [(sd.doc_id, sd.score) for sd in reference.items]
        mismatches = [
            {"position": i, "got": g, "want": w}
            for i, (g, w) in enumerate(zip(got, want))
            if g != w
        ]
        if len(got) != len(want):
            mismatches.append({"got_len": len(got), "want_len": len(want)})
        return {
            "ok": not mismatches,
            "mode": result.meta.get("mode"),
            "degraded": result.meta.get("degraded"),
            "mismatches": mismatches,
        }

    def snapshot(self, query: str, weights: Mapping[str, float]) -> Snapshot:
        """Freeze the full ranking for stable offset/limit pagination."""
        result = self.exhaustive(query, weights, k=None)
        self._snapshot_counter += 1
        token = f"v{self._index_version}-s{self._snapshot_counter}"
        self._snapshots[token] = list(result.items)
        return Snapshot(
            token=token,
            query=query,
            weights=dict(weights),
            index_version=self._index_version,
            total=len(result.items),
        )

    def page(self, token: str, offset: int = 0, limit: int = 10) -> Page:
        if token not in self._snapshots:
            raise SnapshotNotFoundError(token)
        if offset < 0:
            raise ValueError("offset must be >= 0")
        if limit <= 0:
            raise ValueError("limit must be > 0")
        items = self._snapshots[token]
        chunk = items[offset : offset + limit]
        next_offset = offset + limit if offset + limit < len(items) else None
        return Page(
            token=token,
            offset=offset,
            limit=limit,
            items=list(chunk),
            next_offset=next_offset,
            total=len(items),
        )
