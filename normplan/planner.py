"""Offline volume-normalization planning (Python 3.11+, standard library only).

The planner solves for a per-segment gain curve that jointly respects:
  * a per-segment target RMS (dBFS),
  * a global output peak ceiling,
  * a maximum gain-change ratio between adjacent segments.

Silence is never amplified. Short segments and overlap/fade regions are
measured with their real energy. When the constraints cannot all be met the
result carries explicit conflicts and residuals; `normalized` stays False.
"""
from __future__ import annotations

import bisect
import hashlib
import json
import math
import struct
from array import array
from dataclasses import dataclass, field

SILENCE_RMS = 1e-5            # -100 dBFS: below this a segment is silence
RMS_TOLERANCE_DB = 0.1        # declared residual tolerance for RMS targets
PEAK_TOLERANCE = 1e-6         # declared relative tolerance for the peak ceiling
MAX_GAIN_DB = 60.0
MAX_GAIN = 10.0 ** (MAX_GAIN_DB / 20.0)
FIXUP_MARGIN = 1e-3
MAX_FIXUP_ITERS = 16
PLAN_VERSION = 1
FADE_SHAPES = ("linear", "equal_power")


def db_to_lin(db: float) -> float:
    return 10.0 ** (db / 20.0)


def lin_to_db(value: float) -> float:
    return 20.0 * math.log10(max(value, 1e-12))


def input_digest(samples, sample_rate: int) -> str:
    """Stable digest of the exact float32 sample stream plus format."""
    if isinstance(samples, array) and samples.typecode == "f":
        data = samples
    else:
        data = array("f", samples)
    h = hashlib.sha256()
    h.update(struct.pack("<QI", len(data), int(sample_rate)))
    h.update(data.tobytes())
    return h.hexdigest()


@dataclass
class Segment:
    start: int
    end: int
    target_rms_db: float


@dataclass
class PlannedSegment:
    start: int
    end: int
    target_rms_db: float
    gain: float
    rms: float
    peak: float
    silent: bool
    achieved_rms: float = 0.0
    residual_db: float = 0.0

    def to_dict(self) -> dict:
        return {
            "start": self.start,
            "end": self.end,
            "target_rms_db": self.target_rms_db,
            "gain": self.gain,
            "rms": self.rms,
            "peak": self.peak,
            "silent": self.silent,
            "achieved_rms": self.achieved_rms,
            "residual_db": self.residual_db,
        }

    @classmethod
    def from_dict(cls, d: dict) -> "PlannedSegment":
        return cls(
            start=int(d["start"]),
            end=int(d["end"]),
            target_rms_db=float(d["target_rms_db"]),
            gain=float(d["gain"]),
            rms=float(d["rms"]),
            peak=float(d["peak"]),
            silent=bool(d["silent"]),
            achieved_rms=float(d.get("achieved_rms", 0.0)),
            residual_db=float(d.get("residual_db", 0.0)),
        )


@dataclass
class Conflict:
    kind: str                 # rms_target | infeasible_pair | crossfade_peak
    segments: list
    cause: str                # peak_ceiling | gain_rate_limit | crossfade_peak
    residual_db: float
    detail: str

    def to_dict(self) -> dict:
        return {
            "kind": self.kind,
            "segments": list(self.segments),
            "cause": self.cause,
            "residual_db": self.residual_db,
            "detail": self.detail,
        }


@dataclass
class Plan:
    sample_rate: int
    num_samples: int
    input_digest: str
    peak_ceiling: float
    max_adjacent_ratio: float
    fade: str
    segments: list
    version: int = PLAN_VERSION
    _starts: list = field(default=None, init=False, repr=False, compare=False)

    def __post_init__(self):
        self._starts = [s.start for s in self.segments]

    def gain_at(self, n: int) -> float:
        """Effective gain at sample index n; a pure function of n."""
        segs = self.segments
        idx = bisect.bisect_right(self._starts, n) - 1
        if idx < 0:
            return 1.0
        cur = segs[idx]
        if n >= cur.end:
            return 1.0
        if idx > 0:
            prev = segs[idx - 1]
            if n < prev.end:
                span = prev.end - cur.start
                w = (n - cur.start) / span
                if self.fade == "equal_power":
                    return prev.gain * math.cos(w * math.pi / 2.0) + cur.gain * math.sin(w * math.pi / 2.0)
                return prev.gain * (1.0 - w) + cur.gain * w
        return cur.gain

    def to_dict(self) -> dict:
        return {
            "version": self.version,
            "sample_rate": self.sample_rate,
            "num_samples": self.num_samples,
            "input_digest": self.input_digest,
            "peak_ceiling": self.peak_ceiling,
            "max_adjacent_ratio": self.max_adjacent_ratio,
            "fade": self.fade,
            "segments": [s.to_dict() for s in self.segments],
        }

    def to_json(self) -> str:
        return json.dumps(self.to_dict(), indent=2, sort_keys=True)

    @classmethod
    def from_dict(cls, d: dict) -> "Plan":
        if int(d["version"]) != PLAN_VERSION:
            raise ValueError(f"unsupported plan version: {d['version']}")
        return cls(
            sample_rate=int(d["sample_rate"]),
            num_samples=int(d["num_samples"]),
            input_digest=str(d["input_digest"]),
            peak_ceiling=float(d["peak_ceiling"]),
            max_adjacent_ratio=float(d["max_adjacent_ratio"]),
            fade=str(d["fade"]),
            segments=[PlannedSegment.from_dict(s) for s in d["segments"]],
        )

    @classmethod
    def from_json(cls, text: str) -> "Plan":
        return cls.from_dict(json.loads(text))


@dataclass
class PlanResult:
    plan: Plan
    conflicts: list
    output_peak: float

    @property
    def normalized(self) -> bool:
        return not self.conflicts

    def to_dict(self) -> dict:
        return {
            "normalized": self.normalized,
            "output_peak": self.output_peak,
            "conflicts": [c.to_dict() for c in self.conflicts],
            "plan": self.plan.to_dict(),
        }


def _segment_stats(samples, seg: Segment):
    total = 0.0
    peak = 0.0
    for k in range(seg.start, seg.end):
        v = float(samples[k])
        total += v * v
        av = abs(v)
        if av > peak:
            peak = av
    count = seg.end - seg.start
    return math.sqrt(total / count), peak


def _validate_segments(segments, num_samples: int) -> None:
    if not segments:
        raise ValueError("at least one segment is required")
    for seg in segments:
        if not (0 <= seg.start < seg.end <= num_samples):
            raise ValueError(f"segment out of range: [{seg.start}, {seg.end})")
    for i in range(1, len(segments)):
        if segments[i].start <= segments[i - 1].start:
            raise ValueError("segment starts must be strictly increasing")
        if segments[i].end <= segments[i - 1].end:
            raise ValueError("nested segments are not supported")
        if i >= 2 and segments[i].start < segments[i - 2].end:
            raise ValueError("only adjacent segments may overlap")


def _smooth(gains: list, ratio: float) -> None:
    for i in range(1, len(gains)):
        if gains[i] > gains[i - 1] * ratio:
            gains[i] = gains[i - 1] * ratio
    for i in range(len(gains) - 2, -1, -1):
        if gains[i] > gains[i + 1] * ratio:
            gains[i] = gains[i + 1] * ratio


def _sync_gains(plan: Plan, gains: list) -> None:
    for seg, g in zip(plan.segments, gains):
        seg.gain = g


def _fixup_crossfade(samples, plan: Plan, gains: list, causes: list,
                     ceiling: float, ratio: float) -> None:
    segs = plan.segments
    for _ in range(MAX_FIXUP_ITERS):
        worst_v = ceiling
        worst_pair = None
        for i in range(1, len(segs)):
            prev, cur = segs[i - 1], segs[i]
            if cur.start >= prev.end:
                continue
            for n in range(cur.start, prev.end):
                v = abs(float(samples[n])) * plan.gain_at(n)
                if v > worst_v:
                    worst_v = v
                    worst_pair = i
        if worst_pair is None:
            return
        scale = ceiling * (1.0 - FIXUP_MARGIN) / worst_v
        for j in (worst_pair - 1, worst_pair):
            gains[j] *= scale
            if causes[j] != "silence":
                causes[j] = "crossfade_peak"
        _smooth(gains, ratio)
        _sync_gains(plan, gains)


def plan_gains(samples, sample_rate: int, segments, peak_ceiling: float = 1.0,
               max_adjacent_ratio: float = 2.0, fade: str = "linear") -> PlanResult:
    if fade not in FADE_SHAPES:
        raise ValueError(f"fade must be one of {FADE_SHAPES}")
    if max_adjacent_ratio < 1.0:
        raise ValueError("max_adjacent_ratio must be >= 1.0")
    num_samples = len(samples)
    _validate_segments(segments, num_samples)

    gains, desired, upper, causes = [], [], [], []
    rms_of, peak_of, silent_of = [], [], []
    for seg in segments:
        rms, peak = _segment_stats(samples, seg)
        rms_of.append(rms)
        peak_of.append(peak)
        silent = rms < SILENCE_RMS
        silent_of.append(silent)
        cap = min(peak_ceiling / peak, MAX_GAIN) if peak > 0.0 else MAX_GAIN
        upper.append(cap)
        want = 1.0 if silent else db_to_lin(seg.target_rms_db) / rms
        desired.append(want)
        if silent:
            gains.append(1.0)
            causes.append("silence")
        elif want > cap:
            gains.append(cap)
            causes.append("peak_ceiling")
        else:
            gains.append(want)
            causes.append("target")

    _smooth(gains, max_adjacent_ratio)
    for i in range(len(gains)):
        if causes[i] == "target" and gains[i] < desired[i] * (1.0 - 1e-12):
            causes[i] = "gain_rate_limit"

    plan = Plan(
        sample_rate=int(sample_rate),
        num_samples=num_samples,
        input_digest=input_digest(samples, sample_rate),
        peak_ceiling=float(peak_ceiling),
        max_adjacent_ratio=float(max_adjacent_ratio),
        fade=fade,
        segments=[
            PlannedSegment(
                start=seg.start, end=seg.end, target_rms_db=seg.target_rms_db,
                gain=gains[i], rms=rms_of[i], peak=peak_of[i], silent=silent_of[i],
            )
            for i, seg in enumerate(segments)
        ],
    )

    _fixup_crossfade(samples, plan, gains, causes, peak_ceiling, max_adjacent_ratio)
    _sync_gains(plan, gains)

    conflicts = []
    for i, ps in enumerate(plan.segments):
        total = 0.0
        for n in range(ps.start, ps.end):
            v = float(samples[n]) * plan.gain_at(n)
            total += v * v
        achieved = math.sqrt(total / (ps.end - ps.start))
        ps.achieved_rms = achieved
        if ps.silent:
            ps.residual_db = 0.0
            continue
        ps.residual_db = lin_to_db(achieved) - ps.target_rms_db
        if abs(ps.residual_db) > RMS_TOLERANCE_DB:
            conflicts.append(Conflict(
                kind="rms_target",
                segments=[i],
                cause=causes[i],
                residual_db=ps.residual_db,
                detail=(
                    f"segment {i}: achieved {lin_to_db(achieved):.2f} dBFS vs "
                    f"target {ps.target_rms_db:.2f} dBFS (limited by {causes[i]})"
                ),
            ))

    allowed_step_db = lin_to_db(max_adjacent_ratio)
    for i in range(len(plan.segments) - 1):
        a, b = plan.segments[i], plan.segments[i + 1]
        if a.silent or b.silent:
            continue
        diff_db = abs(b.target_rms_db - a.target_rms_db)
        if diff_db > allowed_step_db + RMS_TOLERANCE_DB and (
            abs(a.residual_db) > RMS_TOLERANCE_DB
            or abs(b.residual_db) > RMS_TOLERANCE_DB
        ):
            conflicts.append(Conflict(
                kind="infeasible_pair",
                segments=[i, i + 1],
                cause="gain_rate_limit",
                residual_db=diff_db - allowed_step_db,
                detail=(
                    f"segments {i},{i + 1}: targets differ by {diff_db:.2f} dB "
                    f"but max adjacent change is {allowed_step_db:.2f} dB; "
                    f"cannot satisfy both"
                ),
            ))

    output_peak = 0.0
    worst_n = -1
    for n in range(num_samples):
        v = abs(float(samples[n])) * plan.gain_at(n)
        if v > output_peak:
            output_peak = v
            worst_n = n
    if output_peak > peak_ceiling * (1.0 + PEAK_TOLERANCE):
        covering = [
            i for i, s in enumerate(plan.segments)
            if s.start <= worst_n < s.end
        ]
        conflicts.append(Conflict(
            kind="crossfade_peak",
            segments=covering,
            cause="peak_ceiling",
            residual_db=lin_to_db(output_peak / peak_ceiling),
            detail=(
                f"rendered peak {output_peak:.6f} at sample {worst_n} exceeds "
                f"ceiling {peak_ceiling:.6f} after fix-up"
            ),
        ))

    return PlanResult(plan=plan, conflicts=conflicts, output_peak=output_peak)
