"""Three-valued online monitoring of finite temporal rules.

Rules are given by an explicit alphabet and transition structure (a prefix
trie shared by rules with common prefixes); no regex or automata libraries
are used.  Each trigger event opens an obligation whose verdict is one of
PENDING / SATISFIED / VIOLATED.  Verdicts are recomputed deterministically
from the retained, time-sorted event store, so out-of-order arrivals and
retractions revise previously published verdicts; every published version
is retained.  Only a watermark past the deadline or termination closes an
obligation.  State is bounded by a lateness window and a retention budget;
when data cannot be retained the monitor raises BackpressureError instead
of silently dropping it (a budget abort is never recorded as a violation).
"""

from __future__ import annotations

from dataclasses import dataclass, field
from enum import Enum


class BackpressureError(RuntimeError):
    """Raised when an event cannot be retained within the configured budget."""


class Verdict(Enum):
    PENDING = "PENDING"
    SATISFIED = "SATISFIED"
    VIOLATED = "VIOLATED"


@dataclass(frozen=True)
class Event:
    eid: str
    symbol: str
    ts: float

    def to_list(self):
        return [self.eid, self.symbol, self.ts]

    @staticmethod
    def from_list(raw):
        return Event(raw[0], raw[1], float(raw[2]))


@dataclass(frozen=True)
class Rule:
    """A finite temporal rule: match ``path`` in order within ``bound`` of the
    trigger (path[0]), with no ``forbid`` symbol before the match completes.

    ``consume`` defines whether one event may satisfy several obligations of
    this rule (False) or is consumed by the oldest open obligation (True).
    """

    name: str
    path: tuple
    bound: float
    forbid: frozenset
    consume: bool = False

    @property
    def symbols(self):
        return frozenset(self.path) | self.forbid


class _Node:
    __slots__ = ("children", "rules")

    def __init__(self):
        self.children = {}
        self.rules = []


class RuleTrie:
    """Explicitly compiled transition structure; rules sharing a prefix share nodes."""

    def __init__(self, alphabet):
        self.alphabet = frozenset(alphabet)
        self._root = _Node()
        self.rules = {}

    def add_rule(self, name, path, bound, forbid=(), consume=False):
        path = tuple(path)
        if len(path) < 2:
            raise ValueError("rule path needs at least a trigger and one follow symbol")
        unknown = (set(path) | set(forbid)) - self.alphabet
        if unknown:
            raise ValueError(f"symbols not in alphabet: {sorted(unknown)}")
        if set(forbid) & set(path):
            raise ValueError("forbid symbols must not appear on the path")
        if bound <= 0:
            raise ValueError("bound must be positive")
        if name in self.rules:
            raise ValueError(f"duplicate rule name: {name}")
        rule = Rule(name, path, float(bound), frozenset(forbid), bool(consume))
        node = self._root
        for symbol in path:
            node = node.children.setdefault(symbol, _Node())
        node.rules.append(name)
        self.rules[name] = rule
        return rule

    def transitions(self):
        """Explicit transition table: (prefix, symbol) -> extended prefix."""
        table = {}
        stack = [((), self._root)]
        while stack:
            prefix, node = stack.pop()
            for symbol, child in node.children.items():
                table[(prefix, symbol)] = prefix + (symbol,)
                stack.append((prefix + (symbol,), child))
        return table

    def rules_opened_by(self, symbol):
        return [name for name, rule in self.rules.items() if rule.path[0] == symbol]


@dataclass(frozen=True)
class Witness:
    """Shortest violation evidence; independently re-executable via replay_witness."""

    rule: str
    kind: str  # "forbidden" | "deadline"
    events: tuple
    deadline: float
    closed: bool

    def to_dict(self):
        return {
            "rule": self.rule,
            "kind": self.kind,
            "events": [e.to_list() for e in self.events],
            "deadline": self.deadline,
            "closed": self.closed,
        }

    @staticmethod
    def from_dict(raw):
        if raw is None:
            return None
        return Witness(
            raw["rule"],
            raw["kind"],
            tuple(Event.from_list(e) for e in raw["events"]),
            float(raw["deadline"]),
            bool(raw["closed"]),
        )


def replay_witness(rule, witness):
    """Independently re-execute a witness against a rule spec (no monitor state).

    Returns True iff the witness events reproduce a violation of ``rule``.
    """
    if witness.rule != rule.name or not witness.events:
        return False
    trigger = witness.events[0]
    if trigger.symbol != rule.path[0]:
        return False
    if abs(witness.deadline - (trigger.ts + rule.bound)) > 1e-9:
        return False
    pos = 1
    for event in witness.events[1:]:
        if event.ts > witness.deadline:
            return False
        if event.symbol in rule.forbid:
            return witness.kind == "forbidden"
        if pos < len(rule.path) and event.symbol == rule.path[pos]:
            pos += 1
            if pos == len(rule.path):
                return False  # the sequence satisfies the rule
    if pos == len(rule.path):
        return False
    return witness.kind == "deadline" and witness.closed


@dataclass
class VerdictRecord:
    seq: int
    obligation_id: str
    rule: str
    version: int
    verdict: Verdict
    final: bool
    reason: str
    witness: Witness | None

    def to_dict(self):
        return {
            "seq": self.seq,
            "obligation_id": self.obligation_id,
            "rule": self.rule,
            "version": self.version,
            "verdict": self.verdict.value,
            "final": self.final,
            "reason": self.reason,
            "witness": self.witness.to_dict() if self.witness else None,
        }

    @staticmethod
    def from_dict(raw):
        return VerdictRecord(
            raw["seq"],
            raw["obligation_id"],
            raw["rule"],
            raw["version"],
            Verdict(raw["verdict"]),
            bool(raw["final"]),
            raw["reason"],
            Witness.from_dict(raw["witness"]),
        )


@dataclass
class Obligation:
    oid: str
    rule: str
    trigger: Event
    deadline: float
    version: int = 0
    verdict: Verdict = Verdict.PENDING
    final: bool = False
    witness: Witness | None = None
    sealed: bool = False
    history: list = field(default_factory=list)


class Monitor:
    def __init__(self, trie, *, lateness, max_events):
        self.trie = trie
        self.lateness = float(lateness)
        self.max_events = int(max_events)
        self.watermark = None  # None means "no watermark seen yet"
        self.terminated = False
        self._seen = set()  # dedup index: every eid ever accepted
        self._rule_events = {name: {} for name in trie.rules}  # rule -> {eid: Event}
        self._retraction_index = {}  # eid -> set of rule names holding the event
        self._obligations = {}
        self.verdict_log = []  # all published versions, superseded ones retained
        self._seq = 0

    # ------------------------------------------------------------------ input

    def ingest(self, event):
        if self.terminated:
            raise RuntimeError("monitor is terminated")
        if event.symbol not in self.trie.alphabet:
            raise ValueError(f"symbol not in alphabet: {event.symbol}")
        if event.eid in self._seen:
            return []  # duplicate: never opens a new obligation
        if self._too_late(event.ts):
            raise BackpressureError(
                f"event {event.eid} ts={event.ts} is beyond the lateness window "
                f"(watermark={self.watermark}, lateness={self.lateness})"
            )
        relevant = [
            name for name, rule in self.trie.rules.items() if event.symbol in rule.symbols
        ]
        if relevant and len(self._retraction_index) + 1 > self.max_events:
            raise BackpressureError(
                f"retention budget exhausted ({self.max_events} events); "
                "advance the watermark or enlarge max_events"
            )
        self._seen.add(event.eid)
        affected = set()
        for name in relevant:
            self._rule_events[name][event.eid] = event
            self._retraction_index.setdefault(event.eid, set()).add(name)
            affected.add(name)
        for name in self.trie.rules_opened_by(event.symbol):
            rule = self.trie.rules[name]
            oid = f"{name}:{event.eid}"
            self._obligations[oid] = Obligation(oid, name, event, event.ts + rule.bound)
            affected.add(name)
        return self._recompute(affected)

    def retract(self, eid):
        if eid not in self._seen:
            return False
        self._seen.discard(eid)
        affected = set(self._retraction_index.pop(eid, ()))
        for name in affected:
            self._rule_events[name].pop(eid, None)
        for oid in [oid for oid, ob in self._obligations.items() if ob.trigger.eid == eid]:
            affected.add(self._obligations[oid].rule)
            del self._obligations[oid]  # published versions stay in the log
        if affected:
            self._recompute(affected)
        return True

    # ------------------------------------------------------- watermark / close

    def advance_watermark(self, ts):
        if self.terminated:
            raise RuntimeError("monitor is terminated")
        ts = float(ts)
        if self.watermark is not None and ts < self.watermark:
            raise ValueError("watermark must be monotone")
        self.watermark = ts
        published = self._recompute(self.trie.rules.keys())
        self._evict()
        return published

    def close(self):
        if self.terminated:
            return []
        self.terminated = True
        return self._recompute(self.trie.rules.keys())

    # ------------------------------------------------------------- evaluation

    def _too_late(self, ts):
        return self.watermark is not None and ts <= self.watermark - self.lateness

    def _is_closed(self, obligation):
        return self.terminated or (
            self.watermark is not None and self.watermark > obligation.deadline
        )

    def _recompute(self, rule_names):
        published = []
        for name in rule_names:
            rule = self.trie.rules[name]
            events = sorted(self._rule_events[name].values(), key=lambda e: (e.ts, e.eid))
            obligations = sorted(
                (
                    ob
                    for ob in self._obligations.values()
                    if ob.rule == name and not ob.sealed
                ),
                key=lambda ob: (ob.trigger.ts, ob.trigger.eid),
            )
            consumed = set()  # only used when rule.consume is True
            for obligation in obligations:
                record = self._evaluate(rule, obligation, events, consumed)
                if record is not None:
                    published.append(record)
        return published

    def _evaluate(self, rule, obligation, events, consumed):
        path = rule.path
        pos = 1  # trigger already matched path[0]
        progress = [obligation.trigger]
        verdict = Verdict.PENDING
        witness = None
        reason = f"awaiting '{path[pos]}' before t={obligation.deadline}"
        closed = self._is_closed(obligation)
        trigger_key = (obligation.trigger.ts, obligation.trigger.eid)
        for event in events:
            if (event.ts, event.eid) <= trigger_key:
                continue
            if event.ts > obligation.deadline:
                break
            if rule.consume and event.eid in consumed:
                continue
            if event.symbol in rule.forbid:
                verdict = Verdict.VIOLATED
                witness = Witness(
                    rule.name, "forbidden", tuple(progress + [event]), obligation.deadline, closed
                )
                reason = f"forbidden '{event.symbol}' at t={event.ts}"
                break
            if pos < len(path) and event.symbol == path[pos]:
                progress.append(event)
                if rule.consume:
                    consumed.add(event.eid)
                pos += 1
                if pos == len(path):
                    verdict = Verdict.SATISFIED
                    reason = "matched " + "->".join(path)
                    break
        if verdict is Verdict.PENDING and closed:
            verdict = Verdict.VIOLATED
            witness = Witness(
                rule.name, "deadline", (obligation.trigger,), obligation.deadline, True
            )
            reason = f"no match before deadline t={obligation.deadline}"
        final = closed and (
            self.terminated or self.watermark > obligation.deadline + self.lateness
        )
        if final:
            obligation.sealed = True
        if (
            obligation.version == 0
            or verdict is not obligation.verdict
            or final != obligation.final
        ):
            obligation.version += 1
            obligation.verdict = verdict
            obligation.final = final
            obligation.witness = witness
            record = VerdictRecord(
                self._seq, obligation.oid, obligation.rule, obligation.version,
                verdict, final, reason, witness,
            )
            self._seq += 1
            obligation.history.append(record)
            self.verdict_log.append(record)
            return record
        return None

    def _evict(self):
        horizon = self.watermark - self.lateness
        for name, store in self._rule_events.items():
            for eid in [eid for eid, e in store.items() if e.ts <= horizon]:
                del store[eid]
                holders = self._retraction_index.get(eid)
                if holders is not None:
                    holders.discard(name)
                    if not holders:
                        del self._retraction_index[eid]

    # ----------------------------------------------------------------- queries

    @property
    def obligations(self):
        return dict(self._obligations)

    def obligation(self, oid):
        return self._obligations[oid]

    def verdicts(self):
        return {oid: (ob.verdict, ob.final) for oid, ob in self._obligations.items()}

    # ------------------------------------------------------------- checkpoint

    def snapshot(self):
        """JSON-serializable checkpoint: pending obligations, retraction index, log."""
        return {
            "watermark": self.watermark,
            "terminated": self.terminated,
            "lateness": self.lateness,
            "max_events": self.max_events,
            "seen": sorted(self._seen),
            "rule_events": {
                name: [
                    e.to_list()
                    for e in sorted(store.values(), key=lambda e: (e.ts, e.eid))
                ]
                for name, store in self._rule_events.items()
            },
            "retraction_index": {
                eid: sorted(names) for eid, names in sorted(self._retraction_index.items())
            },
            "obligations": [
                {
                    "oid": ob.oid,
                    "rule": ob.rule,
                    "trigger": ob.trigger.to_list(),
                    "deadline": ob.deadline,
                    "version": ob.version,
                    "verdict": ob.verdict.value,
                    "final": ob.final,
                    "sealed": ob.sealed,
                    "witness": ob.witness.to_dict() if ob.witness else None,
                    "history": [rec.to_dict() for rec in ob.history],
                }
                for ob in sorted(self._obligations.values(), key=lambda ob: ob.oid)
            ],
            "verdict_log": [rec.to_dict() for rec in self.verdict_log],
            "seq": self._seq,
        }

    @classmethod
    def restore(cls, trie, snap):
        monitor = cls(trie, lateness=snap["lateness"], max_events=snap["max_events"])
        monitor.watermark = snap["watermark"]
        monitor.terminated = bool(snap["terminated"])
        monitor._seen = set(snap["seen"])
        monitor._rule_events = {
            name: {raw[0]: Event.from_list(raw) for raw in entries}
            for name, entries in snap["rule_events"].items()
        }
        for name in trie.rules:
            monitor._rule_events.setdefault(name, {})
        monitor._retraction_index = {
            eid: set(names) for eid, names in snap["retraction_index"].items()
        }
        for raw in snap["obligations"]:
            obligation = Obligation(
                raw["oid"],
                raw["rule"],
                Event.from_list(raw["trigger"]),
                float(raw["deadline"]),
                version=raw["version"],
                verdict=Verdict(raw["verdict"]),
                final=bool(raw["final"]),
                witness=Witness.from_dict(raw["witness"]),
                sealed=bool(raw["sealed"]),
                history=[VerdictRecord.from_dict(rec) for rec in raw["history"]],
            )
            monitor._obligations[obligation.oid] = obligation
        monitor.verdict_log = [VerdictRecord.from_dict(rec) for rec in snap["verdict_log"]]
        monitor._seq = snap["seq"]
        return monitor
