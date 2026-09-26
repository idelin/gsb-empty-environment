"""Append-only segmented index with version isolation.

Every ``upsert``/``delete`` creates a new immutable :class:`Segment`
that owns its own normalized text, alignment and fragment table. Search
uses *only* the latest segment per document; a hit discovered in an old
segment is skipped (its alignment must never be applied to a newer
original string -- they are different strings and would silently map to
the wrong ranges).

No search service or full-text library is used: search is a plain
``str.find`` scan over the normalized strings plus a whole-token
boundary check, implemented here.
"""

from __future__ import annotations

from dataclasses import dataclass

from .normalize import Alignment, is_token_char, normalize_document, token_spans


@dataclass(frozen=True)
class Hit:
    doc_id: str
    version: int
    norm_start: int
    norm_end: int
    orig_start: int  # original code-point offset
    orig_end: int
    utf16_start: int
    utf16_end: int
    matched_original: str  # raw, unescaped original slice
    cross_fragment: bool


@dataclass(frozen=True)
class _Frag:
    norm_start: int
    norm_end: int
    orig_start: int
    orig_end: int


@dataclass(frozen=True)
class Segment:
    doc_id: str
    version: int
    original: str
    normalized: str
    alignment: Alignment
    fragments: tuple[_Frag, ...]
    deleted: bool
    _utf16: tuple[int, ...]

    def fragment_of(self, norm_pos: int) -> _Frag | None:
        for fragment in self.fragments:
            if fragment.norm_start <= norm_pos < fragment.norm_end:
                return fragment
        return None


def _build_fragments(original: str, normalized: str, alignment: Alignment) -> tuple[_Frag, ...]:
    fragments: list[_Frag] = []
    i = 0
    n = len(normalized)
    while i < n:
        if alignment.link(i).inserted:
            i += 1
            continue
        start = i
        while i < n and not alignment.link(i).inserted:
            i += 1
        end = i
        orig_start = alignment.link(start).start
        orig_end = alignment.link(end - 1).end
        fragments.append(_Frag(start, end, orig_start, orig_end))
    return tuple(fragments)


def find_occurrences(normalized: str, needle: str) -> list[tuple[int, int]]:
    """All overlapping occurrences of ``needle`` (bounded whole tokens).

    The match must start at the first character of a token and end at
    the last character of a token, so ``cat`` does not match inside
    ``concatenate``. Occurrences may span a synthetic space (phrase /
    cross-fragment matches).
    """
    if not needle:
        return []
    occurrences: list[tuple[int, int]] = []
    start = 0
    while True:
        found = normalized.find(needle, start)
        if found < 0:
            return occurrences
        end = found + len(needle)
        before_ok = found == 0 or not is_token_char(normalized[found - 1])
        after_ok = end == len(normalized) or not is_token_char(normalized[end])
        # Needle endpoints are token chars themselves (queries contain
        # at least one token and tokens are space separated).
        if before_ok and after_ok:
            occurrences.append((found, end))
        start = found + 1


class Index:
    def __init__(self) -> None:
        self._current: dict[str, Segment] = {}
        self.segments: list[Segment] = []  # full history, append only

    def upsert(self, doc_id: str, text: str) -> Segment:
        version = self._current[doc_id].version + 1 if doc_id in self._current else 1
        segment = self._make_segment(doc_id, version, text, deleted=False)
        self.segments.append(segment)
        self._current[doc_id] = segment
        return segment

    def delete(self, doc_id: str) -> None:
        if doc_id not in self._current:
            return
        version = self._current[doc_id].version + 1
        normalized, alignment = normalize_document("")
        from .normalize import utf16_prefix

        tombstone = Segment(
            doc_id=doc_id,
            version=version,
            original="",
            normalized="",
            alignment=alignment,
            fragments=(),
            deleted=True,
            _utf16=utf16_prefix(""),
        )
        self.segments.append(tombstone)
        del self._current[doc_id]

    @staticmethod
    def _make_segment(doc_id: str, version: int, text: str, deleted: bool) -> Segment:
        from .normalize import utf16_prefix

        normalized, alignment = normalize_document(text)
        fragments = _build_fragments(text, normalized, alignment)
        return Segment(
            doc_id=doc_id,
            version=version,
            original=text,
            normalized=normalized,
            alignment=alignment,
            fragments=fragments,
            deleted=deleted,
            _utf16=utf16_prefix(text),
        )

    def current_segments(self) -> list[Segment]:
        return list(self._current.values())

    def old_segments(self) -> list[Segment]:
        """Superseded history (visible for tests, never used by search)."""
        return [segment for segment in self.segments if self._current.get(segment.doc_id) is not segment]

    def tokens(self, doc_id: str) -> list[str]:
        segment = self._current[doc_id]
        return [segment.normalized[a:b] for a, b in token_spans(segment.normalized)]

    def search(self, query: str) -> list[Hit]:
        normalized_query, query_alignment = normalize_document(query)
        if not normalized_query:
            return []
        # A valid phrase contains at least one token character.
        if not any(is_token_char(ch) for ch in normalized_query):
            return []
        hits: list[Hit] = []
        # Iterate ONLY current segments: old segments belong to older
        # document versions and their alignments cannot be mixed with
        # any other (newer) original text.
        for segment in self._current.values():
            for norm_start, norm_end in find_occurrences(segment.normalized, normalized_query):
                orig_start, orig_end = segment.alignment.map_span(norm_start, norm_end)
                frag_first = segment.fragment_of(norm_start)
                frag_last = segment.fragment_of(norm_end - 1)
                cross = frag_first is not frag_last
                hits.append(
                    Hit(
                        doc_id=segment.doc_id,
                        version=segment.version,
                        norm_start=norm_start,
                        norm_end=norm_end,
                        orig_start=orig_start,
                        orig_end=orig_end,
                        utf16_start=segment._utf16[orig_start],
                        utf16_end=segment._utf16[orig_end],
                        matched_original=segment.original[orig_start:orig_end],
                        cross_fragment=cross,
                    )
                )
        hits.sort(key=lambda hit: (hit.doc_id, hit.utf16_start))
        return hits
