"""Domain model for the revisable alert state machine.

Everything here is a *pure function of event time*: given the same ordered
samples and versioned rules it produces the same canonical episodes, no matter
what order the facts arrived in. engine.py may forget and recompute this view;
the outbox/notification layer may never silently delete a correction.
"""
from __future__ import annotations

from dataclasses import dataclass, field

# Canonical incident states (the "real fault" view, independent of notifying).
PENDING = "pending"        # value above enter threshold, duration not yet met
FIRING = "firing"          # duration met, currently not suppressed
SUPPRESSED = "suppressed"  # firing-level incident currently covered by a rule
RESOLVED = "resolved"      # value fell back to/below the exit threshold


@dataclass(frozen=True)
class Sample:
    t: int
    metric: str
    value: float
    seq: int = 0  # arrival order; breaks ties for same-time corrections


@dataclass(frozen=True)
class ThresholdVersion:
    """One immutable version of a threshold rule.

    "Editing" a rule = inserting a new version, possibly with valid_from in
    the past. "Rolling back" = inserting a fresh version that copies the
    parameters of an older one. Nothing is ever overwritten.
    """
    rule_id: str
    version: int
    valid_from: int
    metric: str
    enter_at: float
    exit_at: float
    duration: int
    seq: int = 0


@dataclass(frozen=True)
class SuppressionVersion:
    rule_id: str
    version: int
    valid_from: int             # configuration becomes known at this event t
    window_start: int           # suppressed window (event time)
    window_end: int | None      # None => open-ended
    metric: str | None = None   # None => suppresses every metric
    seq: int = 0


@dataclass
class Episode:
    """One canonical above-threshold episode under one threshold-rule version."""
    metric: str
    rule_id: str
    rule_version: int
    start_t: int
    end_t: int | None                 # None => still above threshold at horizon
    matures_at: int | None            # start_t + duration once version known
    mature_t: int | None              # event time enter+duration was reached
    first_open_t: int | None = None   # first mature t not covered by suppression
    # partition of the episode timeline into (start, end_incl|None, covered)
    coverage: list = field(default_factory=list)

    @property
    def key(self) -> tuple:
        return (self.metric, self.rule_id, self.rule_version, self.start_t)

    def covered_at(self, t: int) -> bool:
        for s, e, c in self.coverage:
            if s <= t and (e is None or t <= e):
                return c
        return False

    def state_at(self, horizon: int) -> str:
        if self.end_t is not None:
            return RESOLVED
        suppressed_now = self.covered_at(horizon)
        if self.matures_at is not None and horizon < self.matures_at:
            return PENDING
        if self.mature_t is None:
            return SUPPRESSED if suppressed_now else PENDING
        return SUPPRESSED if suppressed_now else FIRING

    def signature(self) -> tuple:
        """Identity of what the alert is. Equal signature => no correction."""
        return (self.metric, self.rule_id, self.rule_version, self.start_t,
                self.end_t, self.matures_at, self.mature_t,
                self.first_open_t,
                tuple((s, e, c) for s, e, c in self.coverage))


def active_at(versions, t):
    """Version valid at event time t; latest (valid_from, seq, version) wins."""
    candidates = [v for v in versions if v.valid_from <= t]
    if not candidates:
        return None
    return max(candidates, key=lambda v: (v.valid_from, v.seq, v.version))


def _suppressed_at(sup_versions, metric: str, t: int) -> bool:
    for v in sup_versions:
        if v.metric is not None and v.metric != metric:
            continue
        lo = max(v.window_start, v.valid_from)
        if lo <= t and (v.window_end is None or t < v.window_end):
            return True
    return False


def _annotate(ep: Episode, sup_versions, horizon: int) -> None:
    """Partition [start_t .. end] into covered/not-covered integer intervals.

    Suppression only counts once the rule version itself exists
    (valid_from) -- a rule retroactively created at time v cannot suppress a
    notification that was already due before v existed; it only reshapes the
    canonical view from v onward.
    """
    end = ep.end_t if ep.end_t is not None else horizon
    active = [v for v in sup_versions
              if v.metric is None or v.metric == ep.metric]

    bounds = {ep.start_t}
    for v in active:
        lo = max(v.window_start, v.valid_from, ep.start_t)
        if lo <= end:
            bounds.add(lo)
        if v.window_end is not None and ep.start_t < v.window_end <= end + 1:
            bounds.add(v.window_end)
        # an episode beginning inside a window is covered from its own start
        win_lo = max(v.window_start, v.valid_from)
        if win_lo < ep.start_t and (v.window_end is None
                                    or v.window_end > ep.start_t):
            bounds.add(ep.start_t)
    cuts = sorted(bounds)

    intervals = []
    for i, p in enumerate(cuts):
        e = (cuts[i + 1] - 1) if i + 1 < len(cuts) else end
        if p <= end:
            intervals.append((p, e, _suppressed_at(active, ep.metric, p)))
    ep.coverage = intervals

    # Open episode: the final segment runs to an open end. Its start is a
    # fixed boundary point (never the horizon itself) so simply advancing the
    # horizon does not change the canonical signature.
    if ep.end_t is None and intervals:
        last_p, _, c = intervals[-1]
        intervals[-1] = (last_p, None, c)

    if ep.mature_t is not None:
        first_open = None
        lo = max(ep.mature_t, ep.start_t)
        for t in range(lo, end + 1):
            if not _suppressed_at(active, ep.metric, t):
                first_open = t
                break
        ep.first_open_t = first_open


def evaluate(samples, th_versions, sup_versions, horizon=None) -> list[Episode]:
    """Pure event-time evaluation -> canonical episodes.

    Enter at start_t + duration once the value has stayed > enter_at for
    ``duration`` event-time units without an observed counter-example
    (sample <= exit_at). Sparse samples are fine: no news is not evidence of
    recovery, so an episode survives sampling gaps and maturity is measured on
    the event-time span. Exit at the first sample <= exit_at. Episodes split
    when the active rule version changes mid-incident.
    """
    samples = sorted(samples, key=lambda s: (s.t, s.seq))
    if horizon is None:
        horizon = samples[-1].t if samples else 0
    if not samples:
        return []

    by_metric: dict[str, list] = {}
    for s in samples:
        by_metric.setdefault(s.metric, []).append(s)

    episodes: list[Episode] = []
    for metric, rows in by_metric.items():
        versions = [v for v in th_versions if v.metric == metric]
        cur: Episode | None = None
        for smp in rows:
            t, val = smp.t, smp.value
            ver = active_at(versions, t)
            if cur is not None and (ver is None or
                                    ver.rule_id != cur.rule_id or
                                    ver.version != cur.rule_version):
                # boundary point belongs to the (possibly absent) new version
                cur.end_t = t
                episodes.append(cur)
                cur = None
            if ver is None:
                continue
            if cur is None:
                if val > ver.enter_at:
                    cur = Episode(metric, ver.rule_id, ver.version, t, None,
                                  t + ver.duration, None)
            elif val <= ver.exit_at:
                cur.end_t = t
                episodes.append(cur)
                cur = None
                continue
            # else (hysteresis zone exit_at < val <= enter_at): held open
            if cur is not None and cur.mature_t is None \
                    and t >= cur.start_t + ver.duration:
                cur.mature_t = cur.start_t + ver.duration
        if cur is not None:
            episodes.append(cur)

    for ep in episodes:
        if ep.mature_t is not None and ep.end_t is not None \
                and ep.mature_t >= ep.end_t:
            # episode ended before/at the instant duration elapsed => never fired
            ep.mature_t = None
        _annotate(ep, list(sup_versions), horizon)
    episodes.sort(key=lambda e: (e.metric, e.start_t, e.rule_id, e.rule_version))
    return episodes
