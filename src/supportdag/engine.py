"""Lazy enumeration engine for inclusion-minimal fact-support sets.

Design
------
For a fixed repository revision (an immutable :class:`Snapshot`) the engine
builds a shared DAG of *atom streams*.  Each stream lazily emits raw candidate
proofs for one atom in non-decreasing evidence cost:

* a :class:`FactAlt` emits the base fact (if any);
* a :class:`RuleAlt` enumerates the Cartesian product of the streams of the
  positive premises of one rule alternative (k-best style, with a heap),
  attaching the rule step;
* the alternatives of an atom (fact + every rule that derives it) are merged
  by a heap so that the global atom stream is also cost-ordered.

Rootless positive cycles are rejected structurally: when a rule alternative
still needs to be constructed and its atom re-enters construction, the stream
raises :class:`_Cyclic` and that alternative is dropped; a fact-backed stream
answers immediately, so grounded cycles still terminate.

A :class:`Query` drives the root stream.  Candidates are emitted strictly in
cost order.  A candidate is accepted only when the independent WFS check
confirms it entails the goal; and it is included only when no already accepted
set dominates it (is a strict subset).  Because order is by cost, dominance is
detected at the earliest possible moment.  Pulls can be budget-limited
(evicted candidates / derivation cells consumed), paused across calls, resumed,
and report ``complete`` versus ``truncated`` honestly -- a truncated query never
claims completeness.
"""

from __future__ import annotations

import heapq
from dataclasses import dataclass, field
from typing import Dict, FrozenSet, List, Mapping, Optional, Sequence, Set, Tuple

from .model import Fact, Rule, atom_of, is_negative, premise_to_json
from .semantics import entails, rule_step_id, support_id, wfs_true

FactIdSet = FrozenSet[str]


# --------------------------------------------------------------------------- #
# Derivation records
# --------------------------------------------------------------------------- #
@dataclass(frozen=True)
class StepRecord:
    step_id: str
    kind: str  # "fact" | "rule"
    atom: str
    rule_id: Optional[str] = None
    rule_version: Optional[int] = None
    children: Tuple[str, ...] = ()
    premises: Tuple[dict, ...] = ()

    def to_json(self) -> dict:
        return {
            "step_id": self.step_id,
            "kind": self.kind,
            "atom": self.atom,
            "rule_id": self.rule_id,
            "rule_version": self.rule_version,
            "children": list(self.children),
            "premises": list(self.premises),
        }


@dataclass(frozen=True)
class Cand:
    """A raw candidate proof for one atom."""

    atom: str
    facts: FactIdSet
    cost: int
    root_step: str
    steps: Mapping[str, StepRecord]

    def merge(self, other: "Cand") -> "Cand":
        if self.atom != other.atom:
            raise ValueError("can only merge candidates for the same atom")
        facts = self.facts | other.facts
        steps = dict(self.steps)
        steps.update(other.steps)
        return Cand(self.atom, facts, self.cost + other.cost, self.root_step, steps)


# --------------------------------------------------------------------------- #
# Snapshot: immutable, revision-stamped view over the repository content
# --------------------------------------------------------------------------- #
@dataclass(frozen=True)
class Snapshot:
    revision: int
    facts: Tuple[Fact, ...]
    rules: Tuple[Rule, ...]
    rules_for_head: Mapping[str, Tuple[Rule, ...]]
    fact_for_atom: Mapping[str, Fact]
    fact_atom_for_id: Mapping[str, str]
    fact_cost: Mapping[str, int]
    step_cache: dict = field(default_factory=dict)

    def wfs(self, base_atoms: FrozenSet[str]) -> FrozenSet[str]:
        return wfs_true(base_atoms, self.rules)

    def entails(self, base_atoms: FrozenSet[str], goal: str) -> bool:
        return entails(base_atoms, self.rules, goal)

    def current_fact_atoms(self) -> FrozenSet[str]:
        return frozenset(self.fact_for_atom)

    def closure_with_current_world(self, support_atoms: FrozenSet[str]) -> FrozenSet[str]:
        """WFS closure with support facts plus all other currently known facts.

        Used only to test *negative premises*: ``NOT a`` is admissible just
        when ``a`` is not derivable in the knowledge base as it currently
        stands.  Adding a fact therefore defeats the negative premise and the
        dependent goal is re-enumerated incrementally.  Goal sufficiency and
        irredundancy themselves stay self-contained (see ``Query.pull``).
        """
        world = self.current_fact_atoms() | frozenset(support_atoms)
        return wfs_true(world, self.rules)


class _Cyclic(Exception):
    """Raised while a rootless positive cycle is being constructed."""


# --------------------------------------------------------------------------- #
# One way to prove an atom: a base fact, or one alternative of one rule.
# --------------------------------------------------------------------------- #
class FactAlt:
    def __init__(self, fact: Fact, cost: int):
        self._fact = fact
        self._cost = cost
        self._step = StepRecord(fact.step_id(), "fact", fact.atom)
        self._emitted = False
        self.dead = False

    def current(self) -> Optional[Cand]:
        if self._emitted:
            return None
        return Cand(
            self._fact.atom,
            frozenset({self._fact.fid}),
            self._cost,
            self._step.step_id,
            {self._step.step_id: self._step},
        )

    def pop(self) -> Cand:
        self._emitted = True
        self.dead = True
        return Cand(
            self._fact.atom,
            frozenset({self._fact.fid}),
            self._cost,
            self._step.step_id,
            {self._step.step_id: self._step},
        )


class RuleAlt:
    """Lazy k-best enumeration over the positive-premise atom streams.

    A heap cell holds, for each positive premise in order, the index of the
    candidate currently chosen from that premise stream.  When a cell is
    consumed, its successors (advance exactly one premise by one candidate) are
    pushed; the deduplicating ``_seen`` set avoids double pushes.
    """

    def __init__(self, builder, atom, rule, alt, self_witness=None):
        self._builder = builder
        self._atom = atom
        self._rule = rule
        self._alt = alt
        # A self premise (head appearing positively in its own body) is not
        # part of the Cartesian product: its only useful witness is the
        # head's own base fact, supplied as ``self_witness``.
        self._self_witness = self_witness
        self._pos_atoms: List[str] = [
            lit for lit in alt if not is_negative(lit) and lit != atom
        ]
        self._has_self = any(
            (not is_negative(lit)) and lit == atom for lit in alt
        )
        self._neg_atoms: List[str] = [atom_of(lit) for lit in alt if is_negative(lit)]
        self._streams: List["AtomStream"] = []
        self._heap: List[Tuple[int, int, Tuple[int, ...]]] = []
        self._seen: Set[Tuple[int, ...]] = set()
        self._tie = 0
        self.dead = False
        self._initial_failed = False
        self._init_streams()

    def _init_streams(self) -> None:
        try:
            for a in self._pos_atoms:
                self._streams.append(self._builder.get_stream(a))
        except _Cyclic:
            # Still-active transitive dependency with no grounding fact.
            self._initial_failed = True
            return
        self._prime()

    def _prime(self) -> None:
        if self._neg_blocked_empty():
            self.dead = True
            return
        if not self._pos_atoms:
            # Pure-negative rule: fires with the empty fact set.
            self._push_cell(())
            return
        try:
            # Index-based access (not a shared cursor): every consumer of a
            # stream walks its own materialized-candidate indices.
            if all(stream.materialize(0) is not None for stream in self._streams):
                self._push_cell(tuple(0 for _ in self._streams))
            else:
                self.dead = True
        except _Cyclic:
            self._initial_failed = True

    def _neg_blocked_empty(self) -> bool:
        # Cheap static prune: if the negative atom already follows from the
        # current world, no subset of it can make NOT hold.
        closure = self._builder.snapshot.closure_with_current_world(frozenset())
        return any(neg in closure for neg in self._neg_atoms)

    def _push_cell(self, idxs: Tuple[int, ...]) -> None:
        if idxs in self._seen:
            return
        union_facts: Set[str] = set()
        for stream, idx in zip(self._streams, idxs):
            cand = stream.materialize(idx)
            if cand is None:
                return
            union_facts.update(cand.facts)
        cost = sum(self._builder.snapshot.fact_cost.get(fid, 1) for fid in union_facts)
        self._seen.add(idxs)
        heapq.heappush(self._heap, (cost, self._tie, idxs))
        self._tie += 1

    def current(self) -> Optional[Cand]:
        self._advance_to_viable()
        if not self._heap:
            return None
        _, _, idxs = self._heap[0]
        return self._build_cand(idxs)

    def pop(self) -> Optional[Cand]:
        self._advance_to_viable()
        if not self._heap:
            self.dead = True
            return None
        _, _, idxs = heapq.heappop(self._heap)
        self._expand_successors(idxs)
        return self._build_cand(idxs)

    def _expand_successors(self, idxs) -> None:
        for pos in range(len(self._streams)):
            nxt = list(idxs)
            nxt[pos] += 1
            nxt_t = tuple(nxt)
            if nxt_t in self._seen:
                continue
            stream = self._streams[pos]
            child = stream.materialize(nxt[pos])
            if child is not None:
                self._push_cell(nxt_t)

    def _advance_to_viable(self) -> None:
        while self._heap:
            _, _, idxs = self._heap[0]
            try:
                cand = self._build_cand(idxs)
            except _Cyclic:
                heapq.heappop(self._heap)
                self._expand_successors(idxs)
                continue
            if cand is None or self._blocked_for(cand):
                heapq.heappop(self._heap)
                self._expand_successors(idxs)
                continue
            return
        self.dead = True

    def _blocked_for(self, cand: Cand) -> bool:
        support_atoms = {
            self._builder.snapshot.fact_atom_for_id[fid]
            for fid in cand.facts
        }
        closure = self._builder.snapshot.closure_with_current_world(
            frozenset(support_atoms)
        )
        return any(neg in closure for neg in self._neg_atoms)

    def _build_cand(self, idxs: Tuple[int, ...]) -> Optional[Cand]:
        children: List[Cand] = []
        steps: Dict[str, StepRecord] = {}
        fact_set: Set[str] = set()
        premise_tokens: List[str] = []
        premises: List[dict] = []
        child_step_ids: List[str] = []
        for stream, idx in zip(self._streams, idxs):
            child = stream.materialize(idx)
            if child is None:
                return None
            children.append(child)
            steps.update(child.steps)
            fact_set.update(child.facts)
            child_step_ids.append(child.root_step)
            premises.append(
                {"sign": "pos", "atom": child.atom, "via_step": child.root_step}
            )
            premise_tokens.append(f"+{child.atom}@{child.root_step}")
        if self._has_self and self._self_witness is not None:
            witness = self._self_witness
            witness_step = StepRecord(witness.step_id(), "fact", witness.atom)
            steps[witness_step.step_id] = witness_step
            fact_set.add(witness.fid)
            child_step_ids.append(witness_step.step_id)
            premises.append(
                {"sign": "pos", "atom": witness.atom,
                 "via_step": witness_step.step_id}
            )
            premise_tokens.append(f"+{witness.atom}@{witness_step.step_id}")
        for neg in self._neg_atoms:
            premises.append({"sign": "neg", "atom": neg})
            premise_tokens.append(f"-{neg}")

        sid = rule_step_id(self._rule, premise_tokens)
        cached = self._builder.snapshot.step_cache.get(sid)
        if cached is None:
            cached = StepRecord(
                sid,
                "rule",
                self._atom,
                rule_id=self._rule.rid,
                rule_version=self._rule.version,
                children=tuple(child_step_ids),
                premises=tuple(premises),
            )
            self._builder.snapshot.step_cache[sid] = cached
        steps[sid] = cached
        fact_cost = sum(self._builder.snapshot.fact_cost.get(fid, 1) for fid in fact_set)
        return Cand(self._atom, frozenset(fact_set), fact_cost, sid, steps)


# --------------------------------------------------------------------------- #
# Atom stream: merge the fact alternative and all rule alternatives by cost.
# Materialization is required by a rule alternative above; candidates are
# cached so that multiple consumers (the shared DAG) never pay twice and a
# paused/resumed query keeps exact state.
# --------------------------------------------------------------------------- #
class AtomStream:
    def __init__(self, builder, atom, shell_only=False):
        self._builder = builder
        self.atom = atom
        self._alts: List[object] = []
        self._heap: List[Tuple[int, int, int]] = []
        self._tie = 0
        self._materialized: List[Optional[Cand]] = []
        self._initializing = True
        self._seed_fact()
        if shell_only:
            self._initializing = False
            return
        self._seed_rules()
        self._initializing = False

    def finish_rules(self, builder=None):
        self._seed_rules(builder)

    def _seed_fact(self) -> None:
        fact = self._builder.snapshot.fact_for_atom.get(self.atom)
        if fact is None:
            return
        alt = FactAlt(fact, self._builder.snapshot.fact_cost.get(fact.fid, 1))
        self._add_alt(alt)

    def _seed_rules(self, builder=None) -> None:
        b = builder or self._builder
        for rule in self._builder.snapshot.rules_for_head.get(self.atom, ()):
            for alt in rule.alternatives:
                # If the head occurs as its own positive premise, the only
                # sound witness is the head's own base fact (grounded
                # cycle).  Without such a fact the alternative is a rootless
                # cycle and is dropped.
                pos_atoms = [lit for lit in alt if not is_negative(lit)]
                self_witness = None
                if self.atom in pos_atoms:
                    # Grounded self-cycle: the atom's own base fact is the
                    # only witness for the self premise.  Without it the
                    # alternative proves nothing.
                    fact = self._builder.snapshot.fact_for_atom.get(self.atom)
                    if fact is None:
                        continue
                    self_witness = fact
                rule_alt = RuleAlt(
                    self._builder, self.atom, rule, alt,
                    self_witness=self_witness,
                )
                if rule_alt._initial_failed:
                    # Transitive dependency on this still-active atom with no
                    # grounding available: rootless cycle, drop it.
                    continue
                if not rule_alt.dead:
                    self._add_alt(rule_alt)

    def _add_alt(self, alt) -> None:
        index = len(self._alts)
        self._alts.append(alt)
        cand = alt.current() if hasattr(alt, "current") else None
        if cand is not None:
            heapq.heappush(self._heap, (cand.cost, self._tie, index))
            self._tie += 1

    def _refresh(self, index: int) -> None:
        alt = self._alts[index]
        cand = alt.current() if hasattr(alt, "current") else None
        if cand is not None:
            heapq.heappush(self._heap, (cand.cost, self._tie, index))
            self._tie += 1

    def materialize(self, idx: int) -> Optional[Cand]:
        while len(self._materialized) <= idx:
            produced = self._produce()
            self._materialized.append(produced)
            if produced is None:
                return None
        return self._materialized[idx]

    def current(self) -> Optional[Cand]:
        if self._heap:
            return self._alts[self._heap[0][2]].current()
        return None

    def _produce(self) -> Optional[Cand]:
        while self._heap:
            cost, _, alt_index = heapq.heappop(self._heap)
            alt = self._alts[alt_index]
            cand = alt.pop()
            self._refresh(alt_index)
            if cand is None:
                continue
            return cand
        return None


class Builder:
    """Per-revision factory and memoization point for atom streams."""

    def __init__(self, snapshot: Snapshot):
        self.snapshot = snapshot
        self._streams: Dict[str, AtomStream] = {}
        self._active: Set[str] = set()

    def is_active(self, atom: str) -> bool:
        return atom in self._active

    def get_stream(self, atom: str) -> AtomStream:
        existing = self._streams.get(atom)
        if existing is not None:
            return existing
        # Register a fact-only shell first, so a recursive dependency that
        # reaches this atom while rules are being built can still be grounded
        # by the fact (grounded cycles accepted) instead of crashing.
        shell = AtomStream(self, atom, shell_only=True)
        self._streams[atom] = shell
        self._active.add(atom)
        try:
            shell.finish_rules(self)
        finally:
            self._active.discard(atom)
        return shell


# --------------------------------------------------------------------------- #
# Materialized support + query driver
# --------------------------------------------------------------------------- #
@dataclass(frozen=True)
class Support:
    goal: str
    facts: FactIdSet
    cost: int
    support_id: str
    root_step: str
    steps: Tuple[StepRecord, ...]

    def to_bundle(
        self,
        snapshot: Snapshot,
        *,
        include_unreferenced: bool = True,
    ) -> dict:
        """Serialize replayable evidence (see semantics.verify_evidence)."""
        facts = [f for f in snapshot.facts if f.fid in self.facts]
        used_rule_keys = {
            (s.rule_id, s.rule_version)
            for s in self.steps
            if s.kind == "rule"
        }
        if include_unreferenced:
            rules = list(snapshot.rules)
        else:
            rules = [
                r for r in snapshot.rules if (r.rid, r.version) in used_rule_keys
            ]
        return {
            "goal": self.goal,
            "support_id": self.support_id,
            "root_step": self.root_step,
            "facts": [
                {"fid": f.fid, "atom": f.atom, "version": f.version}
                for f in facts
            ],
            "rules": [
                {
                    "rid": r.rid,
                    "head": r.head,
                    "body": [premise_to_json(b) for b in r.body],
                    "version": r.version,
                }
                for r in rules
            ],
            "steps": [s.to_json() for s in self.steps],
        }


@dataclass
class PullResult:
    supports: List[Support] = field(default_factory=list)
    budget_used: int = 0
    truncated: bool = False
    complete: bool = False
    budget_exhausted: bool = False

    @property
    def status(self) -> str:
        if self.complete:
            return "complete"
        if self.truncated:
            return "truncated"
        return "no-progress"


def _build_support(goal: str, cand: Cand, snapshot: Snapshot) -> Support:
    reachable: Set[str] = {cand.root_step}
    stack = [cand.root_step]
    while stack:
        sid = stack.pop()
        for child in cand.steps[sid].children:
            if child not in reachable:
                reachable.add(child)
                stack.append(child)

    # Deterministic topological order: facts first, then rule steps, Kahn with
    # step-id tie-breaking so the evidence replay is byte-stable.
    records = [cand.steps[sid] for sid in reachable]
    indeg: Dict[str, int] = {r.step_id: 0 for r in records}
    edges: Dict[str, List[str]] = {r.step_id: [] for r in records}
    for r in records:
        for child in r.children:
            edges[child].append(r.step_id)
            indeg[r.step_id] += 1
    ready = [sid for sid, d in indeg.items() if d == 0]
    ordered: List[StepRecord] = []
    while ready:
        ready.sort()
        sid = ready.pop(0)
        record = cand.steps[sid]
        ordered.append(record)
        for nxt in edges[sid]:
            indeg[nxt] -= 1
            if indeg[nxt] == 0:
                ready.append(nxt)
    if len(ordered) != len(records):
        raise RuntimeError("cycle detected while materializing support steps")

    versioned = [
        f"{fid}@v{snapshot.facts and next(f.version for f in snapshot.facts if f.fid == fid)}"
        for fid in sorted(cand.facts)
    ]
    return Support(
        goal=goal,
        facts=cand.facts,
        cost=cand.cost,
        support_id=support_id(goal, versioned),
        root_step=cand.root_step,
        steps=tuple(ordered),
    )


class Query:
    """Cost-ordered, pause/resumable enumeration for one goal.

    ``budget`` counts *work units*: each raw candidate examined is one unit.
    A pull returns when it has produced new accepted supports, the budget is
    spent (``truncated`` -- completeness must never be claimed), or the root
    stream is exhausted (``complete``).
    """

    def __init__(self, snapshot: Snapshot, goal: str, builder: Optional[Builder] = None):
        self.snapshot = snapshot
        self.goal = goal
        # Each query owns an independent builder: stream cursors (paused
        # enumeration state) must never leak between goals.  Step *records*
        # are still shared through snapshot.step_cache.
        self._builder = builder if builder is not None else Builder(snapshot)
        self._stream = self._builder.get_stream(goal)
        self._next_idx = 0
        self._accepted: List[Support] = []
        self._accepted_sets: List[FactIdSet] = []
        self._complete = False

    @property
    def accepted(self) -> Tuple[Support, ...]:
        return tuple(self._accepted)

    @property
    def complete(self) -> bool:
        return self._complete

    def pull(self, budget: int) -> PullResult:
        if budget <= 0:
            raise ValueError("budget must be positive")
        result = PullResult()
        while result.budget_used < budget:
            cand = self._stream.materialize(self._next_idx)
            self._next_idx += 1
            if cand is None:
                self._complete = True
                result.complete = True
                break
            result.budget_used += 1

            base_atoms = frozenset(
                self.snapshot.fact_atom_for_id[fid] for fid in cand.facts
            )
            if not self.snapshot.entails(base_atoms, self.goal):
                continue
            if any(prior < cand.facts for prior in self._accepted_sets):
                continue
            support = _build_support(self.goal, cand, self.snapshot)
            self._accepted.append(support)
            self._accepted_sets.append(cand.facts)
            result.supports.append(support)
        else:
            result.truncated = True
            result.budget_exhausted = True
        return result

