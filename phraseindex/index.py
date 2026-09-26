"""Positional inverted index: segments, merge, retraction, snapshots.

Structure:
  * The index is a list of immutable segments plus a tombstone set of
    retracted document ids.
  * A segment holds positional postings
    ``term -> doc_id -> field -> sorted positions`` and a doc store with
    the tokens and the raw UTF-8 text of every field.
  * ``Index.delete_document`` adds a tombstone; postings and document
    frequencies derived from a snapshot immediately reflect the deletion
    (positions and df stay in sync because both are computed over live
    docs only). ``Index.merge`` physically drops tombstoned documents and
    compacts all segments into one.
  * ``Index.snapshot`` captures the current segments and tombstones into
    an immutable view. Searches and cursor iteration on a snapshot are
    stable no matter how the index is mutated afterwards.
"""
from __future__ import annotations

from dataclasses import dataclass
from typing import Dict, Iterator, List, Optional, Tuple

from .postings import BlockedPositions, DecodeCounter
from .query import PhraseQuery, match_field
from .tokenizer import Token, tokenize


@dataclass(frozen=True)
class Evidence:
    """One matched query element: which term, at which position/offsets."""

    element: int
    term: str
    position: int
    start: int
    end: int


@dataclass(frozen=True)
class Match:
    """A minimal covering range plus its positional evidence.

    ``start``/``end`` are code-point offsets into the original field text
    covering exactly the first and last matched token: the minimal
    covering range for this combination of positions.
    """

    doc_id: str
    field: str
    positions: Tuple[int, ...]
    start: int
    end: int
    text: str
    evidence: Tuple[Evidence, ...]


class _DocEntry:
    __slots__ = ("tokens", "texts")

    def __init__(self, tokens, texts):
        self.tokens = tokens
        self.texts = texts


class Segment:
    """Immutable segment: positional postings + doc store."""

    def __init__(self) -> None:
        self.postings: Dict[str, Dict[str, Dict[str, Tuple[int, ...]]]] = {}
        self.docs: Dict[str, _DocEntry] = {}

    @staticmethod
    def build(items: List[Tuple[str, Dict[str, str]]]) -> "Segment":
        seg = Segment()
        collected: Dict[str, Dict[str, Dict[str, List[int]]]] = {}
        for doc_id, fields in items:
            tokens_by_field: Dict[str, Tuple[Token, ...]] = {}
            for fname, text in fields.items():
                tokens = tuple(tokenize(text))
                tokens_by_field[fname] = tokens
                for pos, tok in enumerate(tokens):
                    collected.setdefault(tok.term, {}).setdefault(
                        doc_id, {}
                    ).setdefault(fname, []).append(pos)
            seg.docs[doc_id] = _DocEntry(tokens_by_field, dict(fields))
        seg.postings = {
            term: {
                doc_id: {fname: tuple(pos) for fname, pos in fields.items()}
                for doc_id, fields in docs.items()
            }
            for term, docs in collected.items()
        }
        return seg


class Snapshot:
    """Immutable point-in-time view; also serves as the cursor factory."""

    def __init__(self, segments: Tuple[Segment, ...], deleted: frozenset):
        self.segments = segments
        self.deleted = deleted

    def live_doc_ids(self) -> List[str]:
        ids = set()
        for seg in self.segments:
            ids.update(seg.docs)
        return sorted(ids - set(self.deleted))

    def df(self, term: str) -> int:
        """Document frequency over live documents only."""
        term = term.casefold()
        docs = set()
        for seg in self.segments:
            docs.update(seg.postings.get(term, ()))
        return len(docs - set(self.deleted))

    def iter_live_docs(self) -> Iterator[Tuple[str, _DocEntry]]:
        for seg in self.segments:
            for doc_id in sorted(seg.docs):
                if doc_id not in self.deleted:
                    yield doc_id, seg.docs[doc_id]

    def _candidate_docs(self, query: PhraseQuery) -> List[str]:
        per_element: List[set] = []
        for terms in query.elements:
            docs: set = set()
            for seg in self.segments:
                for term in terms:
                    docs.update(seg.postings.get(term, ()))
            per_element.append(docs - set(self.deleted))
        return sorted(set.intersection(*per_element)) if per_element else []

    def search(
        self,
        query: PhraseQuery,
        counter: Optional[DecodeCounter] = None,
        after_doc: Optional[str] = None,
    ) -> List[Match]:
        """Return all matches, ordered by (doc_id, field, positions).

        ``after_doc`` makes this a resumable cursor: pass the last doc_id
        of the previous page to continue. The snapshot is immutable, so
        pages are always consistent with each other.
        """
        matches: List[Match] = []
        for doc_id in self._candidate_docs(query):
            if after_doc is not None and doc_id <= after_doc:
                continue
            for seg in self.segments:
                entry = seg.docs.get(doc_id)
                if entry is None:
                    continue
                for fname, tokens in entry.tokens.items():
                    elem_pos: List[BlockedPositions] = []
                    cache: Dict[frozenset, BlockedPositions] = {}
                    ok = True
                    for terms in query.elements:
                        bp = cache.get(terms)
                        if bp is None:
                            merged: List[int] = []
                            for term in terms:
                                merged.extend(
                                    seg.postings.get(term, {})
                                    .get(doc_id, {})
                                    .get(fname, ())
                                )
                            bp = BlockedPositions(merged)
                            cache[terms] = bp
                        if len(bp) == 0:
                            ok = False
                            break
                        elem_pos.append(bp)
                    if not ok:
                        continue
                    for combo in match_field(elem_pos, query.gaps, counter):
                        evidence = []
                        for ei, pos in enumerate(combo):
                            tok = tokens[pos]
                            evidence.append(
                                Evidence(ei, tok.term, pos, tok.start, tok.end)
                            )
                        ev = tuple(evidence)
                        matches.append(
                            Match(
                                doc_id=doc_id,
                                field=fname,
                                positions=combo,
                                start=ev[0].start,
                                end=ev[-1].end,
                                text=entry.texts[fname][ev[0].start : ev[-1].end],
                                evidence=ev,
                            )
                        )
        matches.sort(key=lambda m: (m.doc_id, m.field, m.positions))
        return matches

    def iter_matches(
        self, query: PhraseQuery, counter: Optional[DecodeCounter] = None
    ) -> Iterator[Match]:
        """Streaming cursor over the immutable snapshot."""
        yield from self.search(query, counter=counter)


class Index:
    """Mutable index: buffered segments + tombstones."""

    def __init__(self) -> None:
        self._segments: List[Segment] = []
        self._deleted: set = set()

    def add_documents(self, docs: List[Tuple[str, Dict[str, str]]]) -> None:
        """Add a batch of documents as one new segment.

        ``docs`` is a list of ``(doc_id, {field_name: utf8_text})``. Doc
        ids are expected to be unique across the index.
        """
        if docs:
            self._segments.append(Segment.build(docs))

    def add_document(self, doc_id: str, fields: Dict[str, str]) -> None:
        self.add_documents([(doc_id, fields)])

    def delete_document(self, doc_id: str) -> None:
        """Retract a document. Positions and df update in sync because
        both are derived from live (non-tombstoned) docs only."""
        self._deleted.add(doc_id)

    def merge(self) -> None:
        """Compact all segments into one, physically dropping retracted
        documents and rebuilding postings from the stored raw text."""
        live: Dict[str, Dict[str, str]] = {}
        for seg in self._segments:
            for doc_id, entry in seg.docs.items():
                if doc_id not in self._deleted:
                    live[doc_id] = entry.texts
        self._segments = [Segment.build(sorted(live.items()))] if live else []
        self._deleted.clear()

    def snapshot(self) -> Snapshot:
        return Snapshot(tuple(self._segments), frozenset(self._deleted))

    def search(
        self,
        query: PhraseQuery,
        counter: Optional[DecodeCounter] = None,
        after_doc: Optional[str] = None,
    ) -> List[Match]:
        return self.snapshot().search(query, counter=counter, after_doc=after_doc)

    def df(self, term: str) -> int:
        return self.snapshot().df(term)

    @property
    def num_segments(self) -> int:
        return len(self._segments)
