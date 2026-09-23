"""Query executor: drives a plan, distinguishes the four endings:

* ``complete``  -- child pipeline exhausted normally;
* ``limit``     -- a Limit operator stopped early on purpose
                   (normal early termination, no error);
* ``budget``    -- a shared budget was breached mid-flight; partial
                   statistics and an explicitly-incomplete partial
                   aggregate (if any) are returned;
* ``cancelled`` -- cancel() was observed by an operator; temp
                   resources are released in all abort paths.
"""

from __future__ import annotations

from dataclasses import dataclass

from .budget import BudgetExhausted, BudgetTracker, Cancelled
from .operators import Limit, Operator


@dataclass
class QueryStats:
    rows: int          # rows scanned
    steps: int         # operator steps charged
    memory_peak: int   # bytes
    budget_kind: str | None = None
    budget_used: int | None = None
    budget_limit: int | None = None


@dataclass
class Result:
    status: str
    rows: list
    stats: QueryStats
    message: str | None = None
    partial: object | None = None  # PartialAggregate(complete=False) or None


def _limit_hit(node: Operator) -> bool:
    if isinstance(node, Limit) and node.hit:
        return True
    return any(_limit_hit(child) for child in node.children)


class Executor:
    def __init__(self, root: Operator, tracker: BudgetTracker) -> None:
        self.root = root
        self.tracker = tracker

    def cancel(self) -> None:
        self.tracker.cancel()

    def run(self) -> Result:
        rows: list = []
        status, message, partial = "complete", None, None
        budget_error: BudgetExhausted | None = None
        try:
            self.root.open()
            while True:
                row = self.root.next()
                if row is None:
                    break
                rows.append(row)
            if _limit_hit(self.root):
                status = "limit"
        except BudgetExhausted as exc:
            status = "budget"
            message = str(exc)
            budget_error = exc
            # The only legitimate source of mid-flight aggregate values;
            # the PartialAggregate is itself marked incomplete.
            partial = self.root.partial_result()
        except Cancelled as exc:
            status = "cancelled"
            message = str(exc)
        finally:
            # Always releases memory reservations and spill files.
            self.root.close()
        stats = QueryStats(
            rows=self.tracker.rows,
            steps=self.tracker.steps,
            memory_peak=self.tracker.peak_memory,
            budget_kind=None,
        )
        if budget_error is not None:
            stats.budget_kind = budget_error.kind
            stats.budget_used = budget_error.used
            stats.budget_limit = budget_error.limit
        return Result(status=status, rows=rows, stats=stats,
                      message=message, partial=partial)
