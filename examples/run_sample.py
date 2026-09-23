"""Runnable demo: python3 examples/run_sample.py

Covers: full aggregation, budget interruption with partial stats,
limit vs budget distinction, shared subplan accounting, and
cancellation releasing temp files.
"""

import os
import sys
import tempfile
import threading
import time

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

from minirel import (Aggregate, Budget, BudgetTracker, Executor, Filter,
                     HashJoin, Limit, NULL, Project, Scan, SharedSubplan,
                     Sort, Table, sql_eq)

EMP = Table(
    "employees",
    ("emp_id", "name", "dept_id", "salary"),
    [
        (1, "ada",    10,    90000),
        (2, "brian",  10,    NULL),   # NULL salary: skipped by SUM/AVG
        (3, "chen",   20,    80000),
        (4, "dora",   NULL,  70000),  # NULL dept: never joins
        (5, "eli",    20,    80000),  # duplicate salary -> bag semantics
        (5, "eli",    20,    80000),  # duplicate row kept (multiset)
        (6, "fang",   30,    60000),
    ],
)

DEPT = Table(
    "departments",
    ("dept_id", "dname"),
    [(10, "eng"), (20, "ops"), (20, "ops"), (30, "hr")],  # dup dept row
)


def banner(title):
    print("\n=== %s ===" % title)


def show(result):
    print("status      :", result.status)
    if result.message:
        print("message     :", result.message)
    print("rows        :", result.rows)
    s = result.stats
    print("stats       : rows=%d steps=%d peak_mem=%dB" %
          (s.rows, s.steps, s.memory_peak))
    if s.budget_kind:
        print("budget hit  : %s (%d/%d)" % (s.budget_kind, s.budget_used, s.budget_limit))
    if result.partial is not None:
        print("partial agg : complete=%s rows=%s" %
              (result.partial.complete, result.partial.rows))


def agg_plan(tracker, budget_rows=None):
    scan = Scan(EMP, tracker)
    return Aggregate(
        scan,
        group_keys=[lambda r: r[2]],
        aggs=[("count", None), ("count", 3), ("sum", 3), ("avg", 3)],
        tracker=tracker,
    )


def main():
    banner("1. full aggregation (COUNT(*), COUNT(salary), SUM, AVG per dept)")
    tracker = BudgetTracker(Budget())
    show(Executor(agg_plan(tracker), tracker).run())

    banner("2. same query, row budget = 4 -> partial stats, incomplete flag")
    tracker = BudgetTracker(Budget(max_rows=4))
    show(Executor(agg_plan(tracker), tracker).run())

    banner("3. join with NULL keys + bag semantics, LIMIT 3 (normal early stop)")
    tracker = BudgetTracker(Budget())
    join = HashJoin(Scan(EMP, tracker), Scan(DEPT, tracker),
                    left_key=lambda r: r[2], right_key=lambda r: r[0],
                    tracker=tracker)
    proj = Project(join, [lambda r: r[1], lambda r: r[5]], ("name", "dname"))
    show(Executor(Limit(proj, 3), tracker).run())

    banner("4. same join without limit but step budget = 12 (budget interrupt)")
    tracker = BudgetTracker(Budget(max_steps=12))
    join = HashJoin(Scan(EMP, tracker), Scan(DEPT, tracker),
                    left_key=lambda r: r[2], right_key=lambda r: r[0],
                    tracker=tracker)
    proj = Project(join, [lambda r: r[1], lambda r: r[5]], ("name", "dname"))
    show(Executor(proj, tracker).run())

    banner("5. duplicate subplan shares one execution and one budget")
    tracker = BudgetTracker(Budget())
    shared = SharedSubplan(Scan(EMP, tracker), tracker)
    left = Filter(shared.consumer(), lambda r: sql_eq(r[2], 10))
    right = Filter(shared.consumer(), lambda r: sql_eq(r[2], 20))
    join = HashJoin(left, right,
                    left_key=lambda r: 1, right_key=lambda r: 1,
                    tracker=tracker)
    result = Executor(join, tracker).run()
    show(result)
    print("note        : table has %d rows, budget charged rows=%d (read once)"
          % (EMP.cardinality(), result.stats.rows))
    shared.close()

    banner("6. cancel a spilling sort -> temp files released")
    big = Table("big", ("k", "v"),
                [(i, "row-%d" % i) for i in range(200_000)])
    tracker = BudgetTracker(Budget())
    with tempfile.TemporaryDirectory() as tmp:
        sort = Sort(Scan(big, tracker), key=lambda r: -r[0],
                    tracker=tracker, tempdir=tmp, spill_after=2_000)
        executor = Executor(sort, tracker)

        holder = {}
        def work():
            holder["result"] = executor.run()
        thread = threading.Thread(target=work)
        thread.start()
        time.sleep(0.05)
        executor.cancel()
        thread.join()
        show(holder["result"])
        print("temp files left:", os.listdir(tmp))


if __name__ == "__main__":
    main()
