"""Facet counting engine with self-exclusion (disjunctive faceting).

Counting scope, made explicit:
  * ``base``     = documents matching the main :class:`TextQuery`.
  * For every reported dimension *D*, its counts are computed inside
    ``context(D) = base AND (filters of every OTHER selected dimension)``.
    D's own selection is excluded from its own context, so the user sees
    what other values could still be OR-ed in.  When two dimensions are
    selected, each one's counts are computed under the other's filter.
  * OR inside a dimension, AND across dimensions (see model.py).

Sharing / no-rescan guarantees:
  * The base bitmap is computed once and reused by every dimension.
  * Each dimension's filter bitmap is computed once and reused by the
    contexts of all other dimensions (prefix/suffix AND products).
  * Each term/value bitmap is fetched from the index at most once per
    engine instance; repeated requests are served from cache and counted
    in ``Metrics.cache_hits``.
  * The counting path never touches document text: ``docs_examined``
    stays 0 (the brute-force reference in reference.py is the only place
    that iterates documents, and it is used for cross-checking only).
"""

from __future__ import annotations

from dataclasses import dataclass, field, replace

from .model import EMPTY, MISSING, TextQuery
from .store import Snapshot
from .tokenizer import tokenize


@dataclass
class Metrics:
    bitmap_lookups: int = 0   # bitmaps actually fetched from the index
    cache_hits: int = 0       # requests served by a reused bitmap
    bitmap_ops: int = 0       # AND/OR/XOR operations on bitmaps
    docs_examined: int = 0    # documents touched (must stay 0 here)


@dataclass
class DimensionReport:
    dimension: str
    context_size: int            # |context(D)|: docs this dimension counts over
    counts: dict[str, int]       # value -> docs in context having it (deduped)
    missing: int                 # context docs where the attribute is absent
    empty: int                   # context docs where the attribute is []


@dataclass
class FacetReport:
    base_count: int
    dimensions: dict[str, DimensionReport]
    metrics: Metrics


class FacetEngine:
    """Computes facet counts over one immutable snapshot."""

    def __init__(self, snapshot: Snapshot) -> None:
        self._snap = snapshot
        self.metrics = Metrics()
        self._term_cache: dict[str, int] = {}
        self._value_cache: dict[tuple[str, object], int] = {}
        self._filter_cache: dict[str, int] = {}
        self._context_cache: dict[str, int] = {}

    # ---------------- primitive bitmap helpers ----------------

    def _and(self, a: int | None, b: int | None) -> int | None:
        if a is None:
            return b
        if b is None:
            return a
        self.metrics.bitmap_ops += 1
        return a & b

    def _or(self, a: int | None, b: int | None) -> int | None:
        if a is None:
            return b
        if b is None:
            return a
        self.metrics.bitmap_ops += 1
        return a | b

    def _not(self, b: int) -> int:
        # every indexed bitmap is a subset of `alive`, so XOR is exact NOT
        self.metrics.bitmap_ops += 1
        return self._snap.alive ^ b

    # ---------------- cached bitmap sources ----------------

    def term_bitmap(self, term: str) -> int:
        if term in self._term_cache:
            self.metrics.cache_hits += 1
            return self._term_cache[term]
        tokens = tokenize(term)
        if not tokens:
            raise ValueError(f"query term yields no tokens: {term!r}")
        bm: int | None = None
        for tok in tokens:
            self.metrics.bitmap_lookups += 1
            bm = self._and(bm, self._snap.token_index.get(tok, 0))
        assert bm is not None
        self._term_cache[term] = bm
        return bm

    def value_bitmap(self, dimension: str, value) -> int:
        key = (dimension, value)
        if key in self._value_cache:
            self.metrics.cache_hits += 1
            return self._value_cache[key]
        self.metrics.bitmap_lookups += 1
        snap = self._snap
        if value is MISSING:
            # a dimension unknown to the index is missing on every live doc
            bm = snap.alive if dimension not in snap.known_dims \
                else snap.missing_index.get(dimension, 0)
        elif value is EMPTY:
            bm = snap.empty_index.get(dimension, 0)
        else:
            bm = snap.facet_index.get((dimension, value), 0)
        self._value_cache[key] = bm
        return bm

    def dimension_filter(self, dimension: str, values: tuple) -> int:
        """OR of the selected values inside one dimension (cached)."""
        if dimension in self._filter_cache:
            self.metrics.cache_hits += 1
            return self._filter_cache[dimension]
        bm: int | None = None
        for v in dict.fromkeys(values):  # dedupe, keep order
            bm = self._or(bm, self.value_bitmap(dimension, v))
        assert bm is not None
        self._filter_cache[dimension] = bm
        return bm

    # ---------------- main query ----------------

    def base_bitmap(self, query: TextQuery) -> int:
        bm: int | None = None
        for term in query.all_of:
            bm = self._and(bm, self.term_bitmap(term))
        if query.any_of:
            any_bm: int | None = None
            for term in query.any_of:
                any_bm = self._or(any_bm, self.term_bitmap(term))
            bm = self._and(bm, any_bm)
        for term in query.none_of:
            bm = self._and(bm, self._not(self.term_bitmap(term)))
        return self._snap.alive if bm is None else bm

    # ---------------- facet computation ----------------

    def context_bitmap(self, dimension: str, base: int,
                       selected: list[str], filters: dict[str, int]) -> int:
        if dimension in self._context_cache:
            self.metrics.cache_hits += 1
            return self._context_cache[dimension]
        n = len(selected)
        # prefix/suffix AND products: context of every dimension is
        # assembled from shared intermediates, never recomputed wholesale
        pre: list[int | None] = [None] * (n + 1)
        suf: list[int | None] = [None] * (n + 1)
        for i, dim in enumerate(selected):
            pre[i + 1] = self._and(pre[i], filters[dim])
        for i in range(n - 1, -1, -1):
            suf[i] = self._and(suf[i + 1], filters[selected[i]])
        ctx: int | None = base
        if dimension in filters:
            i = selected.index(dimension)
            ctx = self._and(ctx, self._and(pre[i], suf[i + 1]))
        else:
            ctx = self._and(ctx, pre[n])
        assert ctx is not None
        self._context_cache[dimension] = ctx
        return ctx

    def compute(self, query: TextQuery | None = None,
                selections: dict[str, tuple] | None = None,
                dimensions: list[str] | None = None) -> FacetReport:
        query = query or TextQuery()
        selections = selections or {}
        snap = self._snap
        base = self.base_bitmap(query)
        selected = sorted(d for d, vals in selections.items() if vals)
        filters = {d: self.dimension_filter(d, tuple(selections[d]))
                   for d in selected}
        if dimensions is None:
            dims = sorted(set(snap.dimensions()) | set(selected))
        else:
            dims = list(dimensions)
        reports: dict[str, DimensionReport] = {}
        for dim in dims:
            ctx = self.context_bitmap(dim, base, selected, filters)
            universe = sorted(
                set(snap.values_of(dim))
                | {v for v in selections.get(dim, ()) if isinstance(v, str)})
            counts = {}
            for v in universe:
                counts[v] = self._and(ctx, self.value_bitmap(dim, v)).bit_count()
            reports[dim] = DimensionReport(
                dimension=dim,
                context_size=ctx.bit_count(),
                counts=counts,
                missing=self._and(ctx, self.value_bitmap(dim, MISSING)).bit_count(),
                empty=self._and(ctx, self.value_bitmap(dim, EMPTY)).bit_count(),
            )
        return FacetReport(
            base_count=base.bit_count(),
            dimensions=reports,
            metrics=replace(self.metrics),
        )
