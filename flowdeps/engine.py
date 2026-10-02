"""Incremental engine: executes steps, records dependencies, and propagates
invalidation from input revisions with field-level precision.

Key properties:

* Invalidation is always rooted in concrete ``Change`` events -- a node can
  never keep itself alive through a cycle of stale edges.
* Re-executing a step retracts its old edges before installing new ones.
* A re-executed step whose output fingerprint is unchanged is *revalidated*:
  its downstream consumers are no longer affected (early cutoff).
* Consumers of an old object version do not automatically depend on the new
  version; only changes to fields they actually read invalidate them.
"""
from __future__ import annotations

from collections import deque
from dataclasses import dataclass, field as dc_field
from typing import Any, Callable, Optional

from .graph import CycleError, DepGraph
from .model import (
    BranchDep,
    Change,
    FactState,
    FieldDep,
    NegativeDep,
    RuleDep,
    RuleState,
    Run,
    StepDef,
    StepDep,
    fingerprint,
)

_MISSING = object()


# ---------------------------------------------------------------------------
# Reader: the only way step functions may touch inputs
# ---------------------------------------------------------------------------

class Reader:
    """Recording read view handed to a step function during execution."""

    def __init__(self, engine: "Engine", step: str, deps: set,
                 stack: tuple = (), fresh: Optional[dict] = None):
        self._engine = engine
        self._step = step
        self._deps = deps
        self._stack = stack
        self._fresh = fresh          # set only during reference re-execution

    # -- facts -------------------------------------------------------------
    def field(self, fact: str, name: str, default: Any = None) -> Any:
        state = self._engine._facts.get(fact)
        if state is None:
            self._deps.add(FieldDep(fact, 0, name))
            return default
        self._deps.add(FieldDep(fact, state.version, name))
        return state.fields.get(name, default)

    def fact(self, fact: str) -> dict:
        state = self._engine._facts.get(fact)
        if state is None:
            raise KeyError(fact)
        self._deps.add(FieldDep(fact, state.version, "*"))
        return dict(state.fields)

    def lookup(self, fact: str) -> Optional[dict]:
        """Returns None and records a *negative dependency* when missing."""
        state = self._engine._facts.get(fact)
        if state is None:
            self._deps.add(NegativeDep("fact", fact))
            return None
        self._deps.add(FieldDep(fact, state.version, "*"))
        return dict(state.fields)

    # -- rules -------------------------------------------------------------
    def rule(self, rule_id: str, default: Any = _MISSING) -> Any:
        state = self._engine._rules.get(rule_id)
        if state is None:
            if default is _MISSING:
                raise KeyError(rule_id)
            self._deps.add(NegativeDep("rule", rule_id))
            return default
        self._deps.add(RuleDep(rule_id, state.version))
        return state.value

    # -- branches ----------------------------------------------------------
    def branch(self, branch_id: str, choice: Any) -> Any:
        self._deps.add(BranchDep(branch_id, choice))
        return choice

    # -- other steps -------------------------------------------------------
    def step(self, step_id: str) -> Any:
        if self._fresh is not None:
            run = self._fresh[step_id]
        else:
            run = self._engine._ensure(step_id, self._stack)
        self._deps.add(StepDep(step_id, run.fingerprint))
        return run.value


# ---------------------------------------------------------------------------
# Reports
# ---------------------------------------------------------------------------

@dataclass
class BatchReport:
    changes: tuple = ()
    roots: tuple = ()               # steps directly invalidated by a change
    affected: tuple = ()            # steps whose output version changed
    revalidated: tuple = ()         # re-executed, output unchanged
    explanations: dict = dc_field(default_factory=dict)  # step -> shortest chain

    @property
    def direct(self) -> tuple:
        return tuple(s for s in self.affected if s in set(self.roots))

    @property
    def transitive(self) -> tuple:
        return tuple(s for s in self.affected if s not in set(self.roots))


@dataclass
class ImpactReport:
    target: str
    direct: tuple                   # steps with a dependency edge on target
    transitive: tuple               # downstream outputs reached via steps
    chains: dict                    # step -> shortest chain from target


@dataclass
class _CommitCtx:
    changed: set = dc_field(default_factory=set)
    revalidated: list = dc_field(default_factory=list)


# ---------------------------------------------------------------------------
# Engine
# ---------------------------------------------------------------------------

class Engine:
    def __init__(self):
        self._facts: dict[str, FactState] = {}
        self._rules: dict[str, RuleState] = {}
        self.step_defs: dict[str, StepDef] = {}
        self.runs: dict[str, Run] = {}
        self.graph = DepGraph()
        self.log: list[tuple] = []
        self._pending: list[Change] = []
        self._dirty: set[str] = set()
        self._ctx: Optional[_CommitCtx] = None

    # ------------------------------------------------------------------ log
    def _log(self, kind: str, *payload) -> None:
        self.log.append((kind, *payload))

    # ------------------------------------------------------------- mutations
    def define_step(self, step_id: str, version: int,
                    func: Callable[[Reader], Any]) -> None:
        old = self.step_defs.get(step_id)
        if old is not None and old.version == version:
            return
        self.step_defs[step_id] = StepDef(version, func)
        self._log("define_step", step_id, version)
        if old is not None:
            # Step correction: the old edges stay until the re-execution
            # retracts them, but the step becomes an invalidation root now.
            self._pending.append(
                Change("step_define", step_id, old.version, version))

    def upsert_fact(self, fact: str, fields: dict) -> Optional[Change]:
        fields = dict(fields)
        old = self._facts.get(fact)
        if old is None:
            version, old_version, changed = 1, None, None
        else:
            keys = set(old.fields) | set(fields)
            changed = frozenset(
                k for k in keys
                if old.fields.get(k, _MISSING) != fields.get(k, _MISSING))
            if not changed:
                return None
            version, old_version = old.version + 1, old.version
        self._facts[fact] = FactState(version, fields)
        change = Change("fact_upsert", fact, old_version, version, changed)
        self._pending.append(change)
        self._log("upsert", fact, version, fields,
                  sorted(changed) if changed is not None else None)
        return change

    def remove_fact(self, fact: str) -> Optional[Change]:
        old = self._facts.get(fact)
        if old is None:
            return None
        del self._facts[fact]
        change = Change("fact_remove", fact, old.version, None, None)
        self._pending.append(change)
        self._log("remove", fact, old.version)
        return change

    def set_rule(self, rule_id: str, value: Any) -> Optional[Change]:
        old = self._rules.get(rule_id)
        if old is not None and old.value == value:
            return None
        version = 1 if old is None else old.version + 1
        self._rules[rule_id] = RuleState(version, value)
        change = Change("rule_set", rule_id,
                        old.version if old else None, version, None)
        self._pending.append(change)
        self._log("rule_set", rule_id, version, value)
        return change

    # ------------------------------------------------------------ execution
    def _execute(self, step: str, stack: tuple = ()) -> Run:
        sd = self.step_defs[step]
        deps: set = set()
        reader = Reader(self, step, deps, stack + (step,))
        value = sd.func(reader)
        fp = fingerprint(value)
        self.graph.install(step, deps)   # retracts old edges, installs new
        self.runs[step] = Run(step, sd.version, frozenset(deps), value, fp)
        self._log("run", step, sd.version, fp)
        return self.runs[step]

    def _ensure(self, step: str, stack: tuple = ()) -> Run:
        if step in stack:
            raise CycleError(list(stack) + [step])
        if step not in self.step_defs:
            raise KeyError(f"unknown step {step!r}")
        run = self.runs.get(step)
        if run is not None and step not in self._dirty:
            return run
        if self._ctx is not None and run is not None:
            return self._revalidate(step, stack)
        self._dirty.discard(step)
        return self._execute(step, stack)

    def _revalidate(self, step: str, stack: tuple = ()) -> Run:
        self._dirty.discard(step)
        old_fp = self.runs[step].fingerprint
        run = self._execute(step, stack)
        ctx = self._ctx
        if run.fingerprint != old_fp:
            ctx.changed.add(step)
            for consumer in sorted(self.graph.consumers_of_step(step)):
                if consumer not in ctx.changed:
                    self._dirty.add(consumer)
        else:
            # Early cutoff: output unchanged -> downstream no longer affected.
            ctx.revalidated.append(step)
        return run

    def ensure(self, step: str) -> Any:
        """Commit pending changes, then return the step's up-to-date output."""
        self.commit()
        run = self._ensure(step)
        self._log("ensure", step, run.fingerprint)
        return run.value

    # ------------------------------------------------------------- accessors
    def output_version(self, step: str) -> str:
        return self.runs[step].fingerprint

    def dependencies(self, step: str) -> frozenset:
        return self.runs[step].deps

    def fact_version(self, fact: str) -> Optional[int]:
        state = self._facts.get(fact)
        return state.version if state else None

    def rule_version(self, rule_id: str) -> Optional[int]:
        state = self._rules.get(rule_id)
        return state.version if state else None

    # ------------------------------------------------------------ invalidation
    @staticmethod
    def _invalidated(dep, change: Change) -> bool:
        if isinstance(dep, FieldDep):
            if change.kind not in ("fact_upsert", "fact_remove"):
                return False
            if dep.fact != change.target:
                return False
            seen = dep.version                    # 0 == was absent
            before = change.old_version or 0
            if seen > before:
                return False                      # dep already at newer version
            return (change.changed_fields is None
                    or dep.field == "*"
                    or dep.field in change.changed_fields)
        if isinstance(dep, NegativeDep):
            if change.old_version is not None:
                return False                      # only *additions* fire it
            if dep.collection == "fact":
                return change.kind == "fact_upsert" and change.target == dep.key
            return change.kind == "rule_set" and change.target == dep.key
        if isinstance(dep, RuleDep):
            return (change.kind == "rule_set"
                    and dep.rule == change.target
                    and dep.version <= (change.old_version or 0))
        return False                              # BranchDep / StepDep: dynamic

    def commit(self) -> BatchReport:
        if not self._pending and not self._dirty:
            return BatchReport()
        changes, self._pending = self._pending, []

        roots: dict[str, str] = {}
        for step in sorted(self.runs):
            run = self.runs[step]
            for change in changes:
                if any(self._invalidated(dep, change) for dep in run.deps):
                    roots.setdefault(step, change.label())
                    break
        for step, sd in self.step_defs.items():
            run = self.runs.get(step)
            if run is not None and run.step_version != sd.version:
                roots.setdefault(
                    step,
                    f"step {step} corrected v{run.step_version}->v{sd.version}")

        cycle = self.graph.find_cycle()
        if cycle:
            raise CycleError(cycle)

        self._dirty = set(roots)
        ctx = _CommitCtx()
        self._ctx = ctx
        try:
            for step in self.graph.topo_order():
                if step in self._dirty:
                    self._revalidate(step)
            for step in sorted(self._dirty):
                self._revalidate(step)
        finally:
            self._ctx = None
            self._dirty = set()

        affected = sorted(ctx.changed)
        revalidated = sorted(ctx.revalidated)
        explanations = self._explain(roots, ctx.changed)
        report = BatchReport(
            changes=tuple(c.label() for c in changes),
            roots=tuple(sorted(roots)),
            affected=tuple(affected),
            revalidated=tuple(revalidated),
            explanations=explanations,
        )
        self._log("commit", tuple(affected), tuple(revalidated),
                  tuple(sorted(roots)))
        return report

    def _explain(self, roots: dict, changed: set) -> dict:
        """Shortest chain (BFS) from a root change to each affected step."""
        chains: dict[str, tuple] = {}
        queue = deque()
        for step in sorted(changed):
            if step in roots:
                chains[step] = (roots[step], step)
                queue.append(step)
        while queue:
            step = queue.popleft()
            for consumer in sorted(self.graph.consumers_of_step(step)):
                if consumer in changed and consumer not in chains:
                    chains[consumer] = chains[step] + (consumer,)
                    queue.append(consumer)
        return chains

    # -------------------------------------------------------------- queries
    @staticmethod
    def _matches(dep, target: str, fields: Optional[set]) -> bool:
        if isinstance(dep, FieldDep) and dep.fact == target:
            return fields is None or dep.field == "*" or dep.field in fields
        if isinstance(dep, NegativeDep) and dep.key == target:
            return True
        if isinstance(dep, RuleDep) and dep.rule == target:
            return True
        return False

    def impact(self, target: str,
               fields: Optional[set] = None) -> ImpactReport:
        """Dry-run query: which downstream products would a revision of
        ``target`` affect?  Distinguishes direct vs transitive dependents
        and provides the shortest chain to each."""
        self.commit()
        direct = sorted(
            step for step, run in self.runs.items()
            if any(self._matches(dep, target, fields) for dep in run.deps))
        chains = {s: (target, s) for s in direct}
        transitive: list[str] = []
        seen = set(direct)
        queue = deque(direct)
        while queue:
            step = queue.popleft()
            for consumer in sorted(self.graph.consumers_of_step(step)):
                if consumer not in seen:
                    seen.add(consumer)
                    transitive.append(consumer)
                    chains[consumer] = chains[step] + (consumer,)
                    queue.append(consumer)
        return ImpactReport(target, tuple(direct), tuple(transitive), chains)
