"""Stream CEP engine.

Pattern per key: an A followed by a B with ``0 <= b_time - a_time <= 5 ms``
(closed interval), with no C strictly ordered between them.

Total order of records at the SAME timestamp (tie-break):
    A < C < B, then (source, seq) lexicographically.

Hence "C lies between A and B" means the C is *strictly after* the A and
*strictly before* the B in this total order. Consequences at equal times:

* A,C,B all at time t: C is between A and B  -> blocks the match.
* A,B at t (no C): a_time == b_time, d == 0 <= 5 -> matches.
* C before A in total order (none exists, since A ranks first) cannot block.
* C after B in total order at the same time t -> not between -> does not block.
  (C's rank is below B, so a same-time C is always *before* the B; this case
  occurs only for different A/B pairs that the C does not relate to.)

Watermark semantics (event time, half-open closure):
    watermark W means "no later record will have time < W".
    * record with t < W is LATE;
    * a match ending at B@b is FINALIZED once W > b (b is sealed);
    * entries are pruned once they can no longer participate in an open
      match: A/C when t + 5 < W, B when t < W.

Late records:
    * in-scope late records (A/C with t + WIN >= W) are incorporated and may
      still revise OPEN (not yet finalized) matches;
    * B can never be in-scope late because a late B only matches older As,
      whose matches ending at that B are already finalized at the moment the
      B becomes late -- accepting it would silently change final results;
    * out-of-scope late records are reported via on_side_report and never
      change finalized results.

Identity: every ingestible record is deduplicated by (source, seq).
"""

from __future__ import annotations

from collections import defaultdict
from dataclasses import dataclass, field
from typing import DefaultDict, Dict, Iterable, List, Optional, Set, Tuple

WINDOW_MS = 5

# Tie-break ranks for equal event times: A < C < B.
_RANK = {"A": 0, "C": 1, "B": 2}

Key = str
SourceId = str
Seq = int
RecordId = Tuple[SourceId, Seq]
MatchId = Tuple[Key, RecordId, RecordId]  # (key, a_id, b_id)


@dataclass(frozen=True)
class Event:
    etype: str           # "A" | "B" | "C"
    time: int            # event-time in ms
    key: Key
    source: SourceId
    seq: Seq

    def __post_init__(self) -> None:
        if self.etype not in _RANK:
            raise ValueError("etype must be one of A, B, C")

    @property
    def rid(self) -> RecordId:
        return (self.source, self.seq)


@dataclass(frozen=True)
class Withdraw:
    """Retraction of a previously emitted record, identified by (source,seq)."""

    source: SourceId
    seq: Seq

    @property
    def rid(self) -> RecordId:
        return (self.source, self.seq)


@dataclass(frozen=True)
class Watermark:
    time: int


# Ingestible record types.
@dataclass(frozen=True)
class Match:
    key: Key
    a: Event
    b: Event

    @property
    def mid(self) -> MatchId:
        return (self.key, self.a.rid, self.b.rid)


@dataclass
class _OpenMatch:
    match: Match
    proposed: bool = False
    # C records that currently block this match (usually 0 or 1).
    blockers: Dict[RecordId, Event] = field(default_factory=dict)


@dataclass(frozen=True)
class SideReport:
    kind: str                 # see KIND_* below
    record: object            # Event or Withdraw involved
    detail: str = ""
    mids: Tuple[MatchId, ...] = ()


KIND_DUPLICATE = "duplicate"
KIND_OUT_OF_SCOPE_LATE = "out_of_scope_late"
KIND_LATE_INCORPORATED = "late_incorporated"
KIND_WITHDRAW_UNKNOWN_OR_CLOSED = "withdraw_unknown_or_closed"
KIND_EVENT_AFTER_WITHDRAW = "event_after_withdraw"


class Collector:
    """Override the callbacks you need; every method has a no-op default."""

    def on_match_proposed(self, match: Match, late: bool = False) -> None: ...

    def on_match_revoked(self, match: Match, cause: str,
                         late: bool = False,
                         blocker: Optional[Event] = None) -> None: ...

    def on_match_final(self, match: Match) -> None: ...

    def on_side_report(self, report: SideReport) -> None: ...


def _sort_key(ev: Event) -> Tuple[int, int, str, int]:
    return (ev.time, _RANK[ev.etype], ev.source, ev.seq)


class StreamMatcher:
    def __init__(self, collector: Collector, window_ms: int = WINDOW_MS) -> None:
        self._out = collector
        self.win = window_ms
        self.watermark: Optional[int] = None

        # Events and withdrawals SHARE the identity space (source, seq): a
        # withdrawal references the identity of the record it retracts, but
        # dedup is per namespace, so event E and withdraw(E) do not collide.
        self._seen_events: Set[RecordId] = set()
        self._seen_withdraws: Set[RecordId] = set()
        self._withdrawn: Set[RecordId] = set()

        # per key: live (not pruned, not withdrawn) events
        self._as: DefaultDict[Key, Dict[RecordId, Event]] = defaultdict(dict)
        self._bs: DefaultDict[Key, Dict[RecordId, Event]] = defaultdict(dict)
        self._cs: DefaultDict[Key, Dict[RecordId, Event]] = defaultdict(dict)

        # per key: candidate matches (proposed or blocked), never finalized
        self._open: DefaultDict[Key, Dict[Tuple[RecordId, RecordId], _OpenMatch]] = (
            defaultdict(dict))

        # finalized match ids per key, retained for side reporting only
        self._finalized: DefaultDict[Key, Set[Tuple[RecordId, RecordId]]] = (
            defaultdict(set))

    # ---------------------------------------------------------------- ingest

    def push(self, record: object) -> None:
        if isinstance(record, Watermark):
            self.advance(record.time)
        elif isinstance(record, Withdraw):
            self._push_withdraw(record)
        elif isinstance(record, Event):
            self._push_event(record)
        else:
            raise TypeError("unsupported record: %r" % (record,))

    def push_many(self, records: Iterable[object]) -> None:
        for rec in records:
            self.push(rec)

    def _push_event(self, ev: Event) -> None:
        rid = ev.rid
        if rid in self._seen_events:
            self._out.on_side_report(SideReport(
                KIND_DUPLICATE, ev,
                "duplicate %s event (%s,%d) ignored"
                % (ev.etype, ev.source, ev.seq)))
            return
        self._seen_events.add(rid)

        if rid in self._withdrawn:
            self._out.on_side_report(SideReport(
                KIND_EVENT_AFTER_WITHDRAW, ev,
                "%s (%s,%d) arrived after its withdrawal; ignored"
                % (ev.etype, ev.source, ev.seq)))
            return

        w = self.watermark
        if w is not None and ev.time < w:
            # Late: incorporate only if the record can still affect an OPEN
            # match. Matches that the record *would* influence are already
            # finalized, so touching them would silently change final results.
            # Structural scope test: can this record ever influence a match
            # whose B is not yet sealed (b >= W)?
            #   A: a+WIN >= W leaves room for an on-time B;
            #   C: c+WIN >= W leaves room for a future B it could block;
            #   B: never (every B it would match is already sealed).
            in_scope = (ev.etype in ("A", "C")
                        and ev.time + self.win >= w)
            if not in_scope:
                self._out.on_side_report(SideReport(
                    KIND_OUT_OF_SCOPE_LATE, ev,
                    "late %s@%d reported separately: watermark=%d already "
                    "sealed every match it could affect; finals unchanged"
                    % (ev.etype, ev.time, w)))
                return
            revised = self._revised_open_ids(ev)
            self._insert(ev)
            self._recompute(ev.key, late=True)
            self._out.on_side_report(SideReport(
                KIND_LATE_INCORPORATED, ev,
                "late %s@%d incorporated within allowed lateness; %d open "
                "match(es) revised; finalized results untouched"
                % (ev.etype, ev.time, len(revised)),
                tuple((ev.key, a_id, b_id) for a_id, b_id in revised)))
            return

        self._insert(ev)
        self._recompute(ev.key, late=False)

    def _push_withdraw(self, wd: Withdraw) -> None:
        rid = wd.rid
        if rid in self._seen_withdraws:
            self._out.on_side_report(SideReport(
                KIND_DUPLICATE, wd,
                "duplicate withdraw (%s,%d) ignored" % rid))
            return
        self._seen_withdraws.add(rid)

        removed = self._remove_live(rid)
        self._withdrawn.add(rid)
        if removed is None:
            self._out.on_side_report(SideReport(
                KIND_WITHDRAW_UNKNOWN_OR_CLOSED, wd,
                "withdraw (%s,%d) cannot revise: record unknown or already "
                "sealed; finalized results are unchanged" % rid))
            return
        self._recompute(removed.key, late=False)

    # -------------------------------------------------------------- internal

    def _insert(self, ev: Event) -> None:
        bucket = {"A": self._as, "B": self._bs, "C": self._cs}[ev.etype]
        bucket[ev.key][ev.rid] = ev

    def _remove_live(self, rid: RecordId) -> Optional[Event]:
        for bucket in (self._as, self._bs, self._cs):
            for key, events in bucket.items():
                ev = events.pop(rid, None)
                if ev is not None:
                    return ev
        return None

    def _revised_open_ids(self, ev: Event) -> List[Tuple[RecordId, RecordId]]:
        """Open candidate matches this late record changes right now."""
        key = ev.key
        if ev.etype == "C":
            return [pair for pair, om in self._open.get(key, {}).items()
                    if _between(om.match.a, ev, om.match.b)]
        if ev.etype == "A":
            # A new (late) A can propose against Bs that are still live;
            # those pairs do not exist in self._open until _recompute.
            return [(ev.rid, b.rid) for b in self._bs.get(key, {}).values()
                    if _sort_key(ev) < _sort_key(b)
                    and 0 <= b.time - ev.time <= self.win]
        return []  # late B is always out of scope

    def _recompute(self, key: Key, late: bool) -> None:
        as_ = self._as.get(key, {})
        bs = self._bs.get(key, {})
        cs = self._cs.get(key, {})
        open_ = self._open[key]
        finalized = self._finalized.get(key, set())

        # Enumerate every candidate pair satisfying window + total order.
        candidate: Dict[Tuple[RecordId, RecordId], Match] = {}
        for a in as_.values():
            for b in bs.values():
                if _sort_key(a) < _sort_key(b) and 0 <= b.time - a.time <= self.win:
                    candidate[(a.rid, b.rid)] = Match(key, a, b)

        # (1) candidates no longer present (withdrawn/pruned input)
        gone = [pair for pair in open_ if pair not in candidate]
        for pair in gone:
            om = open_.pop(pair)
            if om.proposed:
                self._out.on_match_revoked(
                    om.match, cause="input_withdrawn", late=late)

        # (2) current candidates: create / update / settle
        for pair in sorted(candidate, key=lambda p: (_sort_key(candidate[p].a),
                                                     _sort_key(candidate[p].b))):
            match = candidate[pair]
            blockers = {c.rid: c for c in cs.values()
                        if _between(match.a, c, match.b)}
            om = open_.get(pair)
            blocked = bool(blockers)
            if om is None:
                if pair in finalized:
                    continue  # safety: finalized matches are never re-opened
                open_[pair] = _OpenMatch(match, proposed=False, blockers=blockers)
                om = open_[pair]
            else:
                om.blockers = blockers
            if not blocked and not om.proposed:
                om.proposed = True
                self._out.on_match_proposed(match, late=late)
            elif blocked and om.proposed:
                om.proposed = False
                blocker = next(iter(blockers.values()))
                self._out.on_match_revoked(
                    match, cause="c_between", late=late, blocker=blocker)

    # -------------------------------------------------------------- watermark

    def advance(self, new_w: int) -> None:
        if self.watermark is not None and new_w < self.watermark:
            raise ValueError("watermark cannot move backwards: %d < %d"
                             % (new_w, self.watermark))
        self.watermark = new_w

        # Finalize: B@b sealed once new_w > b.
        for key, open_ in list(self._open.items()):
            for pair in [p for p, om in open_.items()
                         if om.match.b.time < new_w]:
                om = open_.pop(pair)
                self._finalized[key].add(pair)
                if om.proposed:
                    self._out.on_match_final(om.match)
                # A blocked match that never existed as a result is forgotten.

        # Prune state that can no longer participate in any open match.
        for key, events in list(self._as.items()):
            for rid in [rid for rid, ev in events.items()
                        if ev.time + self.win < new_w]:
                del events[rid]
        for key, events in list(self._cs.items()):
            for rid in [rid for rid, ev in events.items()
                        if ev.time + self.win < new_w]:
                del events[rid]
        for key, events in list(self._bs.items()):
            for rid in [rid for rid, ev in events.items()
                        if ev.time < new_w]:
                del events[rid]
        for bucket in (self._as, self._bs, self._cs, self._open):
            for key in [k for k, v in bucket.items() if not v]:
                del bucket[key]

    # ------------------------------------------------------------- snapshot

    def stats(self) -> Dict[str, int]:
        return {
            "watermark": -1 if self.watermark is None else self.watermark,
            "pending_proposed": sum(
                1 for open_ in self._open.values()
                for om in open_.values() if om.proposed),
            "tracked_open_matches": sum(len(v) for v in self._open.values()),
            "live_events": (sum(len(v) for v in self._as.values())
                            + sum(len(v) for v in self._bs.values())
                            + sum(len(v) for v in self._cs.values())),
            "finalized": sum(len(v) for v in self._finalized.values()),
        }


def _between(a: Event, c: Event, b: Event) -> bool:
    """True iff C is strictly after A and strictly before B in total order."""
    return _sort_key(a) < _sort_key(c) < _sort_key(b)


def feed_auto_watermarks(matcher: StreamMatcher,
                         events: Iterable[Event],
                         max_out_of_orderness: int = 0) -> None:
    """Feed events in arrival order with a processing-time-style watermark.

    W after each event = max(event time seen) - max_out_of_orderness.
    Deterministic reference driver for arrival-order experiments.
    """
    max_t: Optional[int] = None
    for ev in events:
        matcher.push(ev)
        max_t = ev.time if max_t is None else max(max_t, ev.time)
        w = max_t - max_out_of_orderness
        cur = matcher.watermark
        if cur is None or w > cur:
            matcher.advance(w)
