"""Plan execution with adaptive replanning at safe materialization boundaries.

Each join stage is fully materialized before the next starts, so a stage
boundary is a safe switch point: no output has been emitted yet (rows are
emitted only from the final materialized stage, in chunks).  Therefore
replanning can never duplicate already-emitted rows, and the final multiset
is identical to the one the original plan would have produced.

Every decision (initial plan, replan, partitioned path, terminal state) is
logged with its numeric inputs so it can be recomputed and replayed.

Explicit states: COMPLETED, BUDGET_EXHAUSTED, CANCELLED (terminal status),
plus STALE_STATS / REPLANNED / SPILLED flags in `states`.
"""
from __future__ import annotations

from dataclasses import dataclass

from .cost import estimate_join
from .planner import choose_order, resolve_edge
from .relations import execute_join, exact_ndv
from .stats import Interval, RelStats

COMPLETED = "COMPLETED"
BUDGET_EXHAUSTED = "BUDGET_EXHAUSTED"
CANCELLED = "CANCELLED"
STALE_STATS = "STALE_STATS"
REPLANNED = "REPLANNED"
SPILLED = "SPILLED"


@dataclass
class ExecutionReport:
    status: str
    states: list
    decisions: list
    emitted: list
    final: object        # Relation when status == COMPLETED, else None
    cost_spent: float


class Executor:
    def __init__(self, relations, provider, cost_model, *,
                 budget=float("inf"), chunk_size=10000,
                 deviation_threshold=2.0, partitions=64,
                 cancel_check=None, boundary_hook=None):
        self.relations = relations
        self.provider = provider
        self.cost_model = cost_model
        self.budget = budget
        self.chunk_size = chunk_size
        self.deviation_threshold = deviation_threshold
        self.partitions = partitions
        self.cancel_check = cancel_check
        self.boundary_hook = boundary_hook

    def _snapshot(self, query):
        return {n: self.provider.get(n) for n in query.relations()}

    def _replan(self, placed, remaining, snapshot, current, decisions, reason):
        """Replan the remaining steps at a materialization boundary.

        The already-materialized intermediate becomes the new base with
        *exact* statistics measured from the data; unplaced relations keep
        their (possibly interval) catalog statistics.
        """
        needed = {lk for step in remaining
                  for src, lk, rk in step.edges if src in placed}
        partial = RelStats(
            rows=Interval.point(current.nrows),
            ndv={lk: Interval.point(exact_ndv(current, lk)) for lk in needed},
        )
        choice = choose_order(partial, set(placed), list(remaining),
                              snapshot, self.cost_model)
        decisions.append({
            "kind": "replan",
            "reason": reason,
            "placed": sorted(placed),
            "partial_rows": current.nrows,
            "new_order": [s.rel for s in choice.primary.order],
            "pessimistic_cost": choice.primary.pessimistic,
            "alternatives": [[s.rel for s in p.order] for p in choice.alternatives],
        })
        return list(choice.primary.order), list(choice.primary.stage_estimates)

    def run(self, query) -> ExecutionReport:
        states: set = set()
        decisions: list = []
        emitted: list = []
        cost_spent = 0.0
        status = None

        snapshot = self._snapshot(query)
        plan_epoch = self.provider.epoch
        choice = choose_order(snapshot[query.base], {query.base},
                              list(query.steps), snapshot, self.cost_model)
        decisions.append({
            "kind": "initial_plan",
            "order": [query.base] + [s.rel for s in choice.primary.order],
            "pessimistic_cost": choice.primary.pessimistic,
            "optimistic_cost": choice.primary.optimistic,
            "alternatives": [[s.rel for s in p.order] for p in choice.alternatives],
            "stats_epoch": plan_epoch,
        })

        current = self.relations[query.base]
        placed = {query.base}
        remaining = list(choice.primary.order)
        expected = list(choice.primary.stage_estimates)

        while remaining:
            if self.boundary_hook is not None:
                self.boundary_hook()
            if self.cancel_check is not None and self.cancel_check():
                status = CANCELLED
                decisions.append({"kind": "cancelled", "placed": sorted(placed)})
                break
            if self.provider.epoch != plan_epoch:
                states.add(STALE_STATS)
                states.add(REPLANNED)
                snapshot = self._snapshot(query)
                plan_epoch = self.provider.epoch
                remaining, expected = self._replan(
                    placed, remaining, snapshot, current, decisions,
                    reason="stale_stats")

            step = remaining[0]
            right = self.relations[step.rel]
            src, lk, rk = resolve_edge(step, placed)
            build_bytes = right.nrows * self.cost_model.row_width
            partitioned = build_bytes > self.cost_model.memory_bytes
            if partitioned:
                states.add(SPILLED)
                decisions.append({
                    "kind": "partitioned_join",
                    "step": step.rel,
                    "build_rows": right.nrows,
                    "build_bytes": build_bytes,
                    "memory_bytes": self.cost_model.memory_bytes,
                })
            result = execute_join(current, right, lk, rk, step.join_type,
                                  partitioned=partitioned,
                                  partitions=self.partitions)
            est = estimate_join(self.cost_model, current.nrows, right.nrows,
                                exact_ndv(current, lk), exact_ndv(right, rk),
                                step.join_type)
            cost_spent += est.total
            decisions.append({
                "kind": "stage",
                "step": step.rel,
                "join_type": step.join_type,
                "partitioned": partitioned,
                "left_rows": current.nrows,
                "right_rows": right.nrows,
                "out_rows": result.nrows,
                "stage_cost": est.total,
            })
            current = result
            placed.add(step.rel)
            remaining.pop(0)
            est_rows = expected.pop(0)

            if cost_spent > self.budget:
                status = BUDGET_EXHAUSTED
                decisions.append({"kind": "budget_exhausted",
                                  "cost_spent": cost_spent,
                                  "budget": self.budget})
                break
            if remaining:
                obs = current.nrows
                ratio = (obs + 1.0) / (est_rows + 1.0)
                deviation = max(ratio, 1.0 / ratio)
                if deviation > self.deviation_threshold:
                    states.add(REPLANNED)
                    remaining, expected = self._replan(
                        placed, remaining, snapshot, current, decisions,
                        reason=f"estimate_deviation observed={obs} "
                               f"expected={est_rows:.1f}")

        if status is None:
            rows = current.rows
            finished = True
            for i in range(0, len(rows), self.chunk_size):
                if self.cancel_check is not None and self.cancel_check():
                    status = CANCELLED
                    decisions.append({"kind": "cancelled", "during": "emit",
                                      "emitted": len(emitted)})
                    finished = False
                    break
                chunk = rows[i:i + self.chunk_size]
                emitted.extend(chunk)
                cost_spent += self.cost_model.cpu_per_row * len(chunk)
                if cost_spent > self.budget:
                    status = BUDGET_EXHAUSTED
                    decisions.append({"kind": "budget_exhausted",
                                      "during": "emit",
                                      "emitted": len(emitted),
                                      "cost_spent": cost_spent,
                                      "budget": self.budget})
                    finished = False
                    break
            if finished:
                status = COMPLETED

        return ExecutionReport(
            status=status,
            states=sorted(states),
            decisions=decisions,
            emitted=emitted,
            final=current if status == COMPLETED else None,
            cost_spent=cost_spent,
        )
