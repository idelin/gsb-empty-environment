"""Three-valued online monitor for finite temporal rules.

The monitor ingests events whose timestamps may arrive out of order and
supports retraction of previously ingested events.  Both can revise
already published verdicts; every published version is retained.

State is bounded by a lateness budget: events older than
``max(max_ts - lateness, watermark)`` cannot be retained, so ingesting one
raises :class:`BackpressureError` explicitly instead of silently degrading
verdicts.  A budget abort is therefore never reported as a violation.

Obligations only close (become final) when a watermark passes their window
end or when the stream is terminated.
"""

from __future__ import annotations

import math
from dataclasses import dataclass, field

from .events import Event
from .rules import RuleSpec
from .verdicts import VerdictRecord, VerdictValue
from .witness import (
    COMPLETENESS_OPEN,
    COMPLETENESS_TERMINATION,
    COMPLETENESS_WATERMARK,
    KIND_FORBIDDEN,
    KIND_MISSING,
    Witness,
)


class BackpressureError(Exception):
    """Raised when an event arrives beyond the lateness budget."""


@dataclass
class Obligation:
    """One open or closed obligation created by a trigger prefix match."""

    rule: RuleSpec
    trigger: tuple[Event, ...]
    window_end: float
    require_matches: dict[str, Event] = field(default_factory=dict)
    forbid_matches: dict[str, Event] = field(default_factory=dict)
    satisfier: Event | None = None
    value: VerdictValue | None = None
    final: bool = False
    history: list[VerdictRecord] = field(default_factory=list)

    @property
    def key(self) -> tuple:
        return (self.rule.name, tuple(e.id for e in self.trigger))


def _event_dict(event: Event) -> dict:
    return {"id": event.id, "ts": event.ts, "symbol": event.symbol}


class Monitor:
    def __init__(self, rules, lateness: float = math.inf):
        rules = list(rules)
        if not rules:
            raise ValueError("at least one rule is required")
        names = [r.name for r in rules]
        if len(set(names)) != len(names):
            raise ValueError("rule names must be unique")
        if lateness < 0:
            raise ValueError("lateness budget must be non-negative")
        self._rules = {r.name: r for r in rules}
        self._alphabet = frozenset().union(*(r.alphabet_set for r in rules))
        self._lateness = lateness
        self._live: dict[str, Event] = {}
        self._retracted: set[str] = set()
        self._max_ts = -math.inf
        self._watermark = -math.inf
        self._terminated = False
        self._obligations: dict[tuple, Obligation] = {}
        self._log: list[VerdictRecord] = []
        self._version = 0

    # ------------------------------------------------------------------ API

    @property
    def log(self) -> list[VerdictRecord]:
        return list(self._log)

    @property
    def obligations(self) -> list[Obligation]:
        return list(self._obligations.values())

    @property
    def live_count(self) -> int:
        return len(self._live)

    @property
    def watermark(self) -> float:
        return self._watermark

    @property
    def terminated(self) -> bool:
        return self._terminated

    def ingest(self, event: Event) -> bool:
        """Ingest one event.  Returns False for ignored duplicates.

        Raises BackpressureError when the event is older than the retention
        horizon; the monitor state is left unchanged in that case.
        """
        if event.symbol not in self._alphabet:
            raise ValueError(f"symbol {event.symbol!r} not in monitor alphabet")
        if event.id in self._live:
            return False  # duplicate: no new obligation, no state change
        self._retracted.discard(event.id)  # re-assertion of a retracted id
        horizon = self._horizon()
        if event.ts <= horizon:
            raise BackpressureError(
                f"event {event.id!r} at ts={event.ts} is beyond the retention "
                f"horizon {horizon}; lateness budget exceeded"
            )
        self._live[event.id] = event
        if event.ts > self._max_ts:
            self._max_ts = event.ts
        self._purge()
        self._recompute()
        return True

    def retract(self, event_id: str) -> None:
        """Retract a previously ingested event; revises affected verdicts."""
        self._retracted.add(event_id)
        self._live.pop(event_id, None)
        self._recompute()

    def advance_watermark(self, ts: float) -> None:
        """Close obligations whose window ends at or before ``ts``."""
        if ts <= self._watermark:
            return
        self._watermark = ts
        self._purge()
        self._recompute()

    def terminate(self) -> None:
        """Close all remaining obligations (end of stream)."""
        if self._terminated:
            return
        self._terminated = True
        self._recompute()

    def verdicts(self) -> dict[tuple, tuple[VerdictValue, bool]]:
        """Current (value, final) per obligation key."""
        return {key: (ob.value, ob.final) for key, ob in self._obligations.items()}

    def counterexample(self) -> Witness | None:
        """The shortest violation witness among currently FALSE obligations."""
        witnesses = [
            self._witness_of(ob)
            for ob in self._obligations.values()
            if ob.value is VerdictValue.FALSE
        ]
        if not witnesses:
            return None
        return min(
            witnesses,
            key=lambda w: (len(w.events), w.window_end, w.events[-1].id),
        )

    # ----------------------------------------------------------- checkpoint

    def checkpoint(self) -> dict:
        """Serialise pending obligations, the retraction index and the log."""
        return {
            "lateness": self._lateness,
            "max_ts": self._max_ts,
            "watermark": self._watermark,
            "terminated": self._terminated,
            "version": self._version,
            "live": [_event_dict(e) for e in self._live.values()],
            "retracted": sorted(self._retracted),
            "obligations": [self._obligation_dict(ob) for ob in self._obligations.values()],
            "log": [rec.to_dict() for rec in self._log],
        }

    @classmethod
    def restore(cls, rules, state: dict) -> "Monitor":
        monitor = cls(rules, lateness=state["lateness"])
        monitor._max_ts = state["max_ts"]
        monitor._watermark = state["watermark"]
        monitor._terminated = state["terminated"]
        monitor._version = state["version"]
        monitor._live = {d["id"]: Event(**d) for d in state["live"]}
        monitor._retracted = set(state["retracted"])
        for od in state["obligations"]:
            ob = Obligation(
                rule=monitor._rules[od["rule"]],
                trigger=tuple(Event(**d) for d in od["trigger"]),
                window_end=od["window_end"],
            )
            ob.require_matches = {d["id"]: Event(**d) for d in od["require_matches"]}
            ob.forbid_matches = {d["id"]: Event(**d) for d in od["forbid_matches"]}
            ob.satisfier = Event(**od["satisfier"]) if od["satisfier"] else None
            ob.value = VerdictValue(od["value"]) if od["value"] else None
            ob.final = od["final"]
            ob.history = [VerdictRecord.from_dict(r) for r in od["history"]]
            monitor._obligations[ob.key] = ob
        monitor._log = [VerdictRecord.from_dict(r) for r in state["log"]]
        return monitor

    # -------------------------------------------------------------- internals

    def _horizon(self) -> float:
        return max(self._watermark, self._max_ts - self._lateness)

    def _purge(self) -> None:
        horizon = self._horizon()
        self._live = {i: e for i, e in self._live.items() if e.ts > horizon}

    def _recompute(self) -> None:
        events = sorted(self._live.values(), key=lambda e: (e.ts, e.id))
        for rule in self._rules.values():
            self._detect(rule, events)
        for ob in self._obligations.values():
            self._refresh_evidence(ob, events)
        for rule in self._rules.values():
            self._assign(rule)
        for ob in self._obligations.values():
            self._update(ob)

    def _detect(self, rule: RuleSpec, events: list[Event]) -> None:
        """Run the rule's explicit transition table over the live stream."""
        state = 0
        matched: list[Event] = []
        goal = len(rule.prefix)
        for event in events:
            if event.symbol not in rule.alphabet_set:
                continue  # projection onto the rule's alphabet
            state = rule.transitions[(state, event.symbol)]
            if state == 0:
                matched = []
            else:
                matched = matched[: state - 1] + [event]
            if state == goal:
                trigger = tuple(matched)
                key = (rule.name, tuple(e.id for e in trigger))
                if key not in self._obligations:
                    self._obligations[key] = Obligation(
                        rule=rule,
                        trigger=trigger,
                        window_end=trigger[-1].ts + rule.window,
                    )

    def _refresh_evidence(self, ob: Obligation, events: list[Event]) -> None:
        lo, hi = ob.trigger[-1].ts, ob.window_end
        req: dict[str, Event] = {}
        frb: dict[str, Event] = {}
        for event in events:
            if event.ts <= lo or event.ts > hi:
                continue
            if ob.rule.require is not None and event.symbol == ob.rule.require:
                req[event.id] = event
            if event.symbol in ob.rule.forbid_set:
                frb[event.id] = event
        # Union with evidence snapshotted before purging; drop retracted ids.
        ob.require_matches = {
            k: v for k, v in ob.require_matches.items() if k not in self._retracted
        } | req
        ob.forbid_matches = {
            k: v for k, v in ob.forbid_matches.items() if k not in self._retracted
        } | frb

    def _assign(self, rule: RuleSpec) -> None:
        obs = [ob for ob in self._obligations.values() if ob.rule.name == rule.name]
        if rule.require is None:
            for ob in obs:
                ob.satisfier = None
            return
        if rule.consume_witness:
            used: set[str] = set()
            for ob in sorted(obs, key=lambda o: (o.window_end, o.key)):
                lo, hi = ob.trigger[-1].ts, ob.window_end
                candidates = sorted(
                    (
                        e
                        for i, e in ob.require_matches.items()
                        if i not in used and lo < e.ts <= hi
                    ),
                    key=lambda e: (e.ts, e.id),
                )
                ob.satisfier = candidates[0] if candidates else None
                if ob.satisfier is not None:
                    used.add(ob.satisfier.id)
        else:
            for ob in obs:
                lo, hi = ob.trigger[-1].ts, ob.window_end
                candidates = [
                    e for e in ob.require_matches.values() if lo < e.ts <= hi
                ]
                ob.satisfier = (
                    min(candidates, key=lambda e: (e.ts, e.id)) if candidates else None
                )

    def _update(self, ob: Obligation) -> None:
        closed = self._terminated or self._watermark >= ob.window_end
        if ob.forbid_matches:
            value, reason = VerdictValue.FALSE, "forbidden event inside window"
        elif ob.rule.require is not None and ob.satisfier is None and closed:
            value, reason = VerdictValue.FALSE, "requirement missing at window close"
        elif ob.rule.require is not None and ob.satisfier is not None:
            value, reason = VerdictValue.TRUE, "requirement satisfied"
        elif ob.rule.require is None and closed:
            value, reason = VerdictValue.TRUE, "no forbidden event at window close"
        else:
            value, reason = VerdictValue.PENDING, "window still open"
        final = closed and value is not VerdictValue.PENDING
        if value != ob.value or final != ob.final:
            self._version += 1
            record = VerdictRecord(
                version=self._version,
                obligation=ob.key,
                rule=ob.rule.name,
                value=value,
                final=final,
                reason=reason,
            )
            ob.value, ob.final = value, final
            ob.history.append(record)
            self._log.append(record)

    def _witness_of(self, ob: Obligation) -> Witness:
        if self._terminated:
            completeness = COMPLETENESS_TERMINATION
        elif self._watermark >= ob.window_end:
            completeness = COMPLETENESS_WATERMARK
        else:
            completeness = COMPLETENESS_OPEN
        if ob.forbid_matches:
            bad = min(ob.forbid_matches.values(), key=lambda e: (e.ts, e.id))
            return Witness(
                rule=ob.rule,
                kind=KIND_FORBIDDEN,
                events=ob.trigger + (bad,),
                window_end=ob.window_end,
                completeness=completeness,
            )
        return Witness(
            rule=ob.rule,
            kind=KIND_MISSING,
            events=ob.trigger,
            window_end=ob.window_end,
            completeness=completeness,
        )

    @staticmethod
    def _obligation_dict(ob: Obligation) -> dict:
        return {
            "rule": ob.rule.name,
            "trigger": [_event_dict(e) for e in ob.trigger],
            "window_end": ob.window_end,
            "require_matches": [_event_dict(e) for e in ob.require_matches.values()],
            "forbid_matches": [_event_dict(e) for e in ob.forbid_matches.values()],
            "satisfier": _event_dict(ob.satisfier) if ob.satisfier else None,
            "value": ob.value.value if ob.value else None,
            "final": ob.final,
            "history": [rec.to_dict() for rec in ob.history],
        }
