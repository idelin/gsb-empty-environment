"""Repair of short missing segments in uniformly sampled signals.

Missing samples are NaN. Real zero values are valid observations and are
never treated as missing. Known (confirmed) samples are never modified by
the repair process, and repaired samples never become training observations
for later repairs.

The two modes are distinct and must not be conflated:

- ``online``: causal repair. The autoregressive (AR) model is fitted only
  on confirmed samples strictly before the gap; no future sample is used.
- ``offline``: non-causal repair. Confirmed samples on both sides of the
  gap may be used (two-sided linear interpolation).

Only the Python standard library is used.
"""

from __future__ import annotations

import math
from dataclasses import dataclass

# Declared numerical tolerance for determinism / equality checks.
NUMERICAL_TOLERANCE = 1e-9

MODE_ONLINE = "online"
MODE_OFFLINE = "offline"

# Declared latency model.
ONLINE_LATENCY = (
    "online repair is emitted once the first confirmed sample after the gap "
    "arrives; it never looks past gap closure"
)
OFFLINE_LATENCY = (
    "offline repair waits for both anchors of the gap and may use confirmed "
    "samples on both sides of it"
)

STATUS_REPAIRED = "repaired"
STATUS_DEGRADED = "degraded"
STATUS_LEFT_UNKNOWN = "left_unknown"

METHOD_AR = "ar"
METHOD_LINEAR = "linear"


def is_missing(value) -> bool:
    """Only NaN marks a missing sample; a real 0.0 is a valid observation."""
    return isinstance(value, float) and math.isnan(value)


def find_gaps(samples) -> list[tuple[int, int]]:
    """Return ``(start, length)`` for every run of missing samples."""
    gaps = []
    start = None
    for index, value in enumerate(samples):
        if is_missing(value):
            if start is None:
                start = index
        elif start is not None:
            gaps.append((start, index - start))
            start = None
    if start is not None:
        gaps.append((start, len(samples) - start))
    return gaps


@dataclass(frozen=True)
class RepairConfig:
    max_gap_len: int = 8
    mode: str = MODE_ONLINE
    ar_order: int = 4
    min_train_samples: int = 24
    max_condition_number: float = 1e10
    confidence_z: float = 1.96
    error_scale: float = 1.0
    model_version: str = "ar-leastsquares-v1"

    def __post_init__(self):
        if self.mode not in (MODE_ONLINE, MODE_OFFLINE):
            raise ValueError(
                f"unknown mode {self.mode!r}; use {MODE_ONLINE!r} or {MODE_OFFLINE!r}"
            )
        if self.max_gap_len < 1:
            raise ValueError("max_gap_len must be >= 1")
        if self.ar_order < 1:
            raise ValueError("ar_order must be >= 1")


@dataclass
class ARModel:
    order: int
    coefficients: tuple
    mean: float
    residual_std: float
    condition_estimate: float
    train_samples: int


@dataclass
class GapReport:
    start: int
    length: int
    status: str
    method: str | None
    error_estimates: tuple | None
    evidence: dict


@dataclass
class RepairResult:
    samples: list
    repair_mask: list
    error_estimates: list
    gaps: list
    config: RepairConfig


def _solve_lu(matrix, rhs):
    """Solve a dense linear system by Gaussian elimination with partial
    pivoting. Returns ``(solution, condition_estimate)``; the solution is
    ``None`` when the system is numerically singular. The condition
    estimate is the max/min ratio of the U diagonal (a cheap heuristic)."""
    n = len(matrix)
    aug = [list(matrix[i]) + [rhs[i]] for i in range(n)]
    max_pivot = 0.0
    min_pivot = math.inf
    for col in range(n):
        pivot_row = max(range(col, n), key=lambda r: abs(aug[r][col]))
        if abs(aug[pivot_row][col]) < 1e-12:
            return None, math.inf
        aug[col], aug[pivot_row] = aug[pivot_row], aug[col]
        pivot = aug[col][col]
        max_pivot = max(max_pivot, abs(pivot))
        min_pivot = min(min_pivot, abs(pivot))
        for row in range(col + 1, n):
            factor = aug[row][col] / pivot
            for c in range(col, n + 1):
                aug[row][c] -= factor * aug[col][c]
    solution = [0.0] * n
    for row in range(n - 1, -1, -1):
        acc = aug[row][n]
        for c in range(row + 1, n):
            acc -= aug[row][c] * solution[c]
        solution[row] = acc / aug[row][row]
    condition = max_pivot / min_pivot if min_pivot > 0 else math.inf
    return solution, condition


def fit_ar(confirmed, config: RepairConfig):
    """Fit an AR model by least squares on confirmed samples only.

    Returns ``(model, evidence)``; ``model`` is ``None`` when the fit is
    impossible or ill-conditioned, and ``evidence`` then carries the
    reason. Only samples supplied in ``confirmed`` are used, so callers
    must pass confirmed (never repaired) observations.
    """
    order = config.ar_order
    evidence = {"train_samples_available": len(confirmed), "ar_order": order}
    if len(confirmed) < max(config.min_train_samples, 2 * order + 2):
        evidence["reason"] = "insufficient confirmed samples for AR fit"
        return None, evidence
    mean = sum(confirmed) / len(confirmed)
    centered = [v - mean for v in confirmed]
    variance = sum(v * v for v in centered) / len(centered)
    evidence["train_variance"] = variance
    if variance < 1e-12:
        evidence["reason"] = "degenerate near-constant training segment"
        return None, evidence
    xtx = [[0.0] * order for _ in range(order)]
    xty = [0.0] * order
    for t in range(order, len(centered)):
        target = centered[t]
        for i in range(order):
            xi = centered[t - 1 - i]
            xty[i] += xi * target
            row = xtx[i]
            for j in range(order):
                row[j] += xi * centered[t - 1 - j]
    coefficients, condition = _solve_lu(xtx, xty)
    evidence["condition_estimate"] = condition
    if coefficients is None:
        evidence["reason"] = "singular normal equations"
        return None, evidence
    if condition > config.max_condition_number:
        evidence["reason"] = "ill-conditioned normal equations"
        return None, evidence
    ss = 0.0
    for t in range(order, len(centered)):
        pred = 0.0
        for i in range(order):
            pred += coefficients[i] * centered[t - 1 - i]
        residual = centered[t] - pred
        ss += residual * residual
    dof = max(1, len(centered) - 2 * order)
    model = ARModel(
        order=order,
        coefficients=tuple(coefficients),
        mean=mean,
        residual_std=math.sqrt(ss / dof),
        condition_estimate=condition,
        train_samples=len(confirmed),
    )
    return model, evidence


def predict_ar(model: ARModel, context, steps: int):
    """Recursive multi-step forecast.

    ``context`` holds the last ``model.order`` values before the gap (it may
    contain previously repaired values, which is deterministic; repaired
    values are never used for *training*). Per-step error estimates grow
    with the horizon via the AR impulse response.
    """
    order = model.order
    psi = [1.0]
    for k in range(1, steps):
        acc = 0.0
        for i in range(1, min(k, order) + 1):
            acc += model.coefficients[i - 1] * psi[k - i]
        psi.append(acc)
    cumulative = []
    acc = 0.0
    for v in psi:
        acc += v * v
        cumulative.append(acc)
    buffer = list(context)
    values = []
    errors = []
    for k in range(steps):
        pred = model.mean
        for i in range(order):
            pred += model.coefficients[i] * (buffer[-1 - i] - model.mean)
        values.append(pred)
        errors.append(model.residual_std * math.sqrt(cumulative[k]))
        buffer.append(pred)
    return values, errors


def _roughness(samples, confirmed, start, length):
    """Std of first differences of adjacent confirmed pairs near the gap."""
    diffs = []
    lo = max(0, start - 8)
    hi = min(len(samples), start + length + 8)
    for i in range(lo, hi - 1):
        if confirmed[i] and confirmed[i + 1]:
            diffs.append(samples[i + 1] - samples[i])
    if len(diffs) < 2:
        return 0.0
    mean = sum(diffs) / len(diffs)
    return math.sqrt(sum((d - mean) ** 2 for d in diffs) / (len(diffs) - 1))


def _linear_patch(samples, confirmed, start, length, config):
    left = samples[start - 1]
    right = samples[start + length]
    step = (right - left) / (length + 1)
    values = [left + step * (k + 1) for k in range(length)]
    rough = _roughness(samples, confirmed, start, length)
    errors = [
        config.error_scale * rough * math.sqrt(min(k + 1, length - k))
        for k in range(length)
    ]
    return values, errors, rough


def _repair_online(samples, confirmed, start, length, config, evidence,
                   has_left, has_right):
    train = [samples[i] for i in range(start) if confirmed[i]]
    model, fit_evidence = fit_ar(train, config)
    evidence["ar_fit"] = fit_evidence
    if model is not None:
        context = samples[start - model.order:start] if start >= model.order else []
        if len(context) == model.order and not any(is_missing(v) for v in context):
            values, errors = predict_ar(model, context, length)
            errors = [e * config.error_scale for e in errors]
            evidence["train_samples_used"] = model.train_samples
            evidence["residual_std"] = model.residual_std
            return GapReport(start, length, STATUS_REPAIRED, METHOD_AR,
                             tuple(errors), evidence), (values, errors)
        evidence["degrade_reason"] = "AR prediction context contains missing samples"
    else:
        evidence["degrade_reason"] = fit_evidence.get("reason", "AR fit failed")
    if has_left and has_right:
        values, errors, rough = _linear_patch(samples, confirmed, start, length, config)
        evidence["degraded_to"] = METHOD_LINEAR
        evidence["roughness"] = rough
        return GapReport(start, length, STATUS_DEGRADED, METHOD_LINEAR,
                         tuple(errors), evidence), (values, errors)
    evidence["anchors"] = {"left": has_left, "right": has_right}
    return GapReport(start, length, STATUS_LEFT_UNKNOWN, None, None, evidence), None


def _repair_offline(samples, confirmed, start, length, config, evidence,
                    has_left, has_right):
    if not (has_left and has_right):
        evidence["reason"] = (
            "two-sided linear interpolation requires both anchors; "
            "a gap without a left or right anchor stays unknown"
        )
        evidence["anchors"] = {"left": has_left, "right": has_right}
        return GapReport(start, length, STATUS_LEFT_UNKNOWN, None, None, evidence), None
    values, errors, rough = _linear_patch(samples, confirmed, start, length, config)
    evidence["roughness"] = rough
    return GapReport(start, length, STATUS_REPAIRED, METHOD_LINEAR,
                     tuple(errors), evidence), (values, errors)


def _repair_gap(samples, confirmed, start, length, config):
    evidence = {"mode": config.mode, "model_version": config.model_version}
    if length > config.max_gap_len:
        evidence["reason"] = (
            f"gap length {length} exceeds max_gap_len {config.max_gap_len}"
        )
        return GapReport(start, length, STATUS_LEFT_UNKNOWN, None, None, evidence), None
    has_left = start - 1 >= 0 and confirmed[start - 1]
    has_right = start + length < len(samples) and confirmed[start + length]
    if config.mode == MODE_ONLINE:
        return _repair_online(samples, confirmed, start, length, config,
                              evidence, has_left, has_right)
    return _repair_offline(samples, confirmed, start, length, config,
                           evidence, has_left, has_right)


def repair_all(samples, config: RepairConfig) -> RepairResult:
    """Repair every eligible gap in one left-to-right pass."""
    original = [float(v) for v in samples]
    confirmed = [not is_missing(v) for v in original]
    work = list(original)
    mask = [False] * len(work)
    errors = [None] * len(work)
    reports = []
    for start, length in find_gaps(original):
        report, patch = _repair_gap(work, confirmed, start, length, config)
        reports.append(report)
        if patch is not None:
            values, errs = patch
            for k in range(length):
                work[start + k] = values[k]
                mask[start + k] = True
                errors[start + k] = errs[k]
    return RepairResult(samples=work, repair_mask=mask, error_estimates=errors,
                        gaps=reports, config=config)


def restore_segments(samples, config: RepairConfig):
    """Restore one gap at a time, left to right.

    Returns ``(final_samples, step_reports)``. The final samples are
    identical (within ``NUMERICAL_TOLERANCE``) to
    ``repair_all(samples, config).samples`` because each gap's repair only
    depends on confirmed original samples and deterministic earlier
    repairs.
    """
    original = [float(v) for v in samples]
    confirmed = [not is_missing(v) for v in original]
    work = list(original)
    step_reports = []
    for start, length in find_gaps(original):
        report, patch = _repair_gap(work, confirmed, start, length, config)
        step_reports.append(report)
        if patch is not None:
            values, _ = patch
            for k in range(length):
                work[start + k] = values[k]
    return work, step_reports
