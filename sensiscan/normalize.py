"""Normalization pipelines with original-index mapping.

Every pipeline transforms an input string into a normalized form while
keeping, for each character of the normalized output, the half-open
character span ``(start, end)`` in the *original* string it derives from.
This lets matches found in normalized space be mapped back to original
byte ranges.
"""

from __future__ import annotations

import re
import unicodedata

SpanMap = list[tuple[int, int]]

#: Characters removed by the zero-width step.
ZERO_WIDTH_CHARS = frozenset(
    "​‌‍⁠﻿"  # ZWSP ZWNJ ZWJ WORD JOINER BOM
)

#: Upper bound on the character length of a single decodable escape run.
MAX_ESCAPE_RUN = 96

_NAMED_ENTITIES = {
    "&amp;": "&",
    "&lt;": "<",
    "&gt;": ">",
    "&quot;": '"',
    "&apos;": "'",
}

_ESCAPE_RE = re.compile(
    r"""\\u[0-9a-fA-F]{4}
      | \\x[0-9a-fA-F]{2}
      | (?:%[0-9a-fA-F]{2})+
      | &\#[xX][0-9a-fA-F]{1,6};
      | &\#[0-9]{1,7};
      | &(?:amp|lt|gt|quot|apos);
    """,
    re.VERBOSE,
)


def identity_map(text: str) -> SpanMap:
    return [(i, i + 1) for i in range(len(text))]


def _charwise(text: str, amap: SpanMap, func) -> tuple[str, SpanMap]:
    out: list[str] = []
    out_map: SpanMap = []
    for i, ch in enumerate(text):
        rep = func(ch)
        if rep:
            out.append(rep)
            out_map.extend([amap[i]] * len(rep))
    return "".join(out), out_map


def step_nfkc(text: str, amap: SpanMap) -> tuple[str, SpanMap]:
    """Per-character NFKC. Canonical reordering across character
    boundaries is not reproduced; adequate for identifier matching."""
    return _charwise(text, amap, lambda ch: unicodedata.normalize("NFKC", ch))


def step_casefold(text: str, amap: SpanMap) -> tuple[str, SpanMap]:
    return _charwise(text, amap, str.casefold)


def step_remove_zero_width(text: str, amap: SpanMap) -> tuple[str, SpanMap]:
    return _charwise(
        text, amap, lambda ch: "" if ch in ZERO_WIDTH_CHARS else ch
    )


def step_remove_whitespace(text: str, amap: SpanMap) -> tuple[str, SpanMap]:
    return _charwise(text, amap, lambda ch: "" if ch.isspace() else ch)


def _decode_escape(seq: str) -> str | None:
    try:
        if seq.startswith("\\u") or seq.startswith("\\x"):
            return chr(int(seq[2:], 16))
        if seq.startswith("%"):
            raw = bytes.fromhex(seq.replace("%", ""))
            return raw.decode("utf-8", errors="strict")
        if seq.startswith("&#"):
            body = seq[2:-1]
            if body[:1] in ("x", "X"):
                return chr(int(body[1:], 16))
            return chr(int(body, 10))
        return _NAMED_ENTITIES.get(seq)
    except (ValueError, UnicodeDecodeError, OverflowError):
        return None


def step_unescape(text: str, amap: SpanMap) -> tuple[str, SpanMap]:
    """Decode common escape forms: \\uXXXX, \\xXX, %XX UTF-8 runs,
    numeric/hex HTML entities and a small named-entity set.
    Undecodable sequences are kept literally."""
    out: list[str] = []
    out_map: SpanMap = []
    pos = 0
    for m in _ESCAPE_RE.finditer(text):
        for i in range(pos, m.start()):
            out.append(text[i])
            out_map.append(amap[i])
        seq = m.group(0)
        decoded = None if len(seq) > MAX_ESCAPE_RUN else _decode_escape(seq)
        if decoded is None:
            for i in range(m.start(), m.end()):
                out.append(text[i])
                out_map.append(amap[i])
        else:
            span = (amap[m.start()][0], amap[m.end() - 1][1])
            out.append(decoded)
            out_map.extend([span] * len(decoded))
        pos = m.end()
    for i in range(pos, len(text)):
        out.append(text[i])
        out_map.append(amap[i])
    return "".join(out), out_map


def _compose(*steps):
    def pipeline(text: str, amap: SpanMap) -> tuple[str, SpanMap]:
        for step in steps:
            text, amap = step(text, amap)
        return text, amap

    return pipeline


def _identity(text: str, amap: SpanMap) -> tuple[str, SpanMap]:
    return text, amap


#: Named normalization pipelines. Order of application is the order of
#: the composed steps.
PIPELINES = {
    "raw": _identity,
    "nfkc_casefold": _compose(step_nfkc, step_casefold),
    "compact": _compose(
        step_nfkc, step_casefold, step_remove_zero_width, step_remove_whitespace
    ),
    "unescaped_compact": _compose(
        step_unescape,
        step_nfkc,
        step_casefold,
        step_remove_zero_width,
        step_remove_whitespace,
    ),
}

DEFAULT_PIPELINES = tuple(PIPELINES)


def normalize(text: str, pipeline: str) -> tuple[str, SpanMap]:
    """Return ``(normalized_text, span_map)`` for the named pipeline."""
    try:
        func = PIPELINES[pipeline]
    except KeyError:
        raise ValueError(f"unknown normalization pipeline: {pipeline!r}") from None
    return func(text, identity_map(text))
