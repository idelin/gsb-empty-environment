"""In-memory index over local synthetic UTF-8 documents.

No search service and no full-text library is used: postings are plain
dicts built with the standard library only.

A document is a mapping ``field name -> field text``.  A field whose key is
absent is *missing* for that document (distinct from "present but no query
match", which is a genuine raw score of 0).
"""

from __future__ import annotations

from dataclasses import dataclass
from typing import Iterator, Mapping, Optional

from .normalize import FieldStats
from .tokenizer import tokenize


@dataclass(frozen=True)
class Document:
    doc_id: str
    fields: Mapping[str, str]  # missing key == field absent for this doc


class Index:
    """Immutable postings index built from a fixed list of documents."""

    def __init__(self, documents: list[Document], fields: list[str]):
        if not fields:
            raise ValueError("Index needs at least one declared field")
        self.fields: tuple[str, ...] = tuple(fields)
        self._field_docs: dict[str, set[str]] = {f: set() for f in self.fields}
        # field -> term -> {doc_id: term frequency}
        self._postings: dict[str, dict[str, dict[str, int]]] = {
            f: {} for f in self.fields
        }
        seen: set[str] = set()
        for doc in documents:
            if doc.doc_id in seen:
                raise ValueError(f"duplicate doc_id: {doc.doc_id!r}")
            seen.add(doc.doc_id)
            unknown = set(doc.fields) - set(self.fields)
            if unknown:
                raise ValueError(f"doc {doc.doc_id!r} has unknown fields: {unknown}")
            for field, text in doc.fields.items():
                tokens = tokenize(text)
                self._field_docs[field].add(doc.doc_id)
                postings = self._postings[field]
                for token in tokens:
                    bucket = postings.setdefault(token, {})
                    bucket[doc.doc_id] = bucket.get(doc.doc_id, 0) + 1

    def field_docs(self, field: str) -> frozenset[str]:
        """Ids of documents that *have* the field (regardless of any query)."""
        return frozenset(self._field_docs[field])

    def postings(self, field: str, term: str) -> Mapping[str, int]:
        return self._postings[field].get(term, {})


class FieldQueryView:
    """Per-(index, field, query) access object.

    * ``stats`` -- explicit min/max raw-score statistics computed once over
      ALL documents that have the field (never over the fetched candidates).
    * ``sorted_access()`` -- lazy stream of ``(doc_id, raw_score)`` ordered by
      raw score descending, ties by doc_id ascending.  Only documents with a
      positive raw score appear; the caller pulls on demand.
    * ``random_access(doc_id)`` -- exact raw score, or ``None`` when the
      document does not have the field at all.
    """

    def __init__(self, index: Index, field: str, query_terms: list[str]):
        self.field = field
        raw: dict[str, float] = {}
        for term in set(query_terms):
            for doc_id, tf in index.postings(field, term).items():
                raw[doc_id] = raw.get(doc_id, 0.0) + float(tf)
        self._raw = raw
        self._has = index.field_docs(field)
        values = [raw.get(d, 0.0) for d in self._has]
        if values:
            self.stats = FieldStats(min(values), max(values))
        else:
            self.stats = FieldStats(0.0, 0.0)
        self._sorted: tuple[tuple[str, float], ...] = tuple(
            sorted(raw.items(), key=lambda kv: (-kv[1], kv[0]))
        )

    @property
    def has_field(self) -> frozenset[str]:
        return self._has

    def sorted_access(self) -> Iterator[tuple[str, float]]:
        yield from self._sorted

    def random_access(self, doc_id: str) -> Optional[float]:
        if doc_id not in self._has:
            return None  # field missing for this document
        return self._raw.get(doc_id, 0.0)
