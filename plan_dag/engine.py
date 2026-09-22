"""Execute a bound :class:`~plan_dag.codec.Plan`.

A node is evaluated at most once even when multiple parents share it:
results are memoized by node id, so a shared subtree stays shared after
serialize -> deserialize -> run.
"""

from __future__ import annotations

from dataclasses import dataclass, field

from .catalog import Catalog
from .codec import Plan
from .errors import ExecutionError
from .types import Relation


@dataclass
class ExecStats:
    evaluations: dict[str, int] = field(default_factory=dict)

    @property
    def total_evaluations(self) -> int:
        return sum(self.evaluations.values())


@dataclass
class ExecContext:
    catalog: Catalog
    stats: ExecStats = field(default_factory=ExecStats)

    def run_plan(self, plan: Plan) -> Relation:
        cache: dict[str, Relation] = {}

        def evaluate(node_id: str) -> Relation:
            if node_id in cache:
                return cache[node_id]
            node = plan.nodes.get(node_id)
            if node is None:
                raise ExecutionError(f"execution reached unknown node {node_id!r}")
            if node.bound is None:
                raise ExecutionError(
                    f"node {node_id!r} was not bound; refusing to execute"
                )
            inputs = [evaluate(child) for child in node.inputs]
            relation = node.run(inputs, self)
            cache[node_id] = relation
            self.stats.evaluations[node_id] = self.stats.evaluations.get(node_id, 0) + 1
            return relation

        return evaluate(plan.root)


def execute(plan: Plan, catalog: Catalog) -> tuple[Relation, ExecStats]:
    context = ExecContext(catalog)
    return context.run_plan(plan), context.stats
