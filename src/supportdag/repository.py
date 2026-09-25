"""Shared rule-DAG repository with incremental revision maintenance."""

from __future__ import annotations

from dataclasses import dataclass, field
from typing import Dict, FrozenSet, List, Mapping, Optional, Sequence, Set, Tuple

from .engine import Builder, PullResult, Query, Snapshot, Support
from .model import Fact, Rule, atom_of, is_negative, premise_to_json


@dataclass(frozen=True)
class Change:
    kind: str  # "fact-add" | "fact-retract" | "fact-version" |
    #          # "rule-add" | "rule-remove" | "rule-version"
    ref_id: str
    old_version: Optional[int] = None
    new_version: Optional[int] = None


@dataclass
class RevisionReport:
    revision_before: int
    revision_after: int
    changes: Tuple[Change, ...]
    affected_goals: Tuple[str, ...]
    revived_goals: Tuple[str, ...] = ()
    noop: bool = False


@dataclass
class GoalView:
    """Maintained enumeration state for one goal across revisions."""

    goal: str
    budget: int
    query: Query
    revision: int
    last: Optional[PullResult] = None

    def pull(self, budget: Optional[int] = None) -> PullResult:
        if budget is not None:
            self.budget = budget
        self.last = self.query.pull(self.budget)
        self.revision = self.query.snapshot.revision
        return self.last

    @property
    def supports(self) -> Tuple[Support, ...]:
        return self.query.accepted

    @property
    def complete(self) -> bool:
        return self.query.complete

    @property
    def status(self) -> str:
        if self.query.complete:
            return "complete"
        if self.last is not None and self.last.truncated:
            return "truncated"
        return "new"


class KnowledgeBase:
    def __init__(self):
        self._facts: Dict[str, Fact] = {}
        self._rules: Dict[str, Rule] = {}
        self._revision = 0
        self._snapshot: Optional[Snapshot] = None
        self._builder: Optional[Builder] = None
        self._views: Dict[str, GoalView] = {}

    # ------------------------------------------------------------------ #
    # Mutations (batched inside a commit)
    # ------------------------------------------------------------------ #
    def add_fact(self, fid: str, atom: str, *, cost: int = 1, version: int = 1):
        if cost < 1:
            raise ValueError("evidence cost must be >= 1")
        if fid in self._facts:
            raise ValueError(f"fact {fid!r} already exists")
        self._facts[fid] = Fact(fid, atom, version)
        self._fact_cost: Dict[str, int] = getattr(self, "_fact_cost", {})
        self._fact_cost[fid] = cost
        return ("fact-add", fid, None, version)

    def retract_fact(self, fid: str):
        if fid not in self._facts:
            raise KeyError(fid)
        old = self._facts.pop(fid)
        getattr(self, "_fact_cost", {}).pop(fid, None)
        return ("fact-retract", fid, old.version, None)

    def update_fact_version(self, fid: str, version: int):
        fact = self._facts[fid]
        old = fact.version
        if version == old:
            raise ValueError("new version must differ")
        self._facts[fid] = Fact(fid, atom=fact.atom, version=version)
        return ("fact-version", fid, old, version)

    def add_rule(self, rule: Rule):
        if rule.rid in self._rules:
            raise ValueError(f"rule {rule.rid!r} already exists")
        self._rules[rule.rid] = rule
        return ("rule-add", rule.rid, None, rule.version)

    def remove_rule(self, rid: str):
        old = self._rules.pop(rid)
        return ("rule-remove", rid, old.version, None)

    def update_rule(self, rule: Rule):
        if rule.rid not in self._rules:
            raise KeyError(rule.rid)
        old = self._rules[rule.rid]
        if old.version == rule.version:
            raise ValueError("updated rule must carry a new version")
        self._rules[rule.rid] = rule
        return ("rule-version", rule.rid, old.version, rule.version)

    def commit(self, *changes) -> RevisionReport:
        """Apply pre-validated change tuples and revise dependent goals.

        Change tuples are produced by the mutator helpers above; callers can
        also build ``(kind, ref_id, old, new)`` tuples manually.  The revision
        bumps and every maintained goal that transitively depends on changed
        content is re-enumerated.
        """
        if not changes:
            return RevisionReport(self._revision, self._revision, (), (), noop=True)

        old_snapshot = self._snapshot
        old_views = dict(self._views)
        before = self._revision

        change_records = tuple(
            Change(kind=kind, ref_id=ref, old_version=old, new_version=new)
            for kind, ref, old, new in changes
        )
        affected_atoms = self._changed_atoms(change_records)

        self._revision += 1
        self._snapshot = self._build_snapshot()
        self._builder = Builder(self._snapshot)

        affected_goals: List[str] = []
        revived_goals: List[str] = []
        dependency = self._dependency_index()
        affected_closure = self._affected_closure(affected_atoms, dependency)

        for goal, view in old_views.items():
            if goal in affected_closure:
                new_query = Query(self._snapshot, goal)
                new_view = GoalView(goal, view.budget, new_query, self._revision)
                new_view.pull()
                self._views[goal] = new_view
                affected_goals.append(goal)
                prev = {s.support_id for s in view.supports}
                now = {s.support_id for s in new_view.supports}
                if now - prev:
                    revived_goals.append(goal)
            else:
                # Unaffected goals keep their already enumerated supports; only
                # the revision stamp moves forward.
                view.revision = self._revision
                self._views[goal] = view

        return RevisionReport(
            revision_before=before,
            revision_after=self._revision,
            changes=change_records,
            affected_goals=tuple(sorted(affected_goals)),
            revived_goals=tuple(sorted(revived_goals)),
        )

    # ------------------------------------------------------------------ #
    # Reads / maintained goals
    # ------------------------------------------------------------------ #
    @property
    def revision(self) -> int:
        return self._revision

    def snapshot(self) -> Snapshot:
        if self._snapshot is None:
            self._snapshot = self._build_snapshot()
            self._builder = Builder(self._snapshot)
        return self._snapshot

    def query(self, goal: str, budget: int) -> GoalView:
        snap = self.snapshot()
        view = self._views.get(goal)
        if view is None:
            query = Query(snap, goal)
            view = GoalView(goal, budget, query, self._revision)
            self._views[goal] = view
        view.pull(budget)
        return view

    def goal(self, goal: str) -> GoalView:
        """Return the maintained view without consuming a fresh pull."""
        return self._views[goal]

    def fact_costs(self) -> Mapping[str, int]:
        return dict(getattr(self, "_fact_cost", {}))

    # ------------------------------------------------------------------ #
    # Dependency analysis
    # ------------------------------------------------------------------ #
    def _dependency_index(self) -> Dict[str, Set[str]]:
        """head -> set of body atoms it (transitively may) depend on."""
        direct: Dict[str, Set[str]] = {}
        for rule in self._rules.values():
            deps = direct.setdefault(rule.head, set())
            for alt in rule.alternatives:
                for lit in alt:
                    deps.add(atom_of(lit))
        return direct

    def _changed_atoms(self, changes: Sequence[Change]) -> Set[str]:
        atoms: Set[str] = set()
        for change in changes:
            if change.kind.startswith("fact"):
                fact = self._facts.get(change.ref_id)
                if fact is not None:
                    atoms.add(fact.atom)
                else:
                    # After retraction the fact is gone; infer the atom from
                    # the change carried by mutators is not possible, so the
                    # caller-supplied snapshot lookup below handles it via
                    # the old snapshot.
                    if self._snapshot is not None:
                        for f in self._snapshot.facts:
                            if f.fid == change.ref_id:
                                atoms.add(f.atom)
                    # Retracted: also scan previous snapshot's rules is unneeded;
                    # the atom itself is the change site.
            elif change.kind.startswith("rule"):
                rule = self._rules.get(change.ref_id)
                if rule is not None:
                    atoms.add(rule.head)
                if self._snapshot is not None:
                    for r in self._snapshot.rules:
                        if r.rid == change.ref_id:
                            atoms.add(r.head)
        return atoms

    def _affected_closure(
        self, seeds: Set[str], direct: Mapping[str, Set[str]]
    ) -> Set[str]:
        """All atoms whose derivations may change (reverse reachability)."""
        reverse: Dict[str, Set[str]] = {}
        for head, deps in direct.items():
            for dep in deps:
                reverse.setdefault(dep, set()).add(head)
        affected = set(seeds)
        stack = list(seeds)
        while stack:
            atom = stack.pop()
            for head in reverse.get(atom, ()):
                if head not in affected:
                    affected.add(head)
                    stack.append(head)
        return affected

    def _build_snapshot(self) -> Snapshot:
        facts = tuple(sorted(self._facts.values(), key=lambda f: f.fid))
        rules = tuple(sorted(self._rules.values(), key=lambda r: r.rid))
        rules_for_head: Dict[str, List[Rule]] = {}
        for rule in rules:
            rules_for_head.setdefault(rule.head, []).append(rule)
        fact_for_atom = {f.atom: f for f in facts}
        fact_atom_for_id = {f.fid: f.atom for f in facts}
        costs = getattr(self, "_fact_cost", {})
        return Snapshot(
            revision=self._revision,
            facts=facts,
            rules=rules,
            rules_for_head={h: tuple(rs) for h, rs in rules_for_head.items()},
            fact_for_atom=fact_for_atom,
            fact_atom_for_id=fact_atom_for_id,
            fact_cost=dict(costs),
        )

    # ------------------------------------------------------------------ #
    # Serialization / replay
    # ------------------------------------------------------------------ #
    def to_json(self) -> dict:
        snap = self.snapshot()
        return {
            "revision": self._revision,
            "facts": [
                {"fid": f.fid, "atom": f.atom, "version": f.version,
                 "cost": snap.fact_cost.get(f.fid, 1)}
                for f in snap.facts
            ],
            "rules": [
                {
                    "rid": r.rid,
                    "head": r.head,
                    "body": [premise_to_json(b) for b in r.body],
                    "version": r.version,
                }
                for r in snap.rules
            ],
        }

    @classmethod
    def from_json(cls, data: dict) -> "KnowledgeBase":
        kb = cls()
        for raw in data.get("facts", []):
            kb.add_fact(raw["fid"], raw["atom"], cost=raw.get("cost", 1),
                        version=raw.get("version", 1))
        for raw in data.get("rules", []):
            from .model import premise_from_json
            rule = Rule(
                rid=raw["rid"],
                head=raw["head"],
                body=tuple(premise_from_json(b) for b in raw["body"]),
                version=raw.get("version", 1),
            )
            kb.add_rule(rule)
        kb._revision = data.get("revision", 0)
        kb._snapshot = None
        return kb
