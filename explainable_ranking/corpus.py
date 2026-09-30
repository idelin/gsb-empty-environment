"""Versioned document corpus.

Every document carries an explicit ``version`` label and a SHA-256
content hash. Explanation trees record both, so a verifier can detect
a swapped or edited document without trusting the retriever.
"""
from __future__ import annotations

import hashlib
from dataclasses import dataclass


@dataclass(frozen=True)
class Document:
    doc_id: str
    text: str
    version: str

    @property
    def content_hash(self) -> str:
        return hashlib.sha256(self.text.encode("utf-8")).hexdigest()


class Corpus:
    """An immutable-by-convention set of documents (one snapshot)."""

    def __init__(self) -> None:
        self._docs: dict[str, Document] = {}

    def put(self, doc: Document) -> None:
        self._docs[doc.doc_id] = doc

    def get(self, doc_id: str) -> Document | None:
        return self._docs.get(doc_id)

    def ids(self) -> list[str]:
        return sorted(self._docs)

    def __contains__(self, doc_id: object) -> bool:
        return doc_id in self._docs

    def __len__(self) -> int:
        return len(self._docs)


class CorpusStore:
    """Keeps every corpus snapshot so historical explanations stay
    verifiable after the corpus evolves."""

    def __init__(self) -> None:
        self._versions: dict[str, Corpus] = {}

    def put(self, version: str, corpus: Corpus) -> None:
        self._versions[version] = corpus

    def get(self, version: str) -> Corpus | None:
        return self._versions.get(version)
