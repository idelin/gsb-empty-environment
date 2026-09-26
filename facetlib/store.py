"""Document store with inverted/facet bitmap indexes.

Bitmaps are plain Python ``int`` values (bit *i* = internal position of
document *i*).  Ints are immutable, so every incremental update builds
*new* bitmap objects and a snapshot is just a shallow copy of the index
dictionaries -- old snapshots keep seeing the old bitmaps, which gives
historical snapshots for free.

Supported incremental operations: ``upsert`` (add or replace) and
``delete``.  Counts are always de-duplicated by document identity
because one document owns exactly one bit, and per-document duplicate
values are collapsed with ``set()`` at index time.
"""

from __future__ import annotations

from .model import Document
from .tokenizer import token_set


class Snapshot:
    """Immutable view of the store at one version."""

    def __init__(self, store: "DocumentStore") -> None:
        self.version: int = store._version
        self.token_index: dict[str, int] = dict(store._token_index)
        self.facet_index: dict[tuple[str, str], int] = dict(store._facet_index)
        self.missing_index: dict[str, int] = dict(store._missing_index)
        self.empty_index: dict[str, int] = dict(store._empty_index)
        self.known_dims: frozenset[str] = frozenset(store._known_dims)
        self.alive: int = store._alive
        self.docs: dict[str, Document] = dict(store._docs)
        self.pos_of: dict[str, int] = dict(store._pos_of)
        self.id_at: tuple[str, ...] = tuple(store._id_at)

    # -- helpers used by tests / demo (never used on the counting path) --
    def doc_ids_in(self, bitmap: int) -> list[str]:
        return [self.id_at[i] for i in range(len(self.id_at))
                if bitmap >> i & 1]

    def dimensions(self) -> list[str]:
        dims = {dim for dim, _ in self.facet_index}
        dims |= set(self.empty_index)
        return sorted(dims)

    def values_of(self, dimension: str) -> list[str]:
        return sorted(v for d, v in self.facet_index if d == dimension)


class DocumentStore:
    def __init__(self) -> None:
        self._docs: dict[str, Document] = {}
        self._pos_of: dict[str, int] = {}
        self._id_at: list[str] = []
        self._token_index: dict[str, int] = {}
        self._facet_index: dict[tuple[str, str], int] = {}
        self._missing_index: dict[str, int] = {}
        self._empty_index: dict[str, int] = {}
        self._known_dims: set[str] = set()
        self._alive = 0
        self._version = 0

    # ---------------- mutation (incremental) ----------------

    def upsert(self, doc: Document) -> None:
        for dim, values in doc.attributes.items():
            if not isinstance(dim, str) or not dim:
                raise ValueError(f"bad dimension name: {dim!r}")
            for v in values:
                if not isinstance(v, str) or not v:
                    raise ValueError(f"bad value in {dim!r}: {v!r}")
        if doc.doc_id in self._docs:
            self._remove_bits(self._docs[doc.doc_id])
            pos = self._pos_of[doc.doc_id]
        else:
            pos = len(self._id_at)
            self._pos_of[doc.doc_id] = pos
            self._id_at.append(doc.doc_id)
        self._docs[doc.doc_id] = doc
        self._add_bits(doc, pos)
        self._alive |= 1 << pos
        self._version += 1
        self._prune_dims()

    def delete(self, doc_id: str) -> None:
        doc = self._docs.pop(doc_id)
        self._remove_bits(doc)
        pos = self._pos_of.pop(doc_id)
        self._alive &= ~(1 << pos)
        self._version += 1
        self._prune_dims()

    def snapshot(self) -> Snapshot:
        return Snapshot(self)

    @property
    def version(self) -> int:
        return self._version

    # ---------------- bitmap maintenance ----------------

    def _add_bits(self, doc: Document, pos: int) -> None:
        bit = 1 << pos
        for tok in token_set(doc.text):
            self._token_index[tok] = self._token_index.get(tok, 0) | bit
        doc_dims = set(doc.attributes)
        # docs known so far that lack a dimension are "missing" it
        for dim in self._known_dims - doc_dims:
            self._missing_index[dim] = self._missing_index.get(dim, 0) | bit
        # a brand-new dimension is missing on every previously live doc
        for dim in doc_dims - self._known_dims:
            self._missing_index[dim] = self._alive
        self._known_dims |= doc_dims
        for dim, values in doc.attributes.items():
            if not values:
                self._empty_index[dim] = self._empty_index.get(dim, 0) | bit
            # set() deduplicates: same doc + same value sets the bit once
            for v in set(values):
                key = (dim, v)
                self._facet_index[key] = self._facet_index.get(key, 0) | bit

    def _remove_bits(self, doc: Document) -> None:
        bit = 1 << self._pos_of[doc.doc_id]
        mask = ~bit
        for tok in token_set(doc.text):
            self._token_index[tok] = self._token_index[tok] & mask
            if not self._token_index[tok]:
                del self._token_index[tok]
        for dim in self._known_dims - set(doc.attributes):
            if dim in self._missing_index:
                self._missing_index[dim] &= mask
                if not self._missing_index[dim]:
                    del self._missing_index[dim]
        for dim, values in doc.attributes.items():
            if not values:
                self._empty_index[dim] &= mask
                if not self._empty_index[dim]:
                    del self._empty_index[dim]
            for v in set(values):
                key = (dim, v)
                self._facet_index[key] &= mask
                if not self._facet_index[key]:
                    del self._facet_index[key]

    def _prune_dims(self) -> None:
        """Forget dimensions no live document carries anymore."""
        live_dims = {d for d, _ in self._facet_index} | set(self._empty_index)
        for dim in list(self._known_dims):
            if dim not in live_dims:
                self._known_dims.discard(dim)
                self._missing_index.pop(dim, None)
