"""Core scanner: rules, whitelist, budget, evidence report.

Definitions
-----------
- DictRule: a versioned set of known identifiers matched against string
  values under one or more normalization pipelines.
- FieldRule: a versioned path pattern (``*`` matches exactly one path
  segment, including array indices); any string at a matching path is
  sensitive regardless of content.
- WhitelistEntry: exempts exactly one (path, rule_id, rule_version)
  triple. Same-named fields at other paths and other rule versions are
  NOT exempted.
- ScanBudget: hard limits; exceeding any limit aborts the scan and the
  report is explicitly marked ``incomplete``.
"""

from __future__ import annotations

import hashlib
import json
from dataclasses import dataclass, field
from typing import Any, Iterable, Iterator, Sequence

from .normalize import DEFAULT_PIPELINES, normalize

_DIGEST_LEN = 16  # hex chars of sha256 kept in the report


# ---------------------------------------------------------------- rules

@dataclass(frozen=True)
class DictRule:
    rule_id: str
    version: int
    identifiers: tuple[str, ...]
    pipelines: tuple[str, ...] = DEFAULT_PIPELINES
    boundary: bool = True  # require non-alnum/_ boundaries around a match


@dataclass(frozen=True)
class FieldRule:
    rule_id: str
    version: int
    path_pattern: str  # '/'-separated; '*' matches one segment


@dataclass(frozen=True)
class WhitelistEntry:
    path: str
    rule_id: str
    rule_version: int


@dataclass(frozen=True)
class ScanBudget:
    max_evidence: int | None = None
    max_chars: int | None = None


# ---------------------------------------------------------------- report

@dataclass
class Evidence:
    """One detection. The raw sensitive value is never stored, only a
    truncated digest, its length and a character-class shape."""

    path: str
    rule_id: str
    rule_version: int
    kind: str  # "dictionary" | "field"
    byte_range: tuple[int, int]  # UTF-8 byte offsets within the scanned string
    normalization_paths: list[str]
    value_length: int
    value_sha256: str
    value_shape: str

    def to_dict(self) -> dict:
        return {
            "path": self.path,
            "rule_id": self.rule_id,
            "rule_version": self.rule_version,
            "kind": self.kind,
            "byte_range": list(self.byte_range),
            "normalization_paths": list(self.normalization_paths),
            "value_summary": {
                "length": self.value_length,
                "sha256_16": self.value_sha256,
                "shape": self.value_shape,
            },
        }


@dataclass
class ScanReport:
    status: str = "complete"  # "complete" | "incomplete"
    incomplete_reason: str | None = None
    evidence: list[Evidence] = field(default_factory=list)
    stats: dict = field(default_factory=dict)

    def to_dict(self) -> dict:
        return {
            "status": self.status,
            "incomplete_reason": self.incomplete_reason,
            "stats": dict(self.stats),
            "evidence": [e.to_dict() for e in self.evidence],
        }

    def to_json(self, **kwargs: Any) -> str:
        return json.dumps(self.to_dict(), ensure_ascii=False, **kwargs)


def _shape(s: str) -> str:
    out = []
    for ch in s:
        if ch.isdigit():
            out.append("9")
        elif ch.isupper():
            out.append("A")
        elif ch.islower():
            out.append("a")
        elif ch.isalnum():
            out.append("?")
        else:
            out.append(ch)
    return "".join(out)


def _summarize(raw: str) -> tuple[int, str, str]:
    digest = hashlib.sha256(raw.encode("utf-8")).hexdigest()[:_DIGEST_LEN]
    return len(raw), digest, _shape(raw)


# ---------------------------------------------------------------- internals

class _BudgetExceeded(Exception):
    pass


def _utf8_offsets(text: str) -> list[int]:
    offsets = [0] * (len(text) + 1)
    total = 0
    for i, ch in enumerate(text):
        offsets[i] = total
        total += len(ch.encode("utf-8"))
    offsets[len(text)] = total
    return offsets


def _boundary_ok(text: str, start: int, end: int) -> bool:
    if start > 0 and (text[start - 1].isalnum() or text[start - 1] == "_"):
        return False
    if end < len(text) and (text[end].isalnum() or text[end] == "_"):
        return False
    return True


def _escape_seg(seg: str) -> str:
    return seg.replace("~", "~0").replace("/", "~1")


def _path_matches(pattern_segs: Sequence[str], path: str) -> bool:
    segs = [s for s in path.split("/") if s != ""]
    if len(segs) != len(pattern_segs):
        return False
    return all(p == "*" or p == s for p, s in zip(pattern_segs, segs))


class _ScanState:
    def __init__(self, scanner: "Scanner", report: ScanReport):
        self.scanner = scanner
        self.report = report
        self.evidence_index: dict[tuple, Evidence] = {}
        self.strings_scanned = 0
        self.chars_scanned = 0
        self.whitelisted = 0

    def add_chars(self, n: int) -> None:
        budget = self.scanner.budget
        if budget.max_chars is not None and self.chars_scanned + n > budget.max_chars:
            raise _BudgetExceeded(
                f"character budget exceeded (max_chars={budget.max_chars})"
            )
        self.chars_scanned += n
        self.strings_scanned += 1

    def stats(self) -> dict:
        return {
            "strings_scanned": self.strings_scanned,
            "chars_scanned": self.chars_scanned,
            "evidence_count": len(self.report.evidence),
            "whitelisted_count": self.whitelisted,
        }


# ---------------------------------------------------------------- scanner

class Scanner:
    def __init__(
        self,
        dict_rules: Iterable[DictRule] = (),
        field_rules: Iterable[FieldRule] = (),
        whitelist: Iterable[WhitelistEntry] = (),
        budget: ScanBudget | None = None,
    ):
        self.dict_rules = list(dict_rules)
        self.field_rules = list(field_rules)
        self.whitelist = {
            (e.path, e.rule_id, e.rule_version) for e in whitelist
        }
        self.budget = budget or ScanBudget()
        self._compiled = [self._compile_dict_rule(r) for r in self.dict_rules]
        self._field = [
            (r, tuple(s for s in r.path_pattern.split("/") if s != ""))
            for r in self.field_rules
        ]

    @staticmethod
    def _compile_dict_rule(rule: DictRule) -> dict:
        patterns: dict[str, set[str]] = {}
        for ident in rule.identifiers:
            for pipeline in rule.pipelines:
                norm, _ = normalize(ident, pipeline)
                if norm:
                    patterns.setdefault(pipeline, set()).add(norm)
        return {"rule": rule, "patterns": patterns}

    # -- tree scanning -------------------------------------------------

    def scan_tree(self, obj: Any) -> ScanReport:
        report = ScanReport()
        state = _ScanState(self, report)
        try:
            self._walk(obj, "", state)
        except _BudgetExceeded as exc:
            report.status = "incomplete"
            report.incomplete_reason = str(exc)
        report.stats = state.stats()
        return report

    def _walk(self, obj: Any, path: str, state: _ScanState) -> None:
        if isinstance(obj, dict):
            for key, value in obj.items():
                self._walk(value, f"{path}/{_escape_seg(str(key))}", state)
        elif isinstance(obj, (list, tuple)):
            for index, value in enumerate(obj):
                self._walk(value, f"{path}/{index}", state)
        elif isinstance(obj, str):
            self.scan_string(obj, path or "/", state)
        # numbers / booleans / None are not scanned (documented)

    # -- string scanning ------------------------------------------------

    def scan_string(
        self,
        text: str,
        path: str,
        state: _ScanState,
        *,
        byte_offset: int = 0,
        char_horizon: int | None = None,
        apply_field_rules: bool = True,
        count_chars: int | None = None,
    ) -> None:
        """Scan one string. ``char_horizon`` (streaming use) suppresses
        matches whose original end lies beyond it; ``byte_offset`` shifts
        reported byte ranges into stream coordinates."""
        state.add_chars(len(text) if count_chars is None else count_chars)
        offsets = _utf8_offsets(text)

        if apply_field_rules:
            for rule, segs in self._field:
                if _path_matches(segs, path):
                    self._emit(
                        state, path, rule, "field",
                        (byte_offset, byte_offset + offsets[len(text)]),
                        ("field",), text,
                    )

        for compiled in self._compiled:
            rule: DictRule = compiled["rule"]
            for pipeline, patterns in compiled["patterns"].items():
                norm, amap = normalize(text, pipeline)
                for pattern in patterns:
                    start = 0
                    while True:
                        idx = norm.find(pattern, start)
                        if idx < 0:
                            break
                        end = idx + len(pattern)
                        start = idx + 1
                        if rule.boundary and not _boundary_ok(norm, idx, end):
                            continue
                        char_start = amap[idx][0]
                        char_end = amap[end - 1][1]
                        if char_horizon is not None and char_end > char_horizon:
                            continue
                        byte_start = byte_offset + offsets[char_start]
                        byte_end = byte_offset + offsets[char_end]
                        self._emit(
                            state, path, rule, "dictionary",
                            (byte_start, byte_end),
                            (pipeline,), text[char_start:char_end],
                        )

    def _emit(
        self,
        state: _ScanState,
        path: str,
        rule: DictRule | FieldRule,
        kind: str,
        byte_range: tuple[int, int],
        pipelines: tuple[str, ...],
        raw: str,
    ) -> None:
        if (path, rule.rule_id, rule.version) in self.whitelist:
            state.whitelisted += 1
            return
        key = (path, rule.rule_id, kind, byte_range)
        existing = state.evidence_index.get(key)
        if existing is not None:
            for p in pipelines:
                if p not in existing.normalization_paths:
                    existing.normalization_paths.append(p)
            return
        budget = self.budget
        if (
            budget.max_evidence is not None
            and len(state.report.evidence) >= budget.max_evidence
        ):
            raise _BudgetExceeded(
                f"evidence budget exceeded (max_evidence={budget.max_evidence})"
            )
        length, digest, shape = _summarize(raw)
        evidence = Evidence(
            path=path,
            rule_id=rule.rule_id,
            rule_version=rule.version,
            kind=kind,
            byte_range=byte_range,
            normalization_paths=list(pipelines),
            value_length=length,
            value_sha256=digest,
            value_shape=shape,
        )
        state.report.evidence.append(evidence)
        state.evidence_index[key] = evidence

    # -- convenience -----------------------------------------------------

    def scan_text(self, text: str, path: str = "/<text>") -> ScanReport:
        report = ScanReport()
        state = _ScanState(self, report)
        try:
            self.scan_string(text, path, state)
        except _BudgetExceeded as exc:
            report.status = "incomplete"
            report.incomplete_reason = str(exc)
        report.stats = state.stats()
        return report
