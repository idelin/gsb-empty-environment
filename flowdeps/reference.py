"""Reference checker: full clean re-execution used to audit the incremental
engine.

* ``missed_invalidations`` -- steps whose incremental output version differs
  from a from-scratch re-execution (must be empty).
* ``irrelevant_branch_accesses`` -- dependency edges the incremental engine
  still carries that the clean re-execution proves irrelevant, e.g. reads
  recorded on branches no longer taken or edges a step correction failed to
  retract (must be 0).
* ``missing_dependencies`` -- edges the clean run records but the incremental
  graph lacks (must be 0).
"""
from __future__ import annotations

from dataclasses import dataclass

from .engine import Engine, Reader
from .model import Run, fingerprint


@dataclass
class ReferenceReport:
    checked_steps: int
    missed_invalidations: tuple
    irrelevant_branch_accesses: int
    missing_dependencies: int

    @property
    def ok(self) -> bool:
        return (not self.missed_invalidations
                and self.irrelevant_branch_accesses == 0
                and self.missing_dependencies == 0)


def reference_check(engine: Engine) -> ReferenceReport:
    engine.commit()
    order = engine.graph.topo_order()
    fresh: dict[str, Run] = {}
    fresh_deps: dict[str, frozenset] = {}
    for step in order:
        sd = engine.step_defs[step]
        deps: set = set()
        reader = Reader(engine, step, deps, (step,), fresh=fresh)
        value = sd.func(reader)
        fresh[step] = Run(step, sd.version, frozenset(deps), value,
                          fingerprint(value))
        fresh_deps[step] = frozenset(deps)

    missed = tuple(
        s for s in order
        if engine.runs[s].fingerprint != fresh[s].fingerprint)
    irrelevant = 0
    missing = 0
    for step in order:
        current = engine.graph.step_deps.get(step, frozenset())
        irrelevant += len(current - fresh_deps[step])
        missing += len(fresh_deps[step] - current)
    return ReferenceReport(len(order), missed, irrelevant, missing)
