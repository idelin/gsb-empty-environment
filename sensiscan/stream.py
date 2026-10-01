"""Streaming scanner: detects identifiers spanning chunk boundaries and
reports byte ranges in original stream coordinates.

A lookbehind window is retained so that a match whose start lies in an
already-processed region but whose end was not yet visible is still
found. The window is sized for escape expansion up to 12x plus slack;
longer expansions are outside the streaming guarantee (documented).
"""

from __future__ import annotations

from .core import (
    ScanReport,
    Scanner,
    _BudgetExceeded,
    _ScanState,
)

_LOOKBEHIND_SLACK = 128
_EXPANSION_FACTOR = 12


class StreamScanner:
    def __init__(self, scanner: Scanner, path: str = "/<stream>"):
        self.scanner = scanner
        self.path = path
        self._buf = ""
        self._byte_base = 0
        self._report = ScanReport()
        self._state = _ScanState(scanner, self._report)
        self._lookbehind = self._compute_lookbehind(scanner)
        self._aborted = False
        self._finished = False

    @staticmethod
    def _compute_lookbehind(scanner: Scanner) -> int:
        max_len = 0
        for compiled in scanner._compiled:
            for patterns in compiled["patterns"].values():
                for pattern in patterns:
                    max_len = max(max_len, len(pattern))
        return max_len * _EXPANSION_FACTOR + _LOOKBEHIND_SLACK

    def feed(self, chunk: str) -> None:
        if self._finished:
            raise RuntimeError("stream already finished")
        if self._aborted:
            return
        self._buf += chunk
        self._scan(final=False)

    def finish(self) -> ScanReport:
        if not self._finished:
            if not self._aborted:
                self._scan(final=True)
            self._report.stats = self._state.stats()
            self._finished = True
        return self._report

    def _scan(self, final: bool) -> None:
        if final:
            horizon = len(self._buf)
        else:
            horizon = len(self._buf) - self._lookbehind
            if horizon <= 0:
                return
        try:
            self.scanner.scan_string(
                self._buf,
                self.path,
                self._state,
                byte_offset=self._byte_base,
                char_horizon=horizon,
                apply_field_rules=False,
                count_chars=horizon,
            )
        except _BudgetExceeded as exc:
            self._report.status = "incomplete"
            self._report.incomplete_reason = str(exc)
            self._aborted = True
            return
        if horizon > 0:
            self._byte_base += len(self._buf[:horizon].encode("utf-8"))
            self._buf = self._buf[horizon:]
