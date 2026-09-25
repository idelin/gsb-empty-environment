"""Shared-rule DAG maintenance of inclusion-minimal fact support sets.

Core concepts
-------------
- Facts carry a stable id, a version and an evidence cost.
- Rules carry a stable id, a version, a head goal and a disjunction of
  alternatives; each alternative is a conjunction of literals:
  positive facts, sub-goals and explicit negated facts.
- A *support* for a goal is a pair (pos, neg): facts that must be present
  (pinned to the version used as evidence) and facts that must be absent.
  Only inclusion-minimal supports are kept: {A} dominates {A, B}.
- Facts, rules and derivation steps all carry stable ids and versions so
  evidence can be replayed by the independent verifier (see verifier.py).
- Fact retraction / version bumps and rule version changes incrementally
  invalidate exactly the dependent goals through a reverse dependency index.
- Ungrounded cycles never derive facts: recursion cuts a goal already on
  the current derivation path, and tainted (cycle-cut) results are never
  cached.

Only the Python standard library is used.
"""

from __future__ import annotations

import heapq
from dataclasses import dataclass, field

FACT = "fact"
GOAL = "goal"
NEGFACT = "negfact"


# --------------------------------------------------------------------------
# Model
# --------------------------------------------------------------------------

@dataclass(frozen=True)
class Fact:
    fact_id: str
    version: int
    cost: float = 1.0


@dataclass(frozen=True)
class Lit:
    kind: str  # FACT | GOAL | NEGFACT
    ref: str


def fact_lit(ref: str) -> Lit:
    return Lit(FACT, ref)


def goal_lit(ref: str) -> Lit:
    return Lit(GOAL, ref)


def neg_lit(ref: str) -> Lit:
    return Lit(NEGFACT, ref)


@dataclass(frozen=True)
class Alt:
    """One alternative: a conjunction of literals (a rule may have several)."""
    alt_id: str
    lits: tuple


@dataclass(frozen=True)
class Rule:
    rule_id: str
    head: str
    version: int
    alts: tuple


@dataclass(frozen=True, order=True)
class FactRef:
    """A fact pinned to the exact version used as evidence."""
    fact_id: str
    version: int


@dataclass(frozen=True)
class Support:
    """pos: facts that must hold; neg: fact ids that must be absent."""
    pos: frozenset  # of FactRef
    neg: frozenset  # of str

    def dominates(self, other: "Support") -> bool:
        """Inclusion dominance: self is a (non-strict) subset on both parts."""
        return self.pos <= other.pos and self.neg <= other.neg

    def to_json(self) -> dict:
        return {
            "pos": sorted([f.fact_id, f.version] for f in self.pos),
            "neg": sorted(self.neg),
        }

    @staticmethod
    def from_json(data: dict) -> "Support":
        return Support(
            frozenset(FactRef(fid, ver) for fid, ver in data["pos"]),
            frozenset(data["neg"]),
        )


def _merge(left: Support, right: Support):
    """Merge two supports; None on a pos/neg contradiction."""
    pos = left.pos | right.pos
    neg = left.neg | right.neg
    if {f.fact_id for f in pos} & neg:
        return None
    return Support(pos, neg)


def _minimal(supports):
    """Keep only inclusion-minimal supports (drops e.g. {A,B} when {A} exists)."""
    uniq = []
    seen = set()
    for sup in supports:
        key = (sup.pos, sup.neg)
        if key not in seen:
            seen.add(key)
            uniq.append(sup)
    return [
        sup for sup in uniq
        if not any(other is not sup and other.dominates(sup) for other in uniq)
    ]


# --------------------------------------------------------------------------
# Engine: derivation + incremental invalidation
# --------------------------------------------------------------------------

class _Entry:
    __slots__ = ("supports", "deps")

    def __init__(self, supports, deps):
        self.supports = supports
        self.deps = deps


class Engine:
    """Maintains minimal support sets for goals over a shared rule DAG."""

    def __init__(self):
        self.facts: dict[str, Fact] = {}
        self.rules: dict[str, Rule] = {}
        self._by_head: dict[str, list[str]] = {}
        self._cache: dict[str, _Entry] = {}
        self._dependents: dict[tuple, set[str]] = {}

    # -- inspection helpers (also used by the verifier / enumerator) --

    def rule_ids_for(self, head: str) -> list[str]:
        return sorted(self._by_head.get(head, ()))

    def alternatives(self, goal: str) -> list[tuple[str, str]]:
        return [
            (rid, alt.alt_id)
            for rid in self._by_head.get(goal, ())
            for alt in self.rules[rid].alts
        ]

    def stamp(self) -> dict:
        """JSON-native fingerprint of every fact/rule id + version."""
        return {
            "facts": {fid: [f.version, f.cost] for fid, f in sorted(self.facts.items())},
            "rules": {rid: r.version for rid, r in sorted(self.rules.items())},
        }

    # -- mutations (each triggers incremental invalidation) --

    def add_fact(self, fact: Fact) -> None:
        self.facts[fact.fact_id] = fact
        self._invalidate(("fact", fact.fact_id))

    def retract_fact(self, fact_id: str) -> None:
        self.facts.pop(fact_id, None)
        self._invalidate(("fact", fact_id))

    def put_rule(self, rule: Rule) -> None:
        old = self.rules.get(rule.rule_id)
        if old is not None:
            self._by_head[old.head] = [
                r for r in self._by_head[old.head] if r != rule.rule_id
            ]
        self.rules[rule.rule_id] = rule
        bucket = self._by_head.setdefault(rule.head, [])
        if rule.rule_id not in bucket:
            bucket.append(rule.rule_id)
        self._invalidate(("rule", rule.rule_id))
        self._invalidate(("head", rule.head))

    def retract_rule(self, rule_id: str) -> None:
        old = self.rules.pop(rule_id, None)
        if old is None:
            return
        self._by_head[old.head] = [
            r for r in self._by_head[old.head] if r != rule_id
        ]
        self._invalidate(("rule", rule_id))
        self._invalidate(("head", old.head))

    def _invalidate(self, key) -> None:
        """Evict every cached goal that (transitively) depends on `key`."""
        queue = [key]
        seen = set()
        while queue:
            cur = queue.pop()
            if cur in seen:
                continue
            seen.add(cur)
            for goal in self._dependents.pop(cur, ()):
                entry = self._cache.pop(goal, None)
                if entry is None:
                    continue
                for dep in entry.deps:
                    bucket = self._dependents.get(dep)
                    if bucket is not None:
                        bucket.discard(goal)
                queue.append(("goal", goal))

    # -- support computation --

    def supports_of(self, goal: str) -> list[Support]:
        supports, _tainted, _deps = self._supports(goal, frozenset())
        return list(supports)

    def alt_supports(self, rule_id: str, alt_id: str) -> list[Support]:
        """Minimal supports of a single alternative (used by the enumerator)."""
        rule = self.rules[rule_id]
        alt = next(a for a in rule.alts if a.alt_id == alt_id)
        deps: set = set()
        tainted: set = set()
        raw = self._alt_supports(alt, frozenset({rule.head}), deps, tainted)
        return _minimal(list(raw))

    def _supports(self, goal: str, visiting: frozenset):
        """Return (supports, tainted, deps_for_parent).

        `tainted` holds ancestors that were cut because of a cycle; results
        computed while any taint is pending are never cached.  `deps_for_parent`
        is the full transitive dependency set when this goal was not cached
        (so parents can still be invalidated correctly), otherwise just the
        ("goal", goal) edge into the cache.
        """
        if goal in visiting:
            return [], frozenset({goal}), frozenset()
        cached = self._cache.get(goal)
        if cached is not None:
            return cached.supports, frozenset(), frozenset({("goal", goal)})

        deps = {("head", goal)}
        tainted: set = set()
        combined: list[Support] = []
        for rid in list(self._by_head.get(goal, ())):
            rule = self.rules[rid]
            deps.add(("rule", rid))
            for alt in rule.alts:
                combined.extend(self._alt_supports(alt, visiting | {goal}, deps, tainted))
        supports = _minimal(combined)

        if goal in tainted:
            tainted.discard(goal)
            return supports, frozenset(tainted), frozenset(deps)
        if tainted:
            return supports, frozenset(tainted), frozenset(deps)
        self._store(goal, supports, deps)
        return supports, frozenset(), frozenset({("goal", goal)})

    def _alt_supports(self, alt: Alt, visiting: frozenset, deps: set, tainted: set):
        partial = [Support(frozenset(), frozenset())]
        for lit in alt.lits:
            if lit.kind == FACT:
                deps.add(("fact", lit.ref))
                fact = self.facts.get(lit.ref)
                if fact is None:
                    return
                step = [Support(frozenset({FactRef(fact.fact_id, fact.version)}), frozenset())]
            elif lit.kind == NEGFACT:
                deps.add(("fact", lit.ref))
                if lit.ref in self.facts:
                    return
                step = [Support(frozenset(), frozenset({lit.ref}))]
            else:
                deps.add(("goal", lit.ref))
                step, taint, sub_deps = self._supports(lit.ref, visiting)
                tainted |= taint
                deps |= sub_deps
                if not step:
                    return
            merged = []
            for base in partial:
                for extra in step:
                    combo = _merge(base, extra)
                    if combo is not None:
                        merged.append(combo)
            partial = _minimal(merged)
            if not partial:
                return
        yield from partial

    def _store(self, goal: str, supports, deps) -> None:
        self._cache[goal] = _Entry(supports, deps)
        for dep in deps:
            self._dependents.setdefault(dep, set()).add(goal)


# --------------------------------------------------------------------------
# Lazy cost-ordered enumeration with budget, truncation and pause/resume
# --------------------------------------------------------------------------

@dataclass
class EnumerationResult:
    goal: str
    supports: list
    complete: bool
    reason: str  # "exhausted" | "budget" | "in-progress"


class SupportEnumerator:
    """Yields inclusion-minimal supports in ascending evidence-cost order.

    Laziness: alternatives are expanded one at a time and merged through a
    heap; with non-negative fact costs a subset always surfaces before its
    supersets, so a popped candidate is minimal iff no already-yielded
    support dominates it.  Budgets (`max_results`, `max_cost`) truncate the
    stream and make the final result report ``complete=False`` instead of
    silently omitting supports.  `checkpoint()` / `resume()` serialize the
    whole state so enumeration can be paused and resumed later; resume
    refuses to continue if any fact/rule version changed meanwhile.
    """

    def __init__(self, engine: Engine, goal: str,
                 max_results: int | None = None, max_cost: float | None = None):
        self._engine = engine
        self._goal = goal
        self._max_results = max_results
        self._max_cost = max_cost
        self._alt_keys = engine.alternatives(goal)
        self._lists: dict[tuple, list] = {}
        self._pos: dict[tuple, int] = {}
        self._heap: list = []
        self._yielded: list[Support] = []
        self._started = False
        self._exhausted = False
        self._truncated = False

    # -- internals --

    def _cost(self, sup: Support) -> float:
        return sum(self._engine.facts[f.fact_id].cost for f in sup.pos)

    @staticmethod
    def _content_key(sup: Support):
        return (
            tuple(sorted((f.fact_id, f.version) for f in sup.pos)),
            tuple(sorted(sup.neg)),
        )

    def _sort_key(self, sup: Support):
        return (self._cost(sup), self._content_key(sup))

    def _push(self, key) -> None:
        idx = self._pos[key]
        lst = self._lists[key]
        if idx < len(lst):
            sup = lst[idx]
            heapq.heappush(self._heap, (self._cost(sup), self._content_key(sup), key))

    def _start(self) -> None:
        self._started = True
        for key in self._alt_keys:
            lst = self._engine.alt_supports(*key)
            lst.sort(key=self._sort_key)
            self._lists[key] = lst
            self._pos[key] = 0
            self._push(key)

    # -- public API --

    def next(self):
        """Return the next cheapest minimal support, or None."""
        if not self._started:
            self._start()
        while self._heap:
            if self._max_results is not None and len(self._yielded) >= self._max_results:
                self._truncated = True
                return None
            cost, _content, key = heapq.heappop(self._heap)
            if self._max_cost is not None and cost > self._max_cost:
                self._truncated = True
                return None
            idx = self._pos[key]
            self._pos[key] = idx + 1
            self._push(key)
            cand = self._lists[key][idx]
            if any(prev.dominates(cand) for prev in self._yielded):
                continue
            self._yielded.append(cand)
            return cand
        self._exhausted = True
        return None

    def result(self) -> EnumerationResult:
        complete = self._exhausted and not self._truncated
        if complete:
            reason = "exhausted"
        elif self._truncated:
            reason = "budget"
        else:
            reason = "in-progress"
        return EnumerationResult(self._goal, list(self._yielded), complete, reason)

    def checkpoint(self) -> dict:
        """Serialize the full enumerator state (JSON-safe)."""
        return {
            "goal": self._goal,
            "max_results": self._max_results,
            "max_cost": self._max_cost,
            "started": self._started,
            "positions": {f"{rid}/{aid}": self._pos.get((rid, aid), 0)
                          for rid, aid in self._alt_keys},
            "yielded": [s.to_json() for s in self._yielded],
            "truncated": self._truncated,
            "stamp": self._engine.stamp(),
        }

    @classmethod
    def resume(cls, engine: Engine, state: dict) -> "SupportEnumerator":
        """Rebuild an enumerator from a checkpoint; refuses stale evidence."""
        if state["stamp"] != engine.stamp():
            raise ValueError(
                "engine state changed since checkpoint; enumeration must restart"
            )
        en = cls(engine, state["goal"], state["max_results"], state["max_cost"])
        en._yielded = [Support.from_json(s) for s in state["yielded"]]
        en._truncated = state["truncated"]
        if state["started"]:
            en._started = True
            for key in en._alt_keys:
                lst = engine.alt_supports(*key)
                lst.sort(key=en._sort_key)
                en._lists[key] = lst
                en._pos[key] = state["positions"][f"{key[0]}/{key[1]}"]
                en._push(key)
        return en
