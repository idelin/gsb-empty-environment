"""Versioned index segments with normalization-space substring search.

Segments are consecutive UTF-8 decoded chunks of one document version.
Segments may be split at arbitrary character boundaries (including inside a
word); normalized text is concatenated *without* separators so that a match
crossing a segment boundary keeps one complete range.
"""

from __future__ import annotations

import unicodedata

from dataclasses import dataclass, field

from .normalize import NormalizedText, normalize_with_map, utf16_len

Token = tuple[str, int, int]


class VersionMismatchError(ValueError):
    """Raised when segments of different document versions share one index."""


@dataclass(frozen=True)
class Segment:
    doc_version: str
    text: str
    base_utf16: int  # UTF-16 code-unit offset of this segment in the document


@dataclass
class Index:
    """Holds segments of exactly one ``doc_version``.

    An index is frozen to one version: adding a segment stamped with another
    version raises ``VersionMismatchError``. Old versions must get their own
    index and can never be mapped against a new original.
    """

    _version: str | None = field(default=None, init=False)
    _segments: list[Segment] = field(default_factory=list, init=False)
    _normalized: NormalizedText | None = field(default=None, init=False)

    @property
    def doc_version(self) -> str | None:
        return self._version

    def add_segment(self, doc_version: str, text: str) -> None:
        if self._version is None:
            self._version = doc_version
        elif doc_version != self._version:
            raise VersionMismatchError(
                f"index bound to version {self._version!r} cannot accept "
                f"segment of version {doc_version!r}"
            )
        base = sum(utf16_len(ch) for seg in self._segments for ch in seg.text)
        self._segments.append(Segment(doc_version, text, base))
        self._normalized = None

    def build(self) -> NormalizedText:
        """Concatenate all segments into one normalized view with global spans."""
        if self._normalized is None:
            chars: list[str] = []
            spans: list[tuple[int, int]] = []
            for segment in self._segments:
                norm = normalize_with_map(segment.text)
                chars.append(norm.normalized)
                for start, end in norm.source_spans:
                    spans.append((start + segment.base_utf16,
                                  end + segment.base_utf16))
            original = "".join(seg.text for seg in self._segments)
            self._normalized = NormalizedText(
                original, "".join(chars), tuple(spans)
            )
        return self._normalized

    def search(self, query: str) -> list[tuple[int, int]]:
        """Return all (overlapping) matches as global UTF-16 code-unit ranges."""
        view = self.build()
        needle = normalize_with_map(query).normalized
        if not needle:
            return []
        ranges: list[tuple[int, int]] = []
        pos = 0
        while True:
            hit = view.normalized.find(needle, pos)
            if hit == -1:
                break
            ranges.append(view.map_range(hit, hit + len(needle)))
            pos = hit + 1  # allow overlapping matches
        return ranges

    def iter_tokens(self) -> list[Token]:
        """Tokenize the normalized view.

        Rule: a token is a maximal run of normalized characters whose Unicode
        general category starts with ``L`` (letter) or ``N`` (number). Every
        other normalized character (space, punctuation, symbols) is a
        separator. Each token also carries its original UTF-16 source range.
        """
        view = self.build()
        tokens: list[Token] = []
        start = None
        for i, ch in enumerate(view.normalized):
            alnum = unicodedata.category(ch)[0] in ("L", "N")
            if alnum and start is None:
                start = i
            elif not alnum and start is not None:
                s, e = view.map_range(start, i)
                tokens.append((view.normalized[start:i], s, e))
                start = None
        if start is not None:
            s, e = view.map_range(start, len(view.normalized))
            tokens.append((view.normalized[start:], s, e))
        return tokens
