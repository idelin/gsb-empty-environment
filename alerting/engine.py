"""Truth layer: pure, deterministic evaluation of the fault timeline.

Everything here is a function of (samples, rule version, suppression
versions) only, so any historical revision can be recomputed at will.
All durations are measured in *event time* on a step function: a sample's
value holds from its event_time until the next sample's event_time.
"""

from __future__ import annotations

import math

from .models import (
    DesiredNotification,
    Episode,
    FaultState,
    RuleVersion,
    Sample,
    SuppressionWindow,
)

StateSegment = tuple[FaultState, float, float]


def effective_samples(samples: list[Sample]) -> list[Sample]:
    """Collapse corrections: per event_time keep the highest-seq sample."""
    best: dict[float, Sample] = {}
    for s in samples:
        cur = best.get(s.event_time)
        if cur is None or s.seq >= cur.seq:
            best[s.event_time] = s
    return [best[t] for t in sorted(best)]


def compute_states(samples: list[Sample], rule: RuleVersion) -> list[StateSegment]:
    """Fault timeline as merged (state, start, end) segments.

    Hysteresis: pending/firing entry uses ``enter_threshold``; while firing,
    the exit condition uses ``exit_threshold``.  Durations must be sustained
    continuously in event time.
    """
    pts = effective_samples(samples)
    segs: list[StateSegment] = []

    def emit(state: FaultState, start: float, end: float) -> None:
        if end <= start:
            return
        if segs and segs[-1][0] == state and segs[-1][2] == start:
            segs[-1] = (state, segs[-1][1], end)
        else:
            segs.append((state, start, end))

    pending_since: float | None = None
    firing_since: float | None = None
    false_since: float | None = None
    ever_fired = False

    for i, p in enumerate(pts):
        seg_start = p.event_time
        seg_end = pts[i + 1].event_time if i + 1 < len(pts) else math.inf
        v = p.value
        if firing_since is None:
            if v >= rule.enter_threshold:
                if pending_since is None:
                    pending_since = seg_start
                fire_at = pending_since + rule.enter_duration
                if fire_at < seg_end:
                    emit(FaultState.PENDING, pending_since, fire_at)
                    firing_since = fire_at
                    ever_fired = True
                    pending_since = None
                    emit(FaultState.FIRING, firing_since, seg_end)
                else:
                    emit(FaultState.PENDING, pending_since, seg_end)
            else:
                pending_since = None
                emit(FaultState.RESOLVED if ever_fired else FaultState.OK, seg_start, seg_end)
        else:
            if v >= rule.exit_threshold:
                false_since = None
                emit(FaultState.FIRING, seg_start, seg_end)
            else:
                if false_since is None:
                    false_since = seg_start
                resolve_at = false_since + rule.exit_duration
                if resolve_at < seg_end:
                    emit(FaultState.FIRING, seg_start, resolve_at)
                    firing_since = None
                    false_since = None
                    emit(FaultState.RESOLVED, resolve_at, seg_end)
                else:
                    emit(FaultState.FIRING, seg_start, seg_end)
    return segs


def episodes_from_states(segs: list[StateSegment], rule_id: str) -> list[Episode]:
    episodes = []
    for state, start, end in segs:
        if state == FaultState.FIRING:
            episodes.append(Episode(rule_id, start, None if end == math.inf else end))
    return episodes


def compute_episodes(samples: list[Sample], rule: RuleVersion) -> list[Episode]:
    return episodes_from_states(compute_states(samples, rule), rule.rule_id)


def defer_past_suppression(rule_id: str, t: float, windows: list[SuppressionWindow]) -> float:
    """Push ``t`` forward past any suppression window covering it."""
    moved = True
    while moved:
        moved = False
        for w in sorted(windows, key=lambda w: (w.start, w.end)):
            if w.covers(rule_id, t):
                t = w.end
                moved = True
    return t


def desired_notifications(
    episodes: list[Episode],
    windows: list[SuppressionWindow],
    horizon: float,
) -> list[DesiredNotification]:
    """Map truth-layer episodes to the notifications the world should see.

    * An episode that recovers while suppressed produces nothing: the fault
      is stale by the time suppression lifts and must not be re-notified.
    * An episode whose (possibly deferred) open time lies beyond the data
      horizon is not due yet.
    """
    out: list[DesiredNotification] = []
    for ep in episodes:
        open_time = defer_past_suppression(ep.rule_id, ep.fire_start, windows)
        if ep.resolve_time is not None and ep.resolve_time <= open_time:
            continue
        if open_time > horizon:
            continue
        out.append(DesiredNotification(ep.rule_id, ep.fire_start, open_time, ep.resolve_time))
    return out


def state_at(
    segs: list[StateSegment],
    windows: list[SuppressionWindow],
    rule_id: str,
    t: float,
) -> FaultState:
    base = FaultState.OK
    for state, start, end in segs:
        if start <= t < end:
            base = state
            break
    if base == FaultState.FIRING and any(w.covers(rule_id, t) for w in windows):
        return FaultState.SUPPRESSED
    return base
