"""Streaming dictionary matcher with cross-chunk matching.

Raw text is fed in chunks.  A tail of ``HOLD_BACK`` characters is withheld
so escape sequences split across chunk boundaries still decode.  Matching
runs incrementally over each normalization path with an overlap of
``max_pattern - 1`` characters, so matches crossing chunk boundaries are
found exactly once.  Matches found through several normalization paths are
deduplicated by their raw source span and merged into one piece of
evidence.
"""
from __future__ import annotations

from .escapes import HOLD_BACK, decode_escapes
from .normalize import NORM_PATHS, normalize_text


class StreamScanner:
    """Incremental matcher over one text stream.

    ``patterns`` is a list of ``(pattern_id, {norm_path: normalized})``.
    ``max_matches`` caps the number of distinct matches; when the cap is
    hit, ``overflow`` is set and further matches are not recorded.
    """

    def __init__(self, patterns: list, max_matches: int | None = None):
        self._patterns = [
            (pid, {k: v for k, v in paths.items() if v}) for pid, paths in patterns
        ]
        self._max_pat = max(
            (len(v) for _, paths in self._patterns for v in paths.values()),
            default=0,
        )
        self._max_matches = max_matches
        self._pending = ""
        self._pending_base = 0
        self._chars = {name: [] for name in NORM_PATHS}
        self._spans = {name: [] for name in NORM_PATHS}
        self._checked = {name: 0 for name in NORM_PATHS}
        self._matches: dict = {}
        self.overflow = False

    def feed(self, chunk: str) -> None:
        data = self._pending + chunk
        base = self._pending_base
        if len(data) > HOLD_BACK:
            cut = len(data) - HOLD_BACK
            self._ingest(data[:cut], base)
            self._pending = data[cut:]
            self._pending_base = base + cut
        else:
            self._pending = data

    def finalize(self) -> None:
        self._ingest(self._pending, self._pending_base)
        self._pending = ""

    def _ingest(self, text: str, base: int) -> None:
        if not text:
            return
        decoded, spans = decode_escapes(text)
        spans = [(s + base, e + base) for s, e in spans]
        for name in NORM_PATHS:
            ntext, nspans = normalize_text(name, decoded, spans)
            self._chars[name].extend(ntext)
            self._spans[name].extend(nspans)
            self._scan(name)

    def _scan(self, name: str) -> None:
        buf = "".join(self._chars[name])
        spans = self._spans[name]
        start = max(0, self._checked[name] - self._max_pat + 1)
        for pid, paths in self._patterns:
            pat = paths.get(name)
            if not pat:
                continue
            idx = buf.find(pat, start)
            while idx != -1:
                end = idx + len(pat)
                key = (pid, spans[idx][0], spans[end - 1][1])
                if key in self._matches:
                    self._matches[key].add(name)
                elif (
                    self._max_matches is not None
                    and len(self._matches) >= self._max_matches
                ):
                    self.overflow = True
                else:
                    self._matches[key] = {name}
                idx = buf.find(pat, idx + 1)
        self._checked[name] = len(buf)

    @property
    def matches(self) -> list:
        """List of ``(pattern_id, raw_start, raw_end, matched_via)``."""
        return [
            (pid, start, end, sorted(paths))
            for (pid, start, end), paths in sorted(
                self._matches.items(), key=lambda kv: (kv[0][1], kv[0][2], str(kv[0][0]))
            )
        ]
