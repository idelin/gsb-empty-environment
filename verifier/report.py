"""Verification report: per-check results with methods and sample counts."""
from __future__ import annotations

import json
from dataclasses import dataclass, field, asdict


@dataclass
class CheckResult:
    name: str
    passed: bool
    method: str
    n_samples: int
    measured: dict = field(default_factory=dict)
    tolerances: dict = field(default_factory=dict)
    detail: str = ""

    def to_dict(self):
        return asdict(self)


@dataclass
class VerificationReport:
    spec: dict
    checks: list
    artifacts: list = field(default_factory=list)

    @property
    def overall_passed(self) -> bool:
        return all(c.passed for c in self.checks)

    def to_dict(self):
        return {
            "spec": self.spec,
            "overall_passed": self.overall_passed,
            "checks": [c.to_dict() for c in self.checks],
            "artifacts": list(self.artifacts),
        }

    def to_json(self, indent: int = 2) -> str:
        return json.dumps(self.to_dict(), indent=indent)

    def save(self, path):
        with open(path, "w") as fh:
            fh.write(self.to_json())

    def format_text(self) -> str:
        s = self.spec
        lines = [
            f"spec: fs={_fmt(s['sample_rate'])}Hz "
            f"passband={_fmt(s['passband'])}Hz +/-{_fmt(s['passband_ripple_db'])}dB "
            f"stopband={_fmt(s['stopband'])}Hz >= {_fmt(s['stopband_atten_db'])}dB",
            f"verdict: {'PASS' if self.overall_passed else 'FAIL'} "
            f"({sum(c.passed for c in self.checks)}/{len(self.checks)} checks)",
        ]
        for c in self.checks:
            mark = "PASS" if c.passed else "FAIL"
            lines.append(f"[{mark}] {c.name:<20} n={c.n_samples:<7} {_fmt_dict(c.measured)}")
            lines.append(f"       method: {c.method}")
            if c.tolerances:
                lines.append(f"       tolerances: {_fmt_dict(c.tolerances)}")
            if c.detail:
                lines.append(f"       {c.detail}")
        if self.artifacts:
            lines.append("artifacts: " + ", ".join(self.artifacts))
        return "\n".join(lines)


def _fmt(v) -> str:
    if isinstance(v, float):
        return f"{v:.4g}"
    if isinstance(v, (list, tuple)):
        return "[" + ", ".join(_fmt(x) for x in v) + "]"
    if isinstance(v, dict):
        return _fmt_dict(v)
    return str(v)


def _fmt_dict(d) -> str:
    return ", ".join(f"{k}={_fmt(v)}" for k, v in d.items())
