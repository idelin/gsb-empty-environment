"""Local, in-process executor. No SQL engine is used."""

from __future__ import annotations

from dataclasses import dataclass, field

from plan_dag.catalog import Catalog
from plan_dag.runtime import BoundPlan, Relation


@dataclass
class ExecutionContext:
    catalog: Catalog
    memo: dict[str, Relation] = field(default_factory=dict)
    scan_count: int = 0


class Executor:
    def __init__(self, catalog: Catalog):
        self.catalog = catalog

    def execute_plan(
        self, plan: BoundPlan, roots: list[str] | None = None
    ) -> dict[str, Relation]:
        """Execute every requested root. Shared nodes are evaluated exactly once."""
        context = ExecutionContext(catalog=self.catalog)
        self.last_context = context
        root_ids = roots if roots is not None else list(plan.roots)
        results: dict[str, Relation] = {}
        for nid in plan.order:
            node = plan.nodes[nid]
            inputs = [context.memo[i] for i in node.inputs]
            context.memo[nid] = node.op.execute(inputs, context)
        for r in root_ids:
            if r not in plan.nodes:
                raise KeyError(f"unknown root node {r!r}")
            results[r] = context.memo[r]
        return results

    def execute(self, plan: BoundPlan, root: str | None = None) -> Relation:
        root = root if root is not None else plan.roots[0]
        return self.execute_plan(plan, [root])[root]


def run(dag, catalog: Catalog, root: str | None = None) -> Relation:
    """Convenience: bind then execute (binding errors abort before execution)."""
    return Executor(catalog).execute(dag.bind(catalog), root)
