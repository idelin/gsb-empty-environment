"""Three-valued verdicts and the versioned verdict log records."""

from __future__ import annotations

from dataclasses import dataclass
from enum import Enum


class VerdictValue(Enum):
    TRUE = "TRUE"
    FALSE = "FALSE"
    PENDING = "PENDING"


@dataclass(frozen=True)
class VerdictRecord:
    """One published verdict version.

    Old versions are never discarded: every obligation keeps its full
    ``history`` and the monitor keeps the global ``log``.  ``final`` is
    True only once the obligation's window is closed by a watermark or by
    termination; a non-final TRUE/FALSE may still be revised by late or
    retracted events.
    """

    version: int
    obligation: tuple  # (rule_name, (trigger event ids, ...))
    rule: str
    value: VerdictValue
    final: bool
    reason: str

    def to_dict(self) -> dict:
        return {
            "version": self.version,
            "obligation": [self.obligation[0], list(self.obligation[1])],
            "rule": self.rule,
            "value": self.value.value,
            "final": self.final,
            "reason": self.reason,
        }

    @staticmethod
    def from_dict(data: dict) -> "VerdictRecord":
        return VerdictRecord(
            version=data["version"],
            obligation=(data["obligation"][0], tuple(data["obligation"][1])),
            rule=data["rule"],
            value=VerdictValue(data["value"]),
            final=data["final"],
            reason=data["reason"],
        )
