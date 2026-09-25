"""Streaming multi-channel relative-delay estimator on top of NCC + DP."""

from __future__ import annotations

import json
from dataclasses import dataclass, field
from typing import Sequence

from . import ncc as ncc_mod
from .dp import TrajectoryStep, solve_trajectory
from .ncc import WindowEvidence
from .tolerances import DEFAULT_LAG_UNCERTAINTY_SAMPLES, GRID_HALF_BIN_SAMPLES


def slice_windows(
    n_samples: int,
    window_len: int,
    hop: int,
) -> list[tuple[int, int, int]]:
    """Return ``(window_index, start, stop)`` triples with full windows only."""
    if window_len <= 0 or hop <= 0:
        raise ValueError("window_len and hop must be positive")
    out: list[tuple[int, int, int]] = []
    idx = 0
    start = 0
    while start + window_len <= n_samples:
        out.append((idx, start, start + window_len))
        idx += 1
        start += hop
    return out


@dataclass
class WindowDelay:
    window_index: int
    start: int
    stop: int
    lag_samples: float | None
    lag_int_samples: int | None
    lag_seconds: float | None
    uncertainty_samples: float | None
    drift_samples_per_window: float | None
    drift_seconds_per_second: float | None
    ncc: float | None
    polarity: int | None
    ambiguous: bool
    candidate_lags: list[int]
    status: str
    reason: str

    @property
    def known(self) -> bool:
        return self.lag_samples is not None


@dataclass
class DelayResult:
    fs: float
    hop: int
    max_lag: int
    windows: dict[int, WindowDelay] = field(default_factory=dict)
    pair: tuple[int, int] = (0, 1)
    trajectory_cost: float = 0.0

    def ordered(self) -> list[WindowDelay]:
        return [self.windows[k] for k in sorted(self.windows)]


class DelayEstimator:
    """Accumulate window evidence and solve the global delay trajectory.

    Windows may be added in any order and at any time, including late ones.
    Every :meth:`solve` re-runs the dynamic program over *all* evidence seen
    so far, so a late window can revise the chosen path before and after it.
    """

    def __init__(
        self,
        fs: float,
        window_len: int,
        hop: int,
        max_lag: int,
        velocities: Sequence[int] = (-1, 0, 1),
        max_jump: int = 3,
        min_overlap_pairs: int = 8,
        skip_cost: float = 2.0,
        restart_cost: float = 2.0,
        pair: tuple[int, int] = (0, 1),
    ) -> None:
        if fs <= 0:
            raise ValueError("fs must be positive")
        self.fs = float(fs)
        self.window_len = int(window_len)
        self.hop = int(hop)
        self.max_lag = int(max_lag)
        self.velocities = list(velocities)
        self.max_jump = int(max_jump)
        self.min_overlap_pairs = int(min_overlap_pairs)
        self.skip_cost = float(skip_cost)
        self.restart_cost = float(restart_cost)
        self.pair = tuple(pair)
        self._evidence: dict[int, WindowEvidence] = {}

    # ------------------------------------------------------------------
    # Evidence ingestion
    # ------------------------------------------------------------------
    def add_window(
        self,
        a: Sequence[float],
        b: Sequence[float],
        window_index: int,
        start: int,
        stop: int,
        mask_a: Sequence[bool] | None = None,
        mask_b: Sequence[bool] | None = None,
    ) -> WindowEvidence:
        ev = ncc_mod.window_evidence(
            a,
            b,
            window_index=window_index,
            start=start,
            stop=stop,
            max_lag=self.max_lag,
            mask_a=mask_a,
            mask_b=mask_b,
            min_overlap_pairs=self.min_overlap_pairs,
        )
        self._evidence[window_index] = ev
        return ev

    def add_arrays(
        self,
        signals: Sequence[Sequence[float]],
        masks: Sequence[Sequence[bool]] | None = None,
    ) -> None:
        """Run all full sliding windows over a channel pair (multi-channel).

        ``signals`` holds ``C`` equal-length channels; ``self.pair`` selects
        the two channels correlated.
        """
        ci, cj = self.pair
        a, b = signals[ci], signals[cj]
        mask_a = None if masks is None else masks[ci]
        mask_b = None if masks is None else masks[cj]
        for idx, start, stop in slice_windows(len(a), self.window_len, self.hop):
            self.add_window(a, b, idx, start, stop, mask_a, mask_b)

    def add_evidence(self, evidence: WindowEvidence) -> None:
        """Inject externally built evidence (used by tests/fixtures)."""
        self._evidence[evidence.window_index] = evidence

    @property
    def evidence(self) -> dict[int, WindowEvidence]:
        return dict(self._evidence)

    # ------------------------------------------------------------------
    # Global solve
    # ------------------------------------------------------------------
    def solve(self) -> DelayResult:
        evidence = [self._evidence[k] for k in sorted(self._evidence)]
        solution = solve_trajectory(
            evidence,
            velocities=self.velocities,
            max_jump=self.max_jump,
            skip_cost=self.skip_cost,
            restart_cost=self.restart_cost,
        )
        result = DelayResult(
            fs=self.fs,
            hop=self.hop,
            max_lag=self.max_lag,
            pair=self.pair,
            trajectory_cost=solution.cost,
        )
        by_index = {step.window_index: step for step in solution.steps}
        for ev in evidence:
            result.windows[ev.window_index] = self._to_window_delay(ev, by_index[ev.window_index])
        return result

    def _to_window_delay(self, ev: WindowEvidence, step: TrajectoryStep) -> WindowDelay:
        if not step.active:
            return WindowDelay(
                window_index=ev.window_index,
                start=ev.start,
                stop=ev.stop,
                lag_samples=None,
                lag_int_samples=None,
                lag_seconds=None,
                uncertainty_samples=None,
                drift_samples_per_window=None,
                drift_seconds_per_second=None,
                ncc=None,
                polarity=None,
                ambiguous=False,
                candidate_lags=ev.candidate_lags(),
                status=ev.status,
                reason=ev.reason,
            )

        chosen_peak = next(peak for peak in ev.peaks if peak.lag == step.lag_int)
        uncertainty = _uncertainty(ev, chosen_peak)
        lag_seconds = step.lag / self.fs
        drift_v = step.velocity
        drift_per_sec = None
        if drift_v is not None:
            window_seconds = self.hop / self.fs
            drift_per_sec = (drift_v / self.fs) / window_seconds if window_seconds > 0 else None
        return WindowDelay(
            window_index=ev.window_index,
            start=ev.start,
            stop=ev.stop,
            lag_samples=step.lag,
            lag_int_samples=step.lag_int,
            lag_seconds=lag_seconds,
            uncertainty_samples=uncertainty,
            drift_samples_per_window=drift_v,
            drift_seconds_per_second=drift_per_sec,
            ncc=chosen_peak.ncc,
            polarity=chosen_peak.polarity,
            ambiguous=not ev.unique,
            candidate_lags=ev.candidate_lags(),
            status="evidence",
            reason="",
        )

    # ------------------------------------------------------------------
    # Checkpointing: full state round-trip through JSON
    # ------------------------------------------------------------------
    def to_checkpoint(self) -> dict:
        return {
            "fs": self.fs,
            "window_len": self.window_len,
            "hop": self.hop,
            "max_lag": self.max_lag,
            "velocities": self.velocities,
            "max_jump": self.max_jump,
            "min_overlap_pairs": self.min_overlap_pairs,
            "skip_cost": self.skip_cost,
            "restart_cost": self.restart_cost,
            "pair": list(self.pair),
            "evidence": [self._evidence[k].to_dict() for k in sorted(self._evidence)],
        }

    def save_checkpoint(self, path: str) -> None:
        with open(path, "w", encoding="utf-8") as handle:
            json.dump(self.to_checkpoint(), handle, indent=2, sort_keys=True)

    @classmethod
    def from_checkpoint(cls, data: dict) -> "DelayEstimator":
        est = cls(
            fs=float(data["fs"]),
            window_len=int(data["window_len"]),
            hop=int(data["hop"]),
            max_lag=int(data["max_lag"]),
            velocities=tuple(data.get("velocities", (-1, 0, 1))),
            max_jump=int(data.get("max_jump", 3)),
            min_overlap_pairs=int(data.get("min_overlap_pairs", 8)),
            skip_cost=float(data.get("skip_cost", 2.0)),
            restart_cost=float(data.get("restart_cost", 2.0)),
            pair=tuple(data.get("pair", (0, 1))),
        )
        for item in data.get("evidence", ()):
            est.add_evidence(WindowEvidence.from_dict(item))
        return est

    @classmethod
    def load_checkpoint(cls, path: str) -> "DelayEstimator":
        with open(path, encoding="utf-8") as handle:
            return cls.from_checkpoint(json.load(handle))


def _uncertainty(ev: WindowEvidence, chosen_peak) -> float:
    """Conservative per-window lag uncertainty in samples.

    At least the half-bin grid bound; widened to cover the spread of tied
    candidate lags when the window is ambiguous, plus a small fraction of the
    parabolic interpolation offset.
    """
    uncertainty = GRID_HALF_BIN_SAMPLES
    if len(ev.peaks) > 1:
        spread = max(peak.lag for peak in ev.peaks) - min(peak.lag for peak in ev.peaks)
        uncertainty = max(uncertainty, float(spread))
    offset = abs(chosen_peak.fractional_offset)
    uncertainty = max(uncertainty, DEFAULT_LAG_UNCERTAINTY_SAMPLES + offset)
    return uncertainty


def estimate_delays(
    signals: Sequence[Sequence[float]],
    fs: float,
    window_len: int,
    hop: int,
    max_lag: int,
    masks: Sequence[Sequence[bool]] | None = None,
    pair: tuple[int, int] = (0, 1),
    velocities: Sequence[int] = (-1, 0, 1),
    max_jump: int = 3,
) -> DelayResult:
    """One-shot helper: build the estimator, ingest all windows, solve."""
    est = DelayEstimator(
        fs=fs,
        window_len=window_len,
        hop=hop,
        max_lag=max_lag,
        velocities=velocities,
        max_jump=max_jump,
        pair=pair,
    )
    est.add_arrays(signals, masks)
    return est.solve()
