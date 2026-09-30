"""Document tree scanner.

Walks nested dict/list structures (arrays of any depth are traversed, and
array elements inherit the key name of their nearest ancestor field),
applies dictionary rules to string leaves, enforces the whitelist and the
scan budget, and produces a redacted report.
"""
from __future__ import annotations

from dataclasses import dataclass

from .normalize import NORM_PATHS, normalize_entry
from .report import Evidence, Report, fingerprint, mask_value
from .stream import StreamScanner


@dataclass(frozen=True)
class Rule:
    """A named, versioned dictionary rule.

    ``fields`` lists leaf key names the rule applies to, or ``("*",)`` for
    every string value.  ``entries`` are the canonical sensitive values
    (synthetic in this project).
    """

    rule_id: str
    version: str
    fields: tuple
    entries: tuple


@dataclass(frozen=True)
class WhitelistEntry:
    """Exemption bound to an exact JSON pointer AND an exact rule version.

    A field with the same key name at any other path is not exempted, and
    a rule whose version differs from ``version`` is not exempted either.
    """

    path: str
    rule_id: str
    version: str


@dataclass(frozen=True)
class Budget:
    """Scan budget.  When any limit is exhausted the scan stops and the
    report is explicitly marked incomplete with the reason."""

    max_strings: int | None = None
    max_chars: int | None = None
    max_evidence: int | None = None


def _escape_pointer(part: str) -> str:
    return part.replace("~", "~0").replace("/", "~1")


def _iter_strings(node, path: str = "", key: str | None = None):
    if isinstance(node, dict):
        for k, v in node.items():
            yield from _iter_strings(v, path + "/" + _escape_pointer(str(k)), str(k))
    elif isinstance(node, list):
        for i, v in enumerate(node):
            yield from _iter_strings(v, f"{path}/{i}", key)
    elif isinstance(node, str):
        yield path, key, node


def _byte_offsets(text: str) -> list[int]:
    offsets = [0]
    total = 0
    for ch in text:
        total += len(ch.encode("utf-8"))
        offsets.append(total)
    return offsets


def scan_document(
    doc,
    rules: list[Rule],
    whitelist: tuple = (),
    budget: Budget | None = None,
    chunk_size: int = 64,
) -> Report:
    """Scan ``doc`` and return a redacted :class:`Report`.

    ``chunk_size`` controls the streaming granularity used to feed each
    string value to the matcher; results are identical for any chunk size.
    Note: the evidence budget caps raw matches per string (including ones
    later suppressed by the whitelist).
    """
    budget = budget or Budget()
    suppressed = {(w.path, w.rule_id, w.version) for w in whitelist}
    report = Report()

    for path, key, value in _iter_strings(doc):
        if budget.max_strings is not None and report.strings_scanned >= budget.max_strings:
            report.mark_incomplete(f"string budget exhausted ({budget.max_strings})")
            break
        report.strings_scanned += 1
        active = [r for r in rules if "*" in r.fields or key in r.fields]
        if not active:
            continue
        if (
            budget.max_chars is not None
            and report.chars_scanned + len(value) > budget.max_chars
        ):
            report.mark_incomplete(f"char budget exhausted ({budget.max_chars})")
            break
        report.chars_scanned += len(value)

        patterns = []
        rule_of = {}
        for rule in active:
            for idx, entry in enumerate(rule.entries):
                pid = (rule.rule_id, idx)
                patterns.append(
                    (pid, {name: normalize_entry(name, entry) for name in NORM_PATHS})
                )
                rule_of[pid] = rule

        remaining = None
        if budget.max_evidence is not None:
            remaining = budget.max_evidence - len(report.evidence)
            if remaining <= 0:
                report.mark_incomplete(f"evidence budget exhausted ({budget.max_evidence})")
                break

        scanner = StreamScanner(patterns, max_matches=remaining)
        for i in range(0, len(value), chunk_size):
            scanner.feed(value[i : i + chunk_size])
        scanner.finalize()
        if scanner.overflow:
            report.mark_incomplete(f"evidence budget exhausted ({budget.max_evidence})")

        offsets = _byte_offsets(value)
        for pid, start, end, via in scanner.matches:
            rule = rule_of[pid]
            if (path, rule.rule_id, rule.version) in suppressed:
                report.suppressed += 1
                continue
            raw = value[start:end]
            report.evidence.append(
                Evidence(
                    path=path,
                    rule_id=rule.rule_id,
                    rule_version=rule.version,
                    matched_via=via,
                    byte_start=offsets[start],
                    byte_end=offsets[end],
                    value_sha256_12=fingerprint(raw),
                    value_length=len(raw),
                    value_masked=mask_value(raw),
                )
            )
        if not report.complete:
            break
    return report
