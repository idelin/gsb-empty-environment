"""Gap repair engine.

Modes are strictly separated and never conflated:

* ONLINE  - causal. Only confirmed samples *before* the gap may be used
  (autoregressive forecast). Two-sided interpolation is impossible
  online because it would leak future truth.
* OFFLINE - may use both sides of the gap (two-sided linear
  interpolation when both anchors exist, one-sided AR otherwise).

Repaired values are never fed back as training observations: every
segment is restored from the original confirmed mask only, which also
makes segment-by-segment restoration identical to a batch pass.
"""

import math
from dataclasses import dataclass, field
from enum import Enum

from .ar import fit_ar_degrading, forecast
from .config import RepairConfig
from .gaps import Gap, find_gaps

MASK_KNOWN = 0
MASK_REPAIRED = 1
MASK_UNKNOWN = 2


class Mode(Enum):
    ONLINE = "online"
    OFFLINE = "offline"


@dataclass
class GapRepair:
    gap: Gap
    method: str
    predictions: list
    stderr: list
    evidence: dict = field(default_factory=dict)


@dataclass
class RepairResult:
    samples: list
    mask: list
    repairs: list
    mode: Mode
    model_version: str
    sample_rate: float

    def counts(self) -> dict:
        return {
            "known": self.mask.count(MASK_KNOWN),
            "repaired": self.mask.count(MASK_REPAIRED),
            "unknown": self.mask.count(MASK_UNKNOWN),
        }


def _nearest_confirmed(confirmed, start, step):
    index = start
    while 0 <= index < len(confirmed):
        if confirmed[index]:
            return index
        index += step
    return None


def _confirmed_run(values, confirmed, start, step, limit):
    out = []
    index = start
    while 0 <= index < len(confirmed) and confirmed[index] and len(out) < limit:
        out.append(values[index])
        index += step
    return out


def _local_noise(values, confirmed, left_idx, right_idx, window):
    diffs = []
    for lo, hi in ((max(0, left_idx - window), left_idx),
                   (right_idx, min(len(values) - 1, right_idx + window))):
        run = [values[i] for i in range(lo, hi + 1) if confirmed[i]]
        diffs.extend(run[k + 1] - run[k] for k in range(len(run) - 1))
    if not diffs:
        return 0.0
    mean = sum(diffs) / len(diffs)
    var = sum((d - mean) ** 2 for d in diffs) / len(diffs)
    return math.sqrt(var / 2.0)


def _linear_repair(values, confirmed, gap, left_idx, right_idx, config):
    y0, y1 = values[left_idx], values[right_idx]
    span = right_idx - left_idx
    sigma = _local_noise(values, confirmed, left_idx, right_idx, config.window)
    preds, stderr = [], []
    for i in range(gap.start, gap.end):
        t = (i - left_idx) / span
        preds.append(y0 + (y1 - y0) * t)
        stderr.append(sigma * math.sqrt((1.0 - t) ** 2 + t ** 2))
    return GapRepair(gap, "linear", preds, stderr,
                     {"left_anchor": left_idx, "right_anchor": right_idx,
                      "noise_sigma": sigma})


def _ar_repair(values, confirmed, gap, config, backward):
    if backward:
        run = _confirmed_run(values, confirmed, gap.end, +1, config.window)
    else:
        run = _confirmed_run(values, confirmed, gap.start - 1, -1, config.window)
    series = list(reversed(run))
    if len(series) < config.min_history:
        return GapRepair(gap, "none", [], [],
                         {"reason": "insufficient_confirmed_history",
                          "available": len(series),
                          "required": config.min_history})
    fit = fit_ar_degrading(series, config.ar_order)
    preds, variances = forecast(fit, series, gap.length)
    if backward:
        preds = list(reversed(preds))
        variances = list(reversed(variances))
    return GapRepair(
        gap,
        "ar_backward" if backward else "ar",
        preds,
        [math.sqrt(v) for v in variances],
        {"ar_order": fit.order, "sigma2": fit.sigma2, "fit": fit.evidence,
         "direction": "backward" if backward else "forward"},
    )


def _apply_calibration(repair, calibration):
    if calibration is None or repair.method == "none":
        return
    family = "linear" if repair.method == "linear" else "ar"
    scale = calibration.scale_for(family, repair.gap.length)
    repair.stderr = [s * scale for s in repair.stderr]
    repair.evidence["calibration_scale"] = scale


def restore_segment(values, confirmed, gap, config: RepairConfig, mode: Mode,
                    calibration=None) -> GapRepair:
    """Restore one gap using only samples flagged as confirmed."""
    if gap.length > config.max_gap:
        return GapRepair(gap, "none", [], [],
                         {"reason": "gap_exceeds_threshold",
                          "threshold": config.max_gap})
    left_idx = _nearest_confirmed(confirmed, gap.start - 1, -1)
    right_idx = _nearest_confirmed(confirmed, gap.end, +1)
    if mode is Mode.OFFLINE and left_idx is not None and right_idx is not None:
        repair = _linear_repair(values, confirmed, gap, left_idx, right_idx,
                                config)
    elif left_idx is not None:
        repair = _ar_repair(values, confirmed, gap, config, backward=False)
    elif mode is Mode.OFFLINE and right_idx is not None:
        repair = _ar_repair(values, confirmed, gap, config, backward=True)
    else:
        repair = GapRepair(gap, "none", [], [],
                           {"reason": "no_confirmed_anchor"})
    _apply_calibration(repair, calibration)
    return repair


def repair(samples, sample_rate, config: RepairConfig, mode: Mode,
           model_version: str = "unversioned", calibration=None) -> RepairResult:
    values = [float(v) for v in samples]
    confirmed = [not math.isnan(v) for v in values]
    out = list(values)
    mask = [MASK_KNOWN if c else MASK_UNKNOWN for c in confirmed]
    repairs = []
    for gap in find_gaps(values):
        segment = restore_segment(values, confirmed, gap, config, mode,
                                  calibration)
        repairs.append(segment)
        if segment.method != "none":
            for k in range(gap.length):
                out[gap.start + k] = segment.predictions[k]
                mask[gap.start + k] = MASK_REPAIRED
    return RepairResult(out, mask, repairs, mode, model_version, sample_rate)
