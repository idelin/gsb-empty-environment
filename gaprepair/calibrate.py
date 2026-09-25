"""Confidence-range calibration via synthetic occlusion experiments.

Known samples are artificially hidden (occluded), repaired by the same
engine, and the prediction is compared against the withheld truth. The
RMS of the resulting z-scores becomes a scale factor that calibrates
the reported standard errors; empirical 95% coverage is reported as
evidence.
"""

import math
import random
from dataclasses import dataclass, field

from .config import RepairConfig
from .gaps import Gap
from .repair import Mode, restore_segment

_SCALE_MIN = 0.25
_SCALE_MAX = 4.0


@dataclass
class CalibrationResult:
    scales: dict
    details: dict
    mode: Mode

    def scale_for(self, family: str, length: int) -> float:
        return self.scales.get((family, length), 1.0)


def synthetic_occlusion_experiment(samples, config: RepairConfig, mode: Mode,
                                   gap_lengths=(2, 4, 6, 8),
                                   trials_per_length: int = 40,
                                   seed: int = 20260926) -> CalibrationResult:
    values = [float(v) for v in samples]
    confirmed = [not math.isnan(v) for v in values]
    rng = random.Random(seed)
    zscores = {}
    for length in gap_lengths:
        candidates = [s for s in range(0, len(values) - length + 1)
                      if all(confirmed[s:s + length])]
        rng.shuffle(candidates)
        trials = 0
        for s in candidates:
            if trials >= trials_per_length:
                break
            trial_values = values[:s] + [math.nan] * length + values[s + length:]
            trial_confirmed = (confirmed[:s] + [False] * length
                               + confirmed[s + length:])
            segment = restore_segment(trial_values, trial_confirmed,
                                      Gap(s, length), config, mode)
            if segment.method == "none":
                continue
            family = "linear" if segment.method == "linear" else "ar"
            bucket = zscores.setdefault((family, length), [])
            for k in range(length):
                if segment.stderr[k] > 0.0:
                    z = ((segment.predictions[k] - values[s + k])
                         / segment.stderr[k])
                    bucket.append(z)
            trials += 1
    scales, details = {}, {}
    for key, zs in zscores.items():
        n = len(zs)
        rms = math.sqrt(sum(z * z for z in zs) / n)
        coverage = sum(1 for z in zs if abs(z) <= 1.96) / n
        scale = min(max(rms, _SCALE_MIN), _SCALE_MAX)
        scales[key] = scale
        details[key] = {"n": n, "rms_z": rms, "coverage_95": coverage,
                        "scale": scale}
    return CalibrationResult(scales, details, mode)
