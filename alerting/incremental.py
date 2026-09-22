"""Incremental truth layer.

Feeds samples one at a time (arrival order) and maintains the same firing
episodes as a full recompute.  Out-of-order or duplicate event times
(backfills / corrections) cannot be handled incrementally: the machine
flags ``needs_recompute`` and the caller must fall back to
``engine.compute_episodes`` plus reconciliation.
"""

from __future__ import annotations

from .models import Episode, RuleVersion, Sample


class IncrementalRule:
    def __init__(self, rule: RuleVersion):
        self.rule = rule
        self.episodes: list[Episode] = []  # last one may be open (resolve_time=None)
        self.needs_recompute = False
        self._prev: Sample | None = None
        self._pending_since: float | None = None
        self._firing_since: float | None = None
        self._false_since: float | None = None

    def add(self, sample: Sample) -> None:
        if self._prev is not None and sample.event_time <= self._prev.event_time:
            # Backfill or same-time correction: incremental path gives up.
            self.needs_recompute = True
            return
        if self._prev is not None:
            self._advance(self._prev.event_time, sample.event_time, self._prev.value)
        self._prev = sample

    def advance_to(self, t: float) -> None:
        """Extend the open tail segment with the last known value."""
        if self._prev is None or t <= self._prev.event_time:
            return
        self._advance(self._prev.event_time, t, self._prev.value)
        self._prev = Sample(t, self._prev.value, self._prev.seq)

    def _advance(self, start: float, end: float, v: float) -> None:
        r = self.rule
        if self._firing_since is None:
            if v >= r.enter_threshold:
                if self._pending_since is None:
                    self._pending_since = start
                fire_at = self._pending_since + r.enter_duration
                if fire_at < end:
                    self.episodes.append(Episode(r.rule_id, fire_at, None))
                    self._firing_since = fire_at
                    self._pending_since = None
            else:
                self._pending_since = None
        else:
            if v >= r.exit_threshold:
                self._false_since = None
            else:
                if self._false_since is None:
                    self._false_since = start
                resolve_at = self._false_since + r.exit_duration
                if resolve_at < end:
                    ep = self.episodes[-1]
                    self.episodes[-1] = Episode(ep.rule_id, ep.fire_start, resolve_at)
                    self._firing_since = None
                    self._false_since = None
