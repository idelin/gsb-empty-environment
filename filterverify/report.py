"""Measurement records and the verification report."""
from __future__ import annotations

import json
from dataclasses import asdict, dataclass, field


@dataclass
class MeasurementRecord:
    name: str
    method: str
    sample_rate: float
    num_samples: int
    value: float | None
    tolerance: float | None
    resolution: float | None  # finite test resolution, reported separately
    passed: bool
    details: dict = field(default_factory=dict)

    def line(self) -> str:
        status = "PASS" if self.passed else "FAIL"
        val = "n/a" if self.value is None else f"{self.value:.6g}"
        tol = "" if self.tolerance is None else f" (tol {self.tolerance:.6g})"
        res = "" if self.resolution is None else f", resolution {self.resolution:.6g} Hz"
        return (
            f"{status} {self.name}: {val}{tol} "
            f"[method={self.method}, n={self.num_samples}, fs={self.sample_rate:g}{res}]"
        )


@dataclass
class VerificationReport:
    implementation: str
    declared_latency_samples: int
    measurements: list[MeasurementRecord] = field(default_factory=list)
    counterexamples: list = field(default_factory=list)
    full_band_claim: str = "not certified: measurements cover listed points only"
    notes: list[str] = field(default_factory=list)

    @property
    def passed(self) -> bool:
        return all(m.passed for m in self.measurements) and not self.counterexamples

    def to_text(self) -> str:
        lines = [
            f"Verification report: {self.implementation}",
            f"declared latency: {self.declared_latency_samples} samples",
            f"overall: {'PASS' if self.passed else 'FAIL'}",
        ]
        lines += [m.line() for m in self.measurements]
        for ce in self.counterexamples:
            lines.append(
                f"COUNTEREXAMPLE {ce.kind}: max_error={ce.max_error:.6g} "
                f"at index {ce.error_index} (tol {ce.tolerance:.6g}), "
                f"partition={ce.partition}, shift={ce.shift}, "
                f"replay=(seed={ce.seed}, n={ce.signal_length})"
            )
        lines.append(f"full-band claim: {self.full_band_claim}")
        lines += [f"note: {n}" for n in self.notes]
        return "\n".join(lines)

    def to_json(self) -> str:
        return json.dumps(asdict(self), indent=2, sort_keys=True, default=str)
