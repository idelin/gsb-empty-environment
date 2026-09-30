"""Match evidence with redacted values.

Evidence never stores the raw matched value: only a truncated SHA-256
fingerprint, the value length and a masked preview are reported, so the
scan report itself cannot be used to reconstruct a sensitive value.
Diagnostics (incomplete reasons, summaries) likewise never embed raw
values.
"""
from __future__ import annotations

import hashlib
import json
from dataclasses import dataclass, field


def mask_value(value: str) -> str:
    """Mask a value, revealing at most the first and last character."""
    n = len(value)
    if n <= 4:
        return "*" * n
    return value[0] + "*" * (n - 2) + value[-1]


def fingerprint(value: str) -> str:
    return hashlib.sha256(value.encode("utf-8")).hexdigest()[:12]


@dataclass
class Evidence:
    path: str
    rule_id: str
    rule_version: str
    matched_via: list
    byte_start: int
    byte_end: int
    value_sha256_12: str
    value_length: int
    value_masked: str

    def to_dict(self) -> dict:
        return {
            "path": self.path,
            "rule_id": self.rule_id,
            "rule_version": self.rule_version,
            "matched_via": list(self.matched_via),
            "byte_range": [self.byte_start, self.byte_end],
            "value": {
                "sha256_12": self.value_sha256_12,
                "length": self.value_length,
                "masked": self.value_masked,
            },
        }


@dataclass
class Report:
    complete: bool = True
    incomplete_reasons: list = field(default_factory=list)
    evidence: list = field(default_factory=list)
    suppressed: int = 0
    strings_scanned: int = 0
    chars_scanned: int = 0

    def mark_incomplete(self, reason: str) -> None:
        self.complete = False
        if reason not in self.incomplete_reasons:
            self.incomplete_reasons.append(reason)

    def to_dict(self) -> dict:
        return {
            "complete": self.complete,
            "incomplete_reasons": list(self.incomplete_reasons),
            "summary": {
                "evidence_count": len(self.evidence),
                "suppressed_count": self.suppressed,
                "strings_scanned": self.strings_scanned,
                "chars_scanned": self.chars_scanned,
            },
            "evidence": [e.to_dict() for e in self.evidence],
        }

    def to_json(self) -> str:
        return json.dumps(self.to_dict(), ensure_ascii=False, indent=2)
