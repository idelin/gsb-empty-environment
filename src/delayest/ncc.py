"""Masked normalized cross-correlation on an integer lag grid.

Only the Python standard library is used.
"""

from __future__ import annotations

from dataclasses import dataclass, field
from typing import Sequence

from .tolerances import (
    MIN_OVERLAP_PAIRS,
    MIN_PEAK_NCC,
    NCC_CLAMP_TOL,
    PEAK_TIE_TOL,
    POLARITY_TOL,
)


def _clamped_ncc(value: float) -> float:
    if value > 1.0:
        if value - 1.0 <= NCC_CLAMP_TOL:
            return 1.0
    elif value < -1.0:
        if -1.0 - value <= NCC_CLAMP_TOL:
            return -1.0
    return value


def masked_ncc(
    a: Sequence[float],
    b: Sequence[float],
    lag: int,
    mask_a: Sequence[bool] | None = None,
    mask_b: Sequence[bool] | None = None,
) -> float | None:
    """Zero-mean, unit-variance NCC of ``a`` and ``b`` at integer ``lag``.

    ``b[n]`` is paired with ``a[n - lag]``. Samples missing in either mask
    are excluded. Returns ``None`` when fewer than two valid pairs exist or
    either side has zero variance on the intersection.
    """
    n = len(a)
    if len(b) != n:
        raise ValueError("a and b must have equal length (align windows first)")
    if lag >= n or lag <= -n:
        return None

    pairs: list[tuple[float, float]] = []
    for nb in range(n):
        na = nb - lag
        if na < 0 or na >= n:
            continue
        if mask_a is not None and not mask_a[na]:
            continue
        if mask_b is not None and not mask_b[nb]:
            continue
        pairs.append((float(a[na]), float(b[nb])))

    if len(pairs) < 2:
        return None

    k = len(pairs)
    mean_a = sum(x for x, _ in pairs) / k
    mean_b = sum(y for _, y in pairs) / k
    var_a = 0.0
    var_b = 0.0
    cov = 0.0
    for x, y in pairs:
        dx = x - mean_a
        dy = y - mean_b
        var_a += dx * dx
        var_b += dy * dy
        cov += dx * dy

    if var_a <= 0.0 or var_b <= 0.0:
        return None
    return _clamped_ncc(cov / (var_a * var_b) ** 0.5)


def _interp(seq: Sequence[float], index: float) -> float:
    """Linear interpolation sample of ``seq`` at a fractional index."""
    if index <= 0:
        return float(seq[0])
    if index >= len(seq) - 1:
        return float(seq[-1])
    lo = int(index)
    frac = index - lo
    return float(seq[lo]) * (1.0 - frac) + float(seq[lo + 1]) * frac


def fractional_ncc(
    a: Sequence[float],
    b: Sequence[float],
    lag: float,
    mask_a: Sequence[bool] | None = None,
    mask_b: Sequence[bool] | None = None,
    min_overlap_pairs: int = 2,
) -> float | None:
    """NCC at a fractional lag ``d`` with ``b[n]`` vs interp(a[n - d]).

    Mask membership for interpolated a-samples uses the floor index, which is
    conservative for sparse masks.
    """
    n = len(a)
    if len(b) != n:
        raise ValueError("a and b must have equal length")
    pairs: list[tuple[float, float]] = []
    for nb in range(n):
        na = nb - lag
        if na < 0 or na > n - 1:
            continue
        ia = int(na)
        if mask_a is not None and not mask_a[min(ia, n - 1)]:
            continue
        if mask_b is not None and not mask_b[nb]:
            continue
        pairs.append((_interp(a, na), float(b[nb])))
    if len(pairs) < max(2, min_overlap_pairs):
        return None
    k = len(pairs)
    mean_a = sum(x for x, _ in pairs) / k
    mean_b = sum(y for _, y in pairs) / k
    var_a = sum((x - mean_a) ** 2 for x, _ in pairs)
    var_b = sum((y - mean_b) ** 2 for _, y in pairs)
    if var_a <= 0.0 or var_b <= 0.0:
        return None
    cov = sum((x - mean_a) * (y - mean_b) for x, y in pairs)
    return _clamped_ncc(cov / (var_a * var_b) ** 0.5)


def refine_fractional(
    a: Sequence[float],
    b: Sequence[float],
    peak_lag: int,
    mask_a: Sequence[bool] | None,
    mask_b: Sequence[bool] | None,
    steps: int = 20,
) -> float:
    """Sub-sample offset around an integer peak by scanning |NCC|.

    Scans ``lag in [peak-1, peak+1]`` on a grid of 1/steps samples and
    returns the offset of the best |NCC|. Falls back to 0.0 if the scan is
    uninformative.
    """
    best_lag = float(peak_lag)
    best_value = -1.0
    found = False
    for k in range(-steps, steps + 1):
        candidate = peak_lag + k / steps
        value = fractional_ncc(a, b, candidate, mask_a, mask_b)
        if value is None:
            continue
        magnitude = abs(value)
        if magnitude > best_value + 1e-12:
            best_value = magnitude
            best_lag = candidate
            found = True
    return (best_lag - peak_lag) if found else 0.0


def ncc_curve(
    a: Sequence[float],
    b: Sequence[float],
    max_lag: int,
    mask_a: Sequence[bool] | None = None,
    mask_b: Sequence[bool] | None = None,
) -> dict[int, float | None]:
    """NCC for every integer lag in ``[-max_lag, max_lag]``.

    Non-informative lag points map to ``None``. The curve is symmetric under
    polarity reversal in magnitude (the sign flips), so peak search considers
    both signs.
    """
    if max_lag < 0:
        raise ValueError("max_lag must be non-negative")
    curve: dict[int, float | None] = {}
    for lag in range(-max_lag, max_lag + 1):
        curve[lag] = masked_ncc(a, b, lag, mask_a, mask_b)
    return curve


@dataclass(frozen=True)
class LagPeak:
    """One NCC peak candidate."""

    lag: int
    ncc: float
    polarity: int  # +1: b ~= a[n-lag]; -1: b ~= -a[n-lag]
    fractional_offset: float = 0.0  # sub-sample refinement, added to ``lag``

    @property
    def fractional_lag(self) -> float:
        return self.lag + self.fractional_offset

    def to_dict(self) -> dict:
        return {
            "lag": self.lag,
            "ncc": self.ncc,
            "polarity": self.polarity,
            "fractional_offset": self.fractional_offset,
        }

    @staticmethod
    def from_dict(data: dict) -> "LagPeak":
        return LagPeak(
            lag=int(data["lag"]),
            ncc=float(data["ncc"]),
            polarity=int(data["polarity"]),
            fractional_offset=float(data.get("fractional_offset", 0.0)),
        )

def find_peaks(curve: dict[int, float | None]) -> list[LagPeak]:
    """Local maxima of ``|NCC|`` with polarity sign on the integer grid.

    Plateaus of equal values are merged into a single candidate at their
    central lag. Ties at the global best are all returned, in ascending lag
    order, so a periodic signal's repeated equal peaks cannot be reported as
    a unique delay.
    """
    lags = sorted(curve)
    mag: dict[int, float] = {}
    sign: dict[int, int] = {}
    for lag in lags:
        value = curve[lag]
        if value is None:
            continue
        mag[lag] = abs(value)
        sign[lag] = 1 if value >= 0.0 else -1

    raw: list[tuple[int, float, int]] = []
    i = 0
    ordered = sorted(mag)
    while i < len(ordered):
        lag = ordered[i]
        value = mag[lag]
        j = i + 1
        while j < len(ordered) and ordered[j] == ordered[j - 1] + 1 and mag[ordered[j]] == value:
            j += 1
        plateau = ordered[i:j]
        center = plateau[len(plateau) // 2]
        left = mag.get(center - 1)
        right = mag.get(center + 1)
        if (left is None or value >= left) and (right is None or value >= right):
            raw.append((center, value, sign[center]))
        i = j

    if not raw:
        return []

    best = max(value for _, value, _ in raw)
    tied = [item for item in raw if best - item[1] <= PEAK_TIE_TOL]
    tied.sort(key=lambda item: item[0])

    # Sub-sample refinement is performed later by refine_fractional, which
    # has access to the raw signals; find_peaks reports the integer grid only.
    return [
        LagPeak(lag=lag, ncc=value, polarity=polarity)
        for lag, value, polarity in tied
    ]


@dataclass
class WindowEvidence:
    """Evidence extracted from one time window of a channel pair."""

    window_index: int
    start: int
    stop: int
    peaks: list[LagPeak] = field(default_factory=list)
    curve: dict[int, float] = field(default_factory=dict)
    n_pairs: int = 0
    polarity: int | None = None
    unique: bool = False
    status: str = "unknown"  # one of: evidence, silent, insufficient_overlap,
    #                         low_correlation, no_peak
    reason: str = ""

    @property
    def known(self) -> bool:
        return self.status == "evidence"

    def candidate_lags(self) -> list[int]:
        return [peak.lag for peak in self.peaks]

    def to_dict(self) -> dict:
        return {
            "window_index": self.window_index,
            "start": self.start,
            "stop": self.stop,
            "peaks": [peak.to_dict() for peak in self.peaks],
            "curve": {str(lag): value for lag, value in self.curve.items()},
            "n_pairs": self.n_pairs,
            "polarity": self.polarity,
            "unique": self.unique,
            "status": self.status,
            "reason": self.reason,
        }

    @staticmethod
    def from_dict(data: dict) -> "WindowEvidence":
        return WindowEvidence(
            window_index=int(data["window_index"]),
            start=int(data["start"]),
            stop=int(data["stop"]),
            peaks=[LagPeak.from_dict(item) for item in data.get("peaks", ())],
            curve={int(lag): float(value) for lag, value in data.get("curve", {}).items()},
            n_pairs=int(data.get("n_pairs", 0)),
            polarity=data.get("polarity"),
            unique=bool(data.get("unique", False)),
            status=data.get("status", "unknown"),
            reason=data.get("reason", ""),
        )


def _count_valid(seq: Sequence[float], mask: Sequence[bool] | None, lo: int, hi: int) -> tuple[int, float, float]:
    values = [float(seq[i]) for i in range(lo, hi) if mask is None or mask[i]]
    if not values:
        return 0, 0.0, 0.0
    mean = sum(values) / len(values)
    variance = sum((v - mean) ** 2 for v in values)
    return len(values), mean, variance


def window_evidence(
    a: Sequence[float],
    b: Sequence[float],
    window_index: int,
    start: int,
    stop: int,
    max_lag: int,
    mask_a: Sequence[bool] | None = None,
    mask_b: Sequence[bool] | None = None,
    min_overlap_pairs: int = MIN_OVERLAP_PAIRS,
) -> WindowEvidence:
    """Extract :class:`WindowEvidence` for one window.

    The window covers samples ``[start, stop)``. Correlation at integer lag
    ``d`` uses pairs inside the window, so effective overlap shrinks near the
    window borders; that shrinkage is counted exactly.
    """
    ev = WindowEvidence(window_index=window_index, start=start, stop=stop)
    length = stop - start

    # Two call shapes are supported:
    # * full arrays plus absolute [start, stop);
    # * one window's worth of samples, where start/stop only label the
    #   window (correlation then uses local indices).
    if 0 <= start and stop <= len(a) and len(a) == len(b):
        wa = a[start:stop]
        wb = b[start:stop]
        wma = None if mask_a is None else mask_a[start:stop]
        wmb = None if mask_b is None else mask_b[start:stop]
    elif len(a) == length and len(b) == length:
        wa, wb = list(a), list(b)
        wma, wmb = mask_a, mask_b
    else:
        raise IndexError("window [start, stop) is incompatible with the arrays")

    _, _, var_a = _count_valid(wa, wma, 0, length)
    _, _, var_b = _count_valid(wb, wmb, 0, length)
    if var_a <= 0.0 or var_b <= 0.0:
        ev.status = "silent"
        ev.reason = "at least one channel is constant on the window"
        return ev

    raw_curve = ncc_curve(wa, wb, max_lag, wma, wmb)
    finite = {lag: value for lag, value in raw_curve.items() if value is not None}

    # Pair counts depend on lag; count exactly on the window intersection.
    pair_counts: dict[int, int] = {}
    for lag in finite:
        count = 0
        for nb in range(length):
            na = nb - lag
            if 0 <= na < length:
                if (wma is None or wma[na]) and (wmb is None or wmb[nb]):
                    count += 1
        pair_counts[lag] = count
    usable = {lag: value for lag, value in finite.items() if pair_counts[lag] >= min_overlap_pairs}
    ev.n_pairs = max(pair_counts.values(), default=0)

    if not usable:
        ev.status = "insufficient_overlap"
        ev.reason = (
            f"fewer than {min_overlap_pairs} overlapping valid pairs at every lag"
        )
        ev.curve = dict(finite)
        return ev

    best_abs = max(abs(value) for value in usable.values())
    if best_abs < MIN_PEAK_NCC:
        ev.status = "low_correlation"
        ev.reason = f"best |NCC|={best_abs:.4f} below {MIN_PEAK_NCC}"
        ev.curve = {lag: usable[lag] for lag in sorted(usable)}
        return ev

    peaks = find_peaks(usable)
    if not peaks:
        ev.status = "no_peak"
        ev.reason = "no local maximum found on the usable curve"
        ev.curve = {lag: usable[lag] for lag in sorted(usable)}
        return ev

    refined = []
    for peak in peaks:
        offset = refine_fractional(wa, wb, peak.lag, wma, wmb)
        refined.append(
            LagPeak(
                lag=peak.lag,
                ncc=peak.ncc,
                polarity=peak.polarity,
                fractional_offset=offset,
            )
        )
    ev.peaks = refined
    ev.curve = {lag: usable[lag] for lag in sorted(usable)}
    ev.unique = len(peaks) == 1
    best_pos = max(usable.values())
    best_neg = min(usable.values())
    if abs(abs(best_pos) - abs(best_neg)) <= POLARITY_TOL:
        ev.polarity = peaks[0].polarity
    elif abs(best_pos) >= abs(best_neg):
        ev.polarity = 1
    else:
        ev.polarity = -1
    ev.status = "evidence"
    ev.reason = ""
    return ev
