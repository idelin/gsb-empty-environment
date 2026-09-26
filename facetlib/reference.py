"""Brute-force per-document boolean reference implementation.

This is the correctness oracle: it iterates every document, evaluates
the main query and every dimension filter with plain Python booleans,
and counts facets directly.  It shares nothing with the bitmap engine
except the tokenizer and the semantic definitions, so the two can
cross-check each other in tests.  It is O(docs x dimensions) and is
*not* used on the engine's counting path.
"""

from __future__ import annotations

from .engine import DimensionReport, FacetReport, Metrics
from .model import EMPTY, MISSING, Document, TextQuery
from .tokenizer import token_set, tokenize


def _term_tokens(term: str) -> set[str]:
    toks = set(tokenize(term))
    if not toks:
        raise ValueError(f"query term yields no tokens: {term!r}")
    return toks


def query_matches(tokens: set[str], query: TextQuery) -> bool:
    if any(not _term_tokens(t) <= tokens for t in query.all_of):
        return False
    if query.any_of and not any(_term_tokens(t) <= tokens
                                for t in query.any_of):
        return False
    if any(_term_tokens(t) <= tokens for t in query.none_of):
        return False
    return True


def value_matches(doc: Document, dimension: str, value) -> bool:
    if value is MISSING:
        return dimension not in doc.attributes
    if value is EMPTY:
        return dimension in doc.attributes and not doc.attributes[dimension]
    return value in doc.attributes.get(dimension, ())


def dimension_matches(doc: Document, dimension: str, values: tuple) -> bool:
    """OR of the selected values inside one dimension."""
    return any(value_matches(doc, dimension, v) for v in values)


def reference_facets(docs, query: TextQuery | None = None,
                     selections: dict[str, tuple] | None = None,
                     dimensions: list[str] | None = None) -> FacetReport:
    query = query or TextQuery()
    selections = selections or {}
    docs = list(docs)
    metrics = Metrics()

    base_docs: list[Document] = []
    for doc in docs:
        metrics.docs_examined += 1
        if query_matches(token_set(doc.text), query):
            base_docs.append(doc)

    selected = sorted(d for d, vals in selections.items() if vals)
    if dimensions is None:
        dims = sorted({d for doc in docs for d in doc.attributes}
                      | set(selected))
    else:
        dims = list(dimensions)

    reports = {}
    for dim in dims:
        universe = sorted(
            {v for doc in docs for v in doc.attributes.get(dim, ())}
            | {v for v in selections.get(dim, ()) if isinstance(v, str)})
        counts = {v: 0 for v in universe}
        missing = empty = context_size = 0
        for doc in base_docs:
            metrics.docs_examined += 1
            # self-exclusion: filters of every OTHER dimension (AND)
            if not all(dimension_matches(doc, other, selections[other])
                       for other in selected if other != dim):
                continue
            context_size += 1
            if dim not in doc.attributes:
                missing += 1
            elif not doc.attributes[dim]:
                empty += 1
            else:
                for v in set(doc.attributes[dim]):  # dedupe per doc
                    counts[v] += 1
        reports[dim] = DimensionReport(
            dimension=dim, context_size=context_size,
            counts=counts, missing=missing, empty=empty)

    return FacetReport(base_count=len(base_docs), dimensions=reports,
                       metrics=metrics)
