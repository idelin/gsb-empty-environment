"""Finite-candidate dynamic program selecting a continuous delay trajectory.

Each evidenced window offers one or more integer-lag candidates (NCC peaks,
including ties). A *landed* state at an evidenced window is
``(window_index, lag, velocity)``. Between two landed windows the track is
held as a *carry* state ``(last_idx, lag, velocity, gap)`` where ``gap`` is
the number of unknown windows crossed since the last landing. Resuming at a
later evidenced window requires linear motion

    lag_new - lag_old == velocity * (window_index_new - last_idx)

with the per-window jump bound ``|jump| <= max_jump * span``; acceleration
(change of velocity) is charged. Refusing an evidenced window costs
``skip_cost`` but lets an active track carry on, so a single anomalous peak
is ignored without breaking continuity or causing an unbounded jump.
Crossing unknown windows is free. Starting a new track after a broken path
(GAP) costs ``restart_cost``.

Windows are never maximized independently: the lag selected at every window
is the one on the globally cheapest full-time path.
"""

from __future__ import annotations

import math
from dataclasses import dataclass
from typing import Sequence

from .ncc import WindowEvidence

GAP = ("gap",)
START = ("start",)


@dataclass(frozen=True)
class TrajectoryStep:
    window_index: int
    active: bool
    lag: float | None
    lag_int: int | None
    velocity: float | None
    ncc: float | None
    polarity: int | None
    unique: bool
    peak_count: int


@dataclass
class TrajectorySolution:
    steps: list[TrajectoryStep]
    cost: float
    evidence: list[WindowEvidence]


def _landed(idx: int, lag: int, velocity: int) -> tuple:
    return ("landed", idx, lag, velocity)


def _carry(last_idx: int, lag: int, velocity: int, gap: int) -> tuple:
    return ("carry", last_idx, lag, velocity, gap)


def solve_trajectory(
    evidence: Sequence[WindowEvidence],
    velocities: Sequence[int] = (-1, 0, 1),
    max_jump: int = 3,
    weight_corr: float = 10.0,
    weight_accel: float = 1.0,
    skip_cost: float = 2.0,
    restart_cost: float = 3.0,
    accel_pow: float = 2.0,
) -> TrajectorySolution:
    evidence = sorted(evidence, key=lambda item: item.window_index)
    if not evidence:
        return TrajectorySolution(steps=[], cost=0.0, evidence=[])

    vel_list = list(velocities)
    dp: dict[tuple, tuple[float, tuple]] = {START: (0.0, START)}
    parents_by_window: list[dict[tuple, tuple]] = []

    for ev in evidence:
        candidates = {peak.lag: peak for peak in ev.peaks}
        next_dp: dict[tuple, tuple] = {}
        parents: dict[tuple, tuple] = {}

        def offer(state: tuple, cost: float, parent: tuple) -> None:
            old = next_dp.get(state)
            if old is None or _prefer(cost, state, old[0], old[1]):
                next_dp[state] = (cost, parent)
                parents[state] = parent

        if not candidates:
            # Unknown window: cross free; tracks carry with gap incremented.
            for state, (cost, _) in dp.items():
                if state[0] == "landed":
                    _, last_idx, lag, velocity = state
                    offer(_carry(last_idx, lag, velocity, 1), cost, state)
                elif state[0] == "carry":
                    _, last_idx, lag, velocity, gap = state
                    offer(_carry(last_idx, lag, velocity, gap + 1), cost, state)
                else:
                    offer(state, cost, state)
            dp = next_dp
            parents_by_window.append(parents)
            continue

        # Evidenced window: try LANDING on every candidate (lag, velocity).
        for lag, peak in candidates.items():
            emission = weight_corr * (1.0 - peak.ncc)
            for velocity in vel_list:
                state = _landed(ev.window_index, lag, velocity)
                best: tuple[float, tuple] | None = None

                def consider(parent_cost: float, parent: tuple) -> None:
                    nonlocal best
                    total = parent_cost + emission
                    if best is None or _prefer(total, state, best[0], best[1]):
                        best = (total, parent)

                if START in dp:
                    consider(dp[START][0], START)
                if GAP in dp:
                    consider(dp[GAP][0] + restart_cost, GAP)
                for pstate, (pcost, _) in dp.items():
                    if pstate[0] == "landed":
                        _, last_idx, plag, pvel = pstate
                        gap = 0
                    elif pstate[0] == "carry":
                        _, last_idx, plag, pvel, gap = pstate
                    else:
                        continue
                    span = ev.window_index - last_idx
                    jump = lag - plag
                    if span <= 0 or abs(jump) > max_jump * span:
                        continue
                    if jump != velocity * span:
                        continue
                    transition = weight_accel * abs(velocity - pvel) ** accel_pow
                    consider(pcost + transition, pstate)

                if best is not None:
                    offer(state, best[0], best[1])

        # Or REFUSE this evidence (+skip_cost). Active tracks carry on with
        # their motion model; broken states collapse to GAP.
        for state, (cost, _) in dp.items():
            if state[0] == "landed":
                _, last_idx, lag, velocity = state
                offer(_carry(last_idx, lag, velocity, 0), cost + skip_cost, state)
                offer(GAP, cost + skip_cost, state)
            elif state[0] == "carry":
                _, last_idx, lag, velocity, gap = state
                offer(_carry(last_idx, lag, velocity, gap), cost + skip_cost, state)
                offer(GAP, cost + skip_cost, state)
            else:
                offer(GAP, cost + skip_cost, state)

        dp = next_dp
        parents_by_window.append(parents)

    if not dp:
        steps = [
            TrajectoryStep(ev.window_index, False, None, None, None, None, None, False, len(ev.peaks))
            for ev in evidence
        ]
        return TrajectorySolution(steps=steps, cost=math.inf, evidence=evidence)

    terminal = min(dp, key=lambda state: _terminal_key(dp[state][0], state))
    total_cost = dp[terminal][0]

    chosen: dict[int, tuple] = {}
    state = terminal
    for idx in range(len(evidence) - 1, -1, -1):
        chosen[evidence[idx].window_index] = state
        state = parents_by_window[idx][state]

    steps: list[TrajectoryStep] = []
    for ev in evidence:
        state = chosen[ev.window_index]
        if state[0] == "landed":
            _, _, lag, velocity = state
            peak = next(item for item in ev.peaks if item.lag == lag)
            steps.append(
                TrajectoryStep(
                    window_index=ev.window_index,
                    active=True,
                    lag=peak.fractional_lag,
                    lag_int=lag,
                    velocity=float(velocity),
                    ncc=peak.ncc,
                    polarity=peak.polarity,
                    unique=ev.unique,
                    peak_count=len(ev.peaks),
                )
            )
        else:
            steps.append(
                TrajectoryStep(
                    window_index=ev.window_index,
                    active=False,
                    lag=None,
                    lag_int=None,
                    velocity=None,
                    ncc=None,
                    polarity=None,
                    unique=False,
                    peak_count=len(ev.peaks),
                )
            )
    return TrajectorySolution(steps=steps, cost=total_cost, evidence=evidence)


def _prefer(cost: float, state: tuple, old_cost: float, old_state: tuple) -> bool:
    if cost < old_cost - 1e-12:
        return True
    if cost > old_cost + 1e-12:
        return False
    return _state_rank(state) < _state_rank(old_state)


def _state_rank(state: tuple) -> tuple:
    if state == START:
        return (0, 0, 0, 0, 0, 0)
    if state == GAP:
        return (1, 0, 0, 0, 0, 0)
    kind = state[0]
    if kind == "landed":
        _, idx, lag, velocity = state
        return (2, idx, abs(lag), abs(velocity), lag, velocity)
    _, idx, lag, velocity, gap = state
    return (3, idx, gap, abs(lag), lag, velocity)


def _terminal_key(cost: float, state: tuple) -> tuple:
    return (cost,) + _state_rank(state)
