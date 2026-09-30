"""Escape decoding that preserves source spans.

Only synthetic test data is processed by this project.  The decoder
understands a small, well-defined set of escape encodings:

- ``\\uXXXX`` (including surrogate pairs) and ``\\xXX``
- percent-encoded UTF-8 bytes (``%40``)
- HTML numeric character references (``&#97;`` / ``&#x61;``)
- HTML named entities (``&amp;`` ...)

Every decoded character is annotated with the half-open character range
``(start, end)`` it occupies in the raw input, so matches found in decoded
text can be mapped back to the original text.
"""
from __future__ import annotations

import html.entities
import re

_U_RE = re.compile(r"\\u([0-9a-fA-F]{4})")
_X_RE = re.compile(r"\\x([0-9a-fA-F]{2})")
_PCT_RE = re.compile(r"(?:%[0-9a-fA-F]{2})+")
_NUMREF_RE = re.compile(r"&#(?:[xX]([0-9a-fA-F]{1,6})|([0-9]{1,7}));")
_ENTITY_RE = re.compile(r"&([a-zA-Z][a-zA-Z0-9]{1,31});")

#: Longest escape prefix the decoder may need to see at once.  The stream
#: scanner holds back this many raw characters at the chunk tail so escape
#: sequences split across chunk boundaries still decode correctly.
HOLD_BACK = 40


def decode_escapes(raw: str) -> tuple[str, list[tuple[int, int]]]:
    """Decode escapes in ``raw``.

    Returns ``(decoded_text, spans)`` where ``spans[i]`` is the half-open
    character range in ``raw`` that produced ``decoded_text[i]``.
    Incomplete or invalid escapes are copied through literally.
    """
    out: list[str] = []
    spans: list[tuple[int, int]] = []
    i = 0
    n = len(raw)

    def emit(text: str, start: int, end: int) -> None:
        for k, ch in enumerate(text):
            out.append(ch)
            spans.append((start, end) if k == 0 else (end, end))

    while i < n:
        m = _U_RE.match(raw, i)
        if m:
            cp = int(m.group(1), 16)
            end = m.end()
            if 0xD800 <= cp <= 0xDBFF:
                m2 = _U_RE.match(raw, end)
                if m2:
                    cp2 = int(m2.group(1), 16)
                    if 0xDC00 <= cp2 <= 0xDFFF:
                        cp = 0x10000 + ((cp - 0xD800) << 10) + (cp2 - 0xDC00)
                        end = m2.end()
            emit(chr(cp), i, end)
            i = end
            continue
        m = _X_RE.match(raw, i)
        if m:
            emit(chr(int(m.group(1), 16)), i, m.end())
            i = m.end()
            continue
        if raw[i] == "%":
            m = _PCT_RE.match(raw, i)
            if m:
                data = bytes(int(raw[j + 1 : j + 3], 16) for j in range(i, m.end(), 3))
                try:
                    emit(data.decode("utf-8"), i, m.end())
                    i = m.end()
                    continue
                except UnicodeDecodeError:
                    pass
        if raw[i] == "&":
            m = _NUMREF_RE.match(raw, i)
            if m:
                cp = int(m.group(1), 16) if m.group(1) else int(m.group(2), 10)
                try:
                    emit(chr(cp), i, m.end())
                    i = m.end()
                    continue
                except (ValueError, OverflowError):
                    pass
            m = _ENTITY_RE.match(raw, i)
            if m:
                repl = html.entities.html5.get(m.group(1) + ";")
                if repl is not None:
                    emit(repl, i, m.end())
                    i = m.end()
                    continue
        emit(raw[i], i, i + 1)
        i += 1
    return "".join(out), spans
