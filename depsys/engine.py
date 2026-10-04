"""Field-level provenance and invalidation engine.

A step is a pure function ``fn(ctx) -> {fact_id: fields}``.  While it runs,
every fact access goes through :class:`ReadContext` and is recorded as a
:class:`~depsys.model.DepEdge`.  Invalidation is field-precise: revising a
fact only affects steps whose recorded accesses intersect the changed
fields, so a consumer of an older version of an object does *not*
automatically depend on the new version.
"""
from __future__ import annotations

from collections import deque
from dataclasses import dataclass, field
from typing import Any, Iterable

from .model import (
    DepEdge,
    DepKind,
    Fact,
    FactAdd,
    FactUpdate,
    RuleUpdate,
    Step,
    StepCorrection,
)


class CycleError(Exception):
    """Raised when a cyclic (illegal) processing chain is detected."""

    def __init__(self, chain: list[str]):
        self.chain = list(chain)
        super().__init__("cyclic processing chain: " + " -> ".join(self.chain))


class MissingFactError(KeyError):
    pass


# ---------------------------------------------------------------------------
# Explanations / evidence
# ---------------------------------------------------------------------------


@dataclass
class RootCause:
    kind: str  # fact_fields | fact_added | rule_update | step_correction
    target_id: str
    changed_fields: frozenset[str] = frozenset()
    data: dict[str, Any] = field(default_factory=dict)

    def describe(self) -> str:
        if self.kind == "fact_fields":
            fields = ",".join(sorted(self.changed_fields))
            return f"{self.target_id} fields {{{fields}}} changed"
        if self.kind == "fact_added":
            return f"{self.target_id} appeared (was absent)"
        if self.kind == "rule_update":
            return f"rule {self.target_id} -> v{self.data.get('new_version')}"
        return f"step {self.target_id} corrected -> v{self.data.get('new_version')}"


@dataclass
class Explanation:
    """Shortest why-chain from a root change to an invalidated step.

    ``hops`` are recorded dependency edges; hop i+1's step consumes a fact
    produced by hop i's step, so the chain is a simple path (a node can
    never be its own justification).
    """

    step_id: str
    root: RootCause
    hops: tuple[DepEdge, ...] = ()

    @property
    def distance(self) -> int:
        return len(self.hops)

    @property
    def is_simple(self) -> bool:
        steps = [h.step_id for h in self.hops]
        return len(set(steps)) == len(steps)

    def describe(self) -> str:
        parts = [self.root.describe()]
        for hop in self.hops:
            if hop.kind is DepKind.NEGATIVE:
                parts.append(f"{hop.step_id} probed missing {hop.fact_id}")
            else:
                parts.append(f"{hop.step_id} {hop.kind.value} {hop.fact_id}.{hop.field}")
        return "  =>  ".join(parts)

    def replay(self, engine: "Engine") -> bool:
        """Re-verify this evidence against the engine's current records."""
        root = self.root
        if root.kind == "fact_fields":
            fact = engine.facts.get(root.target_id)
            if fact is None or fact.version < root.data.get("version", 0):
                return False
        elif root.kind == "fact_added":
            if root.target_id not in engine.facts:
                return False
        elif root.kind == "rule_update":
            if engine.rules.get(root.target_id) != root.data.get("new_version"):
                return False
        elif root.kind == "step_correction":
            step = engine.steps.get(root.target_id)
            if step is None or step.version != root.data.get("new_version"):
                return False
        else:
            return False

        recorded = {(e.step_id, e.fact_id, e.field, e.kind) for e in engine.edges}
        recorded |= {(e.step_id, e.fact_id, e.field, e.kind) for e in engine.journal}
        for i, hop in enumerate(self.hops):
            if (hop.step_id, hop.fact_id, hop.field, hop.kind) not in recorded:
                return False
            if i == 0:
                if root.kind == "fact_fields" and (
                    hop.fact_id != root.target_id
                    or hop.field not in root.changed_fields
                ):
                    return False
                if root.kind == "fact_added" and hop.fact_id != root.target_id:
                    return False
                if root.kind in ("rule_update", "step_correction"):
                    producer = engine.facts.get(hop.fact_id)
                    if producer is None or producer.producer != root.data.get("seed_step"):
                        return False
            else:
                producer = engine.facts.get(hop.fact_id)
                if producer is None or producer.producer != self.hops[i - 1].step_id:
                    return False
        return True


# ---------------------------------------------------------------------------
# Reports
# ---------------------------------------------------------------------------


@dataclass
class ImpactReport:
    """Answer to "which downstream products does this input revision affect"."""

    root: RootCause
    explanations: dict[str, Explanation]  # step_id -> shortest chain
    products: set[str]  # fact ids produced by affected steps

    @property
    def direct(self) -> dict[str, Explanation]:
        return {s: e for s, e in self.explanations.items() if e.distance <= 1}

    @property
    def transitive(self) -> dict[str, Explanation]:
        return {s: e for s, e in self.explanations.items() if e.distance > 1}


@dataclass
class BatchReport:
    explanations: dict[str, Explanation]
    revalidated: set[str]  # steps actually re-executed
    rescued: set[str]  # marked but re-validation proved them unaffected
    actual_changed: dict[str, set[str]]  # fact_id -> actually changed fields
    non_compliant: set[str]  # steps whose rule_version lags the rule

    @property
    def direct(self) -> dict[str, Explanation]:
        return {s: e for s, e in self.explanations.items() if e.distance <= 1}

    @property
    def transitive(self) -> dict[str, Explanation]:
        return {s: e for s, e in self.explanations.items() if e.distance > 1}


@dataclass
class ReferenceReport:
    """Result of a full re-execution used as the ground-truth reference."""

    missed: frozenset[str]  # facts whose incremental state diverged
    irrelevant_branch_accesses: int  # branch reads on facts whose changed
    # fields did not include the read field (a version-based engine would
    # have invalidated on these; field-level tracking avoids it)

    @property
    def ok(self) -> bool:
        return not self.missed


# ---------------------------------------------------------------------------
# Read context (records every access)
# ---------------------------------------------------------------------------


class ReadContext:
    def __init__(self, engine: "Engine", step: Step):
        self._engine = engine
        self._step = step
        self.edges: list[DepEdge] = []

    def _record(self, fact_id: str, fact_version: int | None, field: str | None, kind: DepKind) -> None:
        self.edges.append(
            DepEdge(self._step.step_id, self._step.version, fact_id, fact_version, field, kind)
        )

    def read(self, fact_id: str, field: str) -> Any:
        fact = self._engine.facts.get(fact_id)
        if fact is None:
            raise MissingFactError(fact_id)
        self._record(fact_id, fact.version, field, DepKind.READ)
        return fact.fields[field]

    def branch(self, fact_id: str, field: str) -> Any:
        """Read a field used to decide a conditional branch."""
        fact = self._engine.facts.get(fact_id)
        if fact is None:
            raise MissingFactError(fact_id)
        self._record(fact_id, fact.version, field, DepKind.BRANCH)
        return fact.fields[field]

    def exists(self, fact_id: str) -> bool:
        """Probe for a fact; records a NEGATIVE dependency when absent."""
        fact = self._engine.facts.get(fact_id)
        if fact is None:
            self._record(fact_id, None, None, DepKind.NEGATIVE)
            return False
        return True


# ---------------------------------------------------------------------------
# Engine
# ---------------------------------------------------------------------------


class Engine:
    def __init__(self) -> None:
        self.facts: dict[str, Fact] = {}
        self.steps: dict[str, Step] = {}
        self.rules: dict[str, int] = {}
        self.edges: list[DepEdge] = []
        # Append-only log of every edge ever recorded (including ones later
        # retracted); makes historical evidence replayable.
        self.journal: list[DepEdge] = []
        self._last_batch: dict[str, Any] | None = None

    # -- registration ------------------------------------------------------

    def add_fact(self, fact_id: str, fields: dict[str, Any], version: int = 1) -> None:
        self.facts[fact_id] = Fact(fact_id, version, dict(fields), producer=None)

    def register_rule(self, rule_id: str, version: int = 1) -> None:
        self.rules[rule_id] = version

    def register_step(
        self,
        step_id: str,
        fn,
        rule_id: str,
        rule_version: int | None = None,
        version: int = 1,
    ) -> None:
        if rule_id not in self.rules:
            raise KeyError(f"unknown rule {rule_id!r}")
        step = Step(
            step_id,
            version,
            rule_id,
            rule_version if rule_version is not None else self.rules[rule_id],
            fn,
        )
        self.steps[step_id] = step
        self._run_step(step)  # initial execution records edges; may raise CycleError

    # -- step graph / cycle diagnosis --------------------------------------

    def _step_graph(self) -> dict[str, set[str]]:
        producer_of: dict[str, str] = {}
        for sid, step in self.steps.items():
            for out in step.outputs:
                producer_of[out] = sid
        graph: dict[str, set[str]] = {sid: set() for sid in self.steps}
        for e in self.edges:
            if e.kind is DepKind.NEGATIVE:
                continue
            producer = producer_of.get(e.fact_id)
            if producer:
                graph[producer].add(e.step_id)
        return graph

    def _find_cycle(self) -> list[str] | None:
        graph = self._step_graph()
        color = {s: 0 for s in self.steps}  # 0 white, 1 gray, 2 black
        stack: list[str] = []

        def dfs(node: str) -> list[str] | None:
            color[node] = 1
            stack.append(node)
            for nxt in graph.get(node, ()):
                if color[nxt] == 1:
                    return stack[stack.index(nxt):] + [nxt]
                if color[nxt] == 0:
                    found = dfs(nxt)
                    if found:
                        return found
            stack.pop()
            color[node] = 2
            return None

        for sid in self.steps:
            if color[sid] == 0:
                found = dfs(sid)
                if found:
                    return found
        return None

    def _topo_order(self) -> list[str]:
        graph = self._step_graph()
        indeg = {s: 0 for s in self.steps}
        for dsts in graph.values():
            for d in dsts:
                indeg[d] += 1
        queue = deque(sorted(s for s, d in indeg.items() if d == 0))
        order: list[str] = []
        while queue:
            node = queue.popleft()
            order.append(node)
            for d in sorted(graph[node]):
                indeg[d] -= 1
                if indeg[d] == 0:
                    queue.append(d)
        if len(order) != len(self.steps):
            raise CycleError(self._find_cycle() or ["<unknown>"])
        return order

    # -- execution ----------------------------------------------------------

    def _run_step(self, step: Step) -> dict[str, set[str]]:
        """Run ``step``, retract its old edges, record new ones, version its
        outputs.  Returns {fact_id: actually-changed fields}."""
        ctx = ReadContext(self, step)
        produced = step.fn(ctx) or {}

        original = list(self.edges)
        self.edges = [e for e in self.edges if e.step_id != step.step_id] + list(ctx.edges)
        cycle = self._find_cycle()
        if cycle is not None:
            self.edges = original  # exact roll back: chain stays acyclic
            raise CycleError(cycle)
        self.journal.extend(ctx.edges)

        changed: dict[str, set[str]] = {}
        for fid in set(step.outputs) - set(produced):
            old = self.facts.pop(fid, None)
            if old is not None:
                changed[fid] = set(old.fields)
        step.outputs = tuple(produced)
        for fid, fields in produced.items():
            existing = self.facts.get(fid)
            if existing is None:
                self.facts[fid] = Fact(fid, 1, dict(fields), producer=step.step_id)
                changed[fid] = set(fields)
            elif existing.fields != fields:
                diff = {
                    k
                    for k in set(existing.fields) | set(fields)
                    if existing.fields.get(k) != fields.get(k)
                }
                existing.version += 1
                existing.fields = dict(fields)
                changed[fid] = diff
        return changed

    # -- impact query --------------------------------------------------------

    def _propagate(self, seeds: list[Explanation]) -> dict[str, Explanation]:
        """BFS from seed explanations; yields the shortest chain per step.
        The visited set guarantees simple paths: invalidation can never be
        self-supporting."""
        result: dict[str, Explanation] = {}
        queue: deque[str] = deque()
        for ex in seeds:
            if ex.step_id not in result:
                result[ex.step_id] = ex
                queue.append(ex.step_id)
        while queue:
            sid = queue.popleft()
            base = result[sid]
            for out in self.steps[sid].outputs:
                for e in self._consumers(out):
                    if e.step_id not in result:
                        result[e.step_id] = Explanation(
                            step_id=e.step_id, root=base.root, hops=base.hops + (e,)
                        )
                        queue.append(e.step_id)
        return result

    def _consumers(self, fact_id: str) -> list[DepEdge]:
        return [
            e
            for e in self.edges
            if e.fact_id == fact_id and e.kind is not DepKind.NEGATIVE
        ]

    def query_impact(self, fact_id: str, changed_fields: Iterable[str]) -> ImpactReport:
        """Which downstream products would a revision of ``fact_id`` touching
        ``changed_fields`` affect?  Direct (distance 1) vs transitive."""
        changed = frozenset(changed_fields)
        fact = self.facts.get(fact_id)
        root = RootCause(
            "fact_fields",
            fact_id,
            changed,
            {"version": (fact.version + 1) if fact else None},
        )
        seeds = [
            Explanation(step_id=e.step_id, root=root, hops=(e,))
            for e in self.edges
            if e.kind in (DepKind.READ, DepKind.BRANCH)
            and e.fact_id == fact_id
            and e.field in changed
        ]
        explanations = self._propagate(seeds)
        products = {o for sid in explanations for o in self.steps[sid].outputs}
        return ImpactReport(root=root, explanations=explanations, products=products)

    # -- batch changes --------------------------------------------------------

    def _rule_mismatch(self, step: Step) -> bool:
        return self.rules.get(step.rule_id) != step.rule_version

    def _triggered(
        self,
        step: Step,
        actual_changed: dict[str, set[str]],
        added: dict[str, set[str]],
    ) -> bool:
        for e in self.edges:
            if e.step_id != step.step_id:
                continue
            if e.kind is DepKind.NEGATIVE:
                if e.fact_id in added:
                    return True
            elif e.field in actual_changed.get(e.fact_id, ()):
                return True
        return False

    def apply_batch(self, changes: Iterable[Any]) -> BatchReport:
        pre_edges = list(self.edges)
        base_changed: dict[str, set[str]] = {}
        added: dict[str, set[str]] = {}
        rule_updates: dict[str, int] = {}
        backups: dict[str, tuple] = {}
        must_run: set[str] = set()
        seeds: list[Explanation] = []

        for ch in changes:
            if isinstance(ch, FactUpdate):
                fact = self.facts.get(ch.fact_id)
                if fact is None:
                    raise MissingFactError(ch.fact_id)
                fact.version += 1
                fact.fields = dict(ch.new_fields)
                base_changed.setdefault(ch.fact_id, set()).update(ch.changed_fields)
            elif isinstance(ch, FactAdd):
                if ch.fact_id in self.facts:
                    raise ValueError(f"fact {ch.fact_id!r} already exists")
                self.facts[ch.fact_id] = Fact(ch.fact_id, 1, dict(ch.fields))
                added[ch.fact_id] = set(ch.fields)
            elif isinstance(ch, RuleUpdate):
                self.rules[ch.rule_id] = ch.new_version
                rule_updates[ch.rule_id] = ch.new_version
            elif isinstance(ch, StepCorrection):
                step = self.steps[ch.step_id]
                backups[ch.step_id] = (step.fn, step.version, step.rule_version)
                step.fn = ch.new_fn
                step.version = ch.new_version
                if ch.new_rule_version is not None:
                    step.rule_version = ch.new_rule_version
                must_run.add(ch.step_id)
            else:
                raise TypeError(f"unknown change {ch!r}")

        # Phase A: marking (conservative superset) with shortest explanations.
        for e in list(self.edges):
            if (
                e.kind in (DepKind.READ, DepKind.BRANCH)
                and e.fact_id in base_changed
                and e.field in base_changed[e.fact_id]
            ):
                root = RootCause(
                    "fact_fields",
                    e.fact_id,
                    frozenset(base_changed[e.fact_id]),
                    {"version": self.facts[e.fact_id].version},
                )
                seeds.append(Explanation(step_id=e.step_id, root=root, hops=(e,)))
            elif e.kind is DepKind.NEGATIVE and e.fact_id in added:
                root = RootCause("fact_added", e.fact_id)
                seeds.append(Explanation(step_id=e.step_id, root=root, hops=(e,)))
        for sid, step in self.steps.items():
            if step.rule_id in rule_updates and step.rule_version != rule_updates[step.rule_id]:
                root = RootCause(
                    "rule_update",
                    step.rule_id,
                    data={"new_version": rule_updates[step.rule_id], "seed_step": sid},
                )
                seeds.append(Explanation(step_id=sid, root=root))
        for ch in changes:
            if isinstance(ch, StepCorrection):
                root = RootCause(
                    "step_correction",
                    ch.step_id,
                    data={"new_version": ch.new_version, "seed_step": ch.step_id},
                )
                seeds.append(Explanation(step_id=ch.step_id, root=root))
        explanations = self._propagate(seeds)

        # Phase B: re-validation in topological order.  A candidate only
        # re-runs if a *live* reason exists (actual changed fields reached
        # one of its recorded accesses); otherwise it is rescued.
        actual_changed: dict[str, set[str]] = {k: set(v) for k, v in base_changed.items()}
        for fid, flds in added.items():
            actual_changed[fid] = set(flds)
        revalidated: set[str] = set()
        rescued: set[str] = set()
        for sid in self._topo_order():
            if sid not in explanations and sid not in must_run:
                continue
            step = self.steps[sid]
            live = (
                sid in must_run
                or self._rule_mismatch(step)
                or self._triggered(step, actual_changed, added)
            )
            if live:
                try:
                    changed = self._run_step(step)
                except CycleError:
                    if sid in backups:  # restore the pre-correction definition
                        step.fn, step.version, step.rule_version = backups[sid]
                    raise
                for fid, flds in changed.items():
                    actual_changed.setdefault(fid, set()).update(flds)
                revalidated.add(sid)
            else:
                rescued.add(sid)

        self._last_batch = {"pre_edges": pre_edges, "base_changed": base_changed}
        return BatchReport(
            explanations=explanations,
            revalidated=revalidated,
            rescued=rescued,
            actual_changed=actual_changed,
            non_compliant={
                s.step_id for s in self.steps.values() if self._rule_mismatch(s)
            },
        )

    # -- reference check ------------------------------------------------------

    def reference_check(self) -> ReferenceReport:
        """Full re-execution as ground truth.

        Re-runs every step from scratch (in topological order) and compares
        the derived store with the incremental result: any divergence is a
        missed invalidation.  Also counts irrelevant branch accesses -
        branch-condition reads on revised facts whose field was *not* among
        the changed fields.
        """
        ctx = self._last_batch or {"pre_edges": [], "base_changed": {}}
        pre_edges: list[DepEdge] = ctx["pre_edges"]
        base_changed: dict[str, set[str]] = ctx["base_changed"]
        irrelevant = sum(
            1
            for e in pre_edges
            if e.kind is DepKind.BRANCH
            and e.fact_id in base_changed
            and e.field not in base_changed[e.fact_id]
        )

        incremental = {
            fid: dict(f.fields) for fid, f in self.facts.items() if f.producer
        }
        for fid in [fid for fid, f in self.facts.items() if f.producer]:
            del self.facts[fid]
        for sid in self._topo_order():
            self._run_step(self.steps[sid])
        full = {fid: dict(f.fields) for fid, f in self.facts.items() if f.producer}

        missed = frozenset(
            fid for fid in set(incremental) | set(full) if incremental.get(fid) != full.get(fid)
        )
        return ReferenceReport(missed=missed, irrelevant_branch_accesses=irrelevant)
