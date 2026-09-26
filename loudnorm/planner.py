"""Offline loudness normalization planner.

Jointly solves for a per-segment gain curve subject to:

* per-segment target RMS (desired gain = target / measured RMS),
* a global peak ceiling (per-segment upper bound = ceiling / peak),
* a maximum gain change between adjacent segments (rate limit).

Silence is never amplified. RMS/peak are always computed from the true
samples, including very short segments. The final peak check runs on the
*rendered* curve (crossfade regions included), because a crossfade between
a high-gain and a loud segment can peak above either segment alone. When
the constraints cannot all be met, the plan reports conflicts with dB
residuals instead of pretending to be normalized.
"""

from __future__ import annotations

import math
from dataclasses import dataclass
from typing import List, Optional, Sequence, Tuple

from .audio import input_digest, region_peak, region_rms
from .constants import (
    GAIN_TOLERANCE_DB,
    MAX_REPAIR_ITERATIONS,
    PEAK_TOLERANCE,
    SILENCE_RMS_THRESHOLD,
    processing_latency_samples,
)
from .curve import CurveSegment, GainCurve
from .plan import STATUS_NORMALIZED, STATUS_PARTIAL, Plan

_EPS = 1e-9


@dataclass
class SegmentSpec:
    """[start, end) sample range and its target RMS (linear, FS = 1.0)."""
    start: int
    end: int
    target_rms: float


def _enforce_rate_limit(gains: List[float], step: float) -> None:
    """Project ``gains`` onto |g[i+1] - g[i]| <= step, lowering only."""
    for i in range(1, len(gains)):
        if gains[i] > gains[i - 1] + step:
            gains[i] = gains[i - 1] + step
    for i in range(len(gains) - 2, -1, -1):
        if gains[i] > gains[i + 1] + step:
            gains[i] = gains[i + 1] + step


def _rendered_peak(samples: Sequence[float], curve: GainCurve,
                   ) -> Tuple[float, int]:
    """Peak of samples * curve over the whole signal, and its position."""
    best = 0.0
    best_pos = 0
    for n, x in enumerate(samples):
        v = abs(x) * curve.gain_at(n)
        if v > best:
            best = v
            best_pos = n
    return best, best_pos


def _validate_specs(specs: Sequence[SegmentSpec], num_samples: int) -> None:
    if not specs:
        raise ValueError("at least one segment is required")
    prev_end = 0
    for spec in specs:
        if spec.start != prev_end:
            raise ValueError(
                "segments must be contiguous and cover the whole signal "
                "(expected start %d, got %d)" % (prev_end, spec.start))
        if spec.end <= spec.start:
            raise ValueError("segment end must be greater than its start")
        if spec.target_rms < 0:
            raise ValueError("target RMS must be non-negative")
        prev_end = spec.end
    if prev_end != num_samples:
        raise ValueError(
            "segments must cover the whole signal (end %d != %d samples)"
            % (prev_end, num_samples))


def plan_normalization(
    samples: Sequence[float],
    sample_rate: int,
    specs: Sequence[SegmentSpec],
    *,
    peak_ceiling: float = 0.9,
    max_gain_step: float = 1.0,
    fade_len: int = 0,
) -> Plan:
    """Build a normalization plan; never raises for infeasible targets.

    Infeasible combinations (e.g. adjacent targets farther apart than
    ``max_gain_step`` allows, or targets that would breach the ceiling)
    produce a plan with ``status == "partial"`` and per-segment conflicts
    carrying dB residuals.
    """
    num_samples = len(samples)
    _validate_specs(specs, num_samples)
    if peak_ceiling <= 0:
        raise ValueError("peak ceiling must be positive")
    if max_gain_step < 0:
        raise ValueError("max gain step must be non-negative")

    rms: List[float] = []
    peak: List[float] = []
    silent: List[bool] = []
    desired: List[float] = []
    upper: List[float] = []
    for spec in specs:
        r = region_rms(samples, spec.start, spec.end)
        p = region_peak(samples, spec.start, spec.end)
        rms.append(r)
        peak.append(p)
        is_silent = r < SILENCE_RMS_THRESHOLD
        silent.append(is_silent)
        if is_silent:
            # Silence is left untouched: gain 1.0, never amplified.
            desired.append(1.0)
        else:
            desired.append(spec.target_rms / r)
        upper.append(math.inf if p == 0.0 else peak_ceiling / p)

    # Pointwise-maximal curve under both the target and the ceiling bound,
    # then projected onto the adjacent-segment rate limit.
    gains = [min(d, u) for d, u in zip(desired, upper)]
    _enforce_rate_limit(gains, max_gain_step)

    curve = GainCurve(
        [CurveSegment(s.start, s.end, g) for s, g in zip(specs, gains)],
        fade_len=fade_len,
    )

    # Final peak check on the *rendered* curve. Crossfade regions can peak
    # above either neighbouring segment alone, so this must not be a
    # per-segment check. Repair by locally lowering gains if needed.
    notes: List[dict] = []
    conflicts: List[dict] = []
    repairs = 0
    repaired_segments: set = set()
    for _ in range(MAX_REPAIR_ITERATIONS):
        out_peak, pos = _rendered_peak(samples, curve)
        if out_peak <= peak_ceiling + PEAK_TOLERANCE:
            break
        repairs += 1
        g_here = curve.gain_at(pos)
        x_here = abs(samples[pos])
        if g_here <= 0.0 or x_here <= 0.0:
            break
        factor = peak_ceiling / (x_here * g_here)
        for k in curve.influencing_segments(pos):
            gains[k] = max(0.0, gains[k] * factor)
            repaired_segments.add(k)
        _enforce_rate_limit(gains, max_gain_step)
        curve = GainCurve(
            [CurveSegment(s.start, s.end, g) for s, g in zip(specs, gains)],
            fade_len=fade_len,
        )
    else:
        out_peak, _ = _rendered_peak(samples, curve)

    if repairs:
        notes.append({
            "kind": "peak_repair",
            "detail": "gains lowered %d time(s) so the rendered peak "
                      "(crossfade regions included) respects the ceiling"
                      % repairs,
            "iterations": repairs,
        })

    out_peak, _ = _rendered_peak(samples, curve)
    if out_peak > peak_ceiling + PEAK_TOLERANCE:
        conflicts.append({
            "kind": "output_peak_exceeded",
            "segment": None,
            "detail": "rendered peak %.6f exceeds ceiling %.6f even after "
                      "repair" % (out_peak, peak_ceiling),
            "residual_db": 20.0 * math.log10(out_peak / peak_ceiling),
        })

    # Honest per-segment residuals against the requested targets.
    for i, spec in enumerate(specs):
        if silent[i] or spec.target_rms <= 0.0:
            continue
        achieved = gains[i] * rms[i]
        residual_db = (20.0 * math.log10(achieved / spec.target_rms)
                       if achieved > 0.0 else -300.0)
        if residual_db < -GAIN_TOLERANCE_DB:
            causes = []
            if (upper[i] < desired[i] - _EPS
                    and gains[i] >= upper[i] - _EPS * max(1.0, upper[i])):
                causes.append("peak_ceiling")
            if i > 0 and gains[i] >= gains[i - 1] + max_gain_step - _EPS:
                causes.append("gain_rate_limit_with_segment_%d" % (i - 1))
            if (i + 1 < len(gains)
                    and gains[i] >= gains[i + 1] + max_gain_step - _EPS):
                causes.append("gain_rate_limit_with_segment_%d" % (i + 1))
            if i in repaired_segments:
                causes.append("crossfade_peak_repair")
            if not causes:
                causes.append("unknown")
            conflicts.append({
                "kind": "target_not_reached",
                "segment": i,
                "detail": "achieved RMS %.6f is %.2f dB below target %.6f "
                          "(causes: %s)" % (achieved, -residual_db,
                                            spec.target_rms,
                                            ", ".join(causes)),
                "residual_db": residual_db,
                "causes": causes,
            })

    plan = Plan(
        sample_rate=sample_rate,
        num_samples=num_samples,
        input_digest=input_digest(samples, sample_rate),
        peak_ceiling=peak_ceiling,
        max_gain_step=max_gain_step,
        fade_len=fade_len,
        segments=[{
            "start": s.start,
            "end": s.end,
            "target_rms": s.target_rms,
            "gain": g,
            "rms": r,
            "peak": p,
            "silent": sil,
        } for s, g, r, p, sil in zip(specs, gains, rms, peak, silent)],
        conflicts=conflicts,
        notes=notes,
        status=STATUS_PARTIAL if conflicts else STATUS_NORMALIZED,
        latency_samples=processing_latency_samples(fade_len),
    )
    return plan
