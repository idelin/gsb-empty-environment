"""Unicode normalization pipeline with composable many-to-many links.

Pipeline (applied identically to indexed documents and queries):

1. Fragment the original text on maximal runs of ``\\s`` whitespace.
   Each fragment remembers its original code-point span.
2. Inside each fragment, run four character-stream stages; every stage
   emits a list of *refs* (output position -> input position), so stages
   compose by ref indexing, never by "add a length delta":

   a. delete characters in category ``Cc`` (except whitespace) and all
      ``Cf`` format characters (this includes U+00AD SOFT HYPHEN);
   b. NFKD-decompose each surviving character (compatibility/canonical
      decomposition, e.g. ``\u00e9`` -> ``e\u0301``, ``\ufb03`` ->
      ``ffi``, ``\u00a0`` -> `` ``);
   c. apply Unicode canonical ordering (swap combining marks by
      canonical-combining-class, which may cross character boundaries);
   d. ``str.casefold`` each character (case folding; may expand, e.g.
      ``\u00df`` -> ``ss``, ``\u0130`` -> ``i\u0307``).
3. Join normalized fragments with one synthetic SPACE. The synthetic
   space is an *inserted* character whose link points at no original
   code point.

Because one original code point can fan out to several normalized code
points (decomposition / case expansion) and deleted code points simply
vanish while spans still cover them, the resulting relation between
normalized ranges and original ranges is a genuine many-to-many
relation, represented by :class:`Link` spans and composed across
stages.
"""

from __future__ import annotations

import re
import unicodedata
from dataclasses import dataclass


@dataclass(frozen=True)
class Link:
    """Half-open span ``[start, end)`` of original code points.

    ``start is None`` marks a character inserted by the pipeline itself
    (currently: the synthetic space between normalized fragments).
    """

    start: int | None
    end: int | None

    @classmethod
    def inserted(cls) -> "Link":
        return cls(None, None)

    @property
    def inserted(self) -> bool:
        return self.start is None


_FRAGMENT_RE = re.compile(r"\S+")


def is_deleted_char(ch: str) -> bool:
    """Rule for characters removed by stage 2a."""
    category = unicodedata.category(ch)
    if category == "Cc":
        # Keep whitespace controls (\t \n \r ...): the fragmentizer
        # consumes them as separators anyway.
        return not ch.isspace()
    if category == "Cf":
        # All format/zero-width chars are removed, incl. U+00AD,
        # U+200B, U+200C, U+200D.
        return True
    return False


def _stage_delete(chars, refs):
    out_chars: list[str] = []
    out_refs: list[int] = []
    for ch, ref in zip(chars, refs):
        if not is_deleted_char(ch):
            out_chars.append(ch)
            out_refs.append(ref)
    return out_chars, out_refs


def _stage_nfkd(chars, refs):
    out_chars: list[str] = []
    out_refs: list[int] = []
    for ch, ref in zip(chars, refs):
        for piece in unicodedata.normalize("NFKD", ch):
            out_chars.append(piece)
            out_refs.append(ref)
    return out_chars, out_refs


def _canonical_order(chars, refs) -> None:
    """Stable canonical ordering in place (UAX #15 algorithm)."""
    n = len(chars)
    i = 0
    while i < n:
        if unicodedata.combining(chars[i]) == 0:
            i += 1
            continue
        j = i + 1
        while j < n and unicodedata.combining(chars[j]) != 0:
            k = j
            while k > i and unicodedata.combining(chars[k - 1]) > unicodedata.combining(
                chars[k]
            ):
                chars[k - 1], chars[k] = chars[k], chars[k - 1]
                refs[k - 1], refs[k] = refs[k], refs[k - 1]
                k -= 1
            j += 1
        i = j


def _stage_casefold(chars, refs):
    out_chars: list[str] = []
    out_refs: list[int] = []
    for ch, ref in zip(chars, refs):
        for piece in ch.casefold():
            out_chars.append(piece)
            out_refs.append(ref)
    return out_chars, out_refs


def _normalize_fragment(fragment: str, base: int):
    """Normalize one fragment; refs resolve to absolute original cp."""
    chars: list[str] = list(fragment)
    refs: list[int] = list(range(len(fragment)))
    chars, refs = _stage_delete(chars, refs)
    chars, refs = _stage_nfkd(chars, refs)
    _canonical_order(chars, refs)
    chars, refs = _stage_casefold(chars, refs)
    links = tuple(Link(base + ref, base + ref + 1) for ref in refs)
    return "".join(chars), links


def normalize_document(text: str) -> tuple[str, "Alignment"]:
    """Return ``(normalized, alignment)`` for a document or query."""
    parts: list[str] = []
    links: list[Link] = []
    for match in _FRAGMENT_RE.finditer(text):
        normalized, fragment_links = _normalize_fragment(match.group(0), match.start())
        if not normalized:
            continue
        if parts:
            parts.append(" ")
            links.append(Link.inserted())
        parts.append(normalized)
        links.extend(fragment_links)
    return "".join(parts), Alignment(tuple(links), len(text))


def is_token_char(ch: str) -> bool:
    """Token character: Unicode letter (L*), mark (M*) or number (N*).

    Underscore, punctuation, symbols and separators are not token
    characters. Tokens are maximal runs of token characters.
    """
    return unicodedata.category(ch)[0] in ("L", "M", "N")


def token_spans(normalized: str) -> list[tuple[int, int]]:
    spans: list[tuple[int, int]] = []
    i = 0
    n = len(normalized)
    while i < n:
        if not is_token_char(normalized[i]):
            i += 1
            continue
        start = i
        i += 1
        while i < n and is_token_char(normalized[i]):
            i += 1
        spans.append((start, i))
    return spans


def utf16_prefix(text: str) -> tuple[int, ...]:
    """``prefix[i]`` = UTF-16 code-unit offset of code point ``i``."""
    prefix = [0] * (len(text) + 1)
    total = 0
    for i, ch in enumerate(text):
        prefix[i] = total
        total += 2 if ord(ch) > 0xFFFF else 1
    prefix[len(text)] = total
    return tuple(prefix)


class Alignment:
    """Maps normalized code points back to original code-point spans."""

    def __init__(self, links: tuple[Link, ...], original_length: int):
        self.links = links
        self.original_length = original_length

    def __len__(self) -> int:
        return len(self.links)

    def link(self, index: int) -> Link:
        return self.links[index]

    def map_span(self, start: int, end: int) -> tuple[int, int]:
        """Map normalized half-open span to original code-point span.

        Inserted characters inside the span are ignored for the
        endpoints, so a cross-fragment range covers the original
        whitespace gap completely. A span containing only inserted
        characters cannot be mapped and raises ``ValueError``.
        """
        if not (0 <= start < end <= len(self.links)):
            raise ValueError(f"span out of range: [{start}, {end})")
        first = None
        last = None
        for link in self.links[start:end]:
            if link.inserted:
                continue
            if first is None:
                first = link
            last = link
        if first is None or last is None:
            raise ValueError("span contains no original characters")
        return first.start, last.end
