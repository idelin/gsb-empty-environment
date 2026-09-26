"""Serializable normalization plan.

A plan is a plain-data object: it round-trips through JSON, carries the
digest of the input it was built from, declares its latency and numerical
tolerances, and records every conflict/residual so a caller can never
mistake a partially satisfied plan for a fully normalized one.
"""

from __future__ import annotations

import json
from dataclasses import dataclass, field
from typing import Any, Dict, List

from .constants import (
    GAIN_TOLERANCE_DB,
    PEAK_TOLERANCE,
    PLAN_FORMAT_VERSION,
    SILENCE_RMS_THRESHOLD,
    processing_latency_samples,
)
from .curve import CurveSegment, GainCurve

STATUS_NORMALIZED = "normalized"
STATUS_PARTIAL = "partial"


@dataclass
class Plan:
    sample_rate: int
    num_samples: int
    input_digest: str
    peak_ceiling: float
    max_gain_step: float
    fade_len: int
    # Each entry: {start, end, target_rms, gain, rms, peak, silent}
    segments: List[Dict[str, Any]]
    # Each entry: {kind, segment, detail, residual_db}
    conflicts: List[Dict[str, Any]] = field(default_factory=list)
    # Informational entries, e.g. crossfade peak repairs.
    notes: List[Dict[str, Any]] = field(default_factory=list)
    status: str = STATUS_NORMALIZED
    version: int = PLAN_FORMAT_VERSION
    latency_samples: int = 0
    latency_note: str = (
        "planning is offline and requires the full signal; applying a plan "
        "needs a lookahead of latency_samples around segment boundaries"
    )
    tolerances: Dict[str, float] = field(default_factory=lambda: {
        "gain_db": GAIN_TOLERANCE_DB,
        "peak_linear": PEAK_TOLERANCE,
        "silence_rms": SILENCE_RMS_THRESHOLD,
    })

    def gain_curve(self) -> GainCurve:
        return GainCurve(
            [CurveSegment(int(s["start"]), int(s["end"]), float(s["gain"]))
             for s in self.segments],
            fade_len=self.fade_len,
        )

    def to_dict(self) -> Dict[str, Any]:
        return {
            "version": self.version,
            "sample_rate": self.sample_rate,
            "num_samples": self.num_samples,
            "input_digest": self.input_digest,
            "peak_ceiling": self.peak_ceiling,
            "max_gain_step": self.max_gain_step,
            "fade_len": self.fade_len,
            "segments": [dict(s) for s in self.segments],
            "conflicts": [dict(c) for c in self.conflicts],
            "notes": [dict(n) for n in self.notes],
            "status": self.status,
            "latency_samples": self.latency_samples,
            "latency_note": self.latency_note,
            "tolerances": dict(self.tolerances),
        }

    @classmethod
    def from_dict(cls, data: Dict[str, Any]) -> "Plan":
        if int(data["version"]) != PLAN_FORMAT_VERSION:
            raise ValueError(
                "unsupported plan format version: %r" % (data["version"],))
        return cls(
            sample_rate=int(data["sample_rate"]),
            num_samples=int(data["num_samples"]),
            input_digest=str(data["input_digest"]),
            peak_ceiling=float(data["peak_ceiling"]),
            max_gain_step=float(data["max_gain_step"]),
            fade_len=int(data["fade_len"]),
            segments=[dict(s) for s in data["segments"]],
            conflicts=[dict(c) for c in data.get("conflicts", [])],
            notes=[dict(n) for n in data.get("notes", [])],
            status=str(data["status"]),
            latency_samples=int(data.get("latency_samples", 0)),
            latency_note=str(data.get("latency_note", "")),
            tolerances=dict(data.get("tolerances", {})),
        )

    def to_json(self, **json_kwargs: Any) -> str:
        return json.dumps(self.to_dict(), **json_kwargs)

    @classmethod
    def from_json(cls, text: str) -> "Plan":
        return cls.from_dict(json.loads(text))

    @property
    def is_normalized(self) -> bool:
        """True only when every target was met within tolerance."""
        return self.status == STATUS_NORMALIZED and not self.conflicts


def make_latency_samples(fade_len: int) -> int:
    return processing_latency_samples(fade_len)
