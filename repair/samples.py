"""Built-in synthetic sample scenarios (exact ints and rationals)."""
from __future__ import annotations

SAMPLES = {
    # A resource failure [6,9) displaces task A.  Moving only A forces it
    # to start 15 (cost 9); moving A and B would cost 4 but moves 2 tasks.
    # Lexicographic objective keeps B and C in place: 1 move, cost 9.
    "resource_failure": {
        "now": 5,
        "tasks": [
            {"id": "E", "machine": "M1", "duration": 3, "start": 0,
             "executed": True},
            {"id": "A", "machine": "M1", "duration": 2, "start": 6},
            {"id": "B", "machine": "M1", "duration": 2, "start": 10},
            {"id": "C", "machine": "M1", "duration": 2, "start": 13},
        ],
        "new_tasks": [],
        "failures": [{"machine": "M1", "start": 6, "end": 9}],
    },
    # A new task is added; it fits between existing tasks, nobody moves.
    "new_task": {
        "now": 2,
        "tasks": [
            {"id": "E", "machine": "M1", "duration": 2, "start": 0,
             "executed": True},
            {"id": "A", "machine": "M1", "duration": 2, "start": 2},
            {"id": "B", "machine": "M1", "duration": 1, "start": 5},
        ],
        "new_tasks": [{"id": "N", "machine": "M1", "duration": 1}],
        "failures": [],
    },
    # The executed prefix itself overlaps a failure: cannot be repaired
    # (executed tasks cannot move or be re-run).  Original plan is kept.
    "frozen_conflict": {
        "now": 8,
        "tasks": [
            {"id": "E", "machine": "M1", "duration": 3, "start": 4,
             "executed": True},
            {"id": "A", "machine": "M1", "duration": 1, "start": 9},
        ],
        "new_tasks": [],
        "failures": [{"machine": "M1", "start": 5, "end": 8}],
    },
    # Same as resource_failure but with subset budget 0: the search cannot
    # even test the zero-extra-move level, so feasibility stays undecided.
    "budget_undecided": {
        "now": 5,
        "tasks": [
            {"id": "E", "machine": "M1", "duration": 3, "start": 0,
             "executed": True},
            {"id": "A", "machine": "M1", "duration": 2, "start": 6},
            {"id": "B", "machine": "M1", "duration": 2, "start": 10},
            {"id": "C", "machine": "M1", "duration": 2, "start": 13},
        ],
        "new_tasks": [],
        "failures": [{"machine": "M1", "start": 6, "end": 9}],
        "budget": {"subset": 0, "placement": 100000},
    },
    # Permanent shutdown [10,inf) fragments the free time so the new task
    # N (duration 3) fits nowhere: at least one existing task must move.
    # Moving A costs 11, moving C costs 3 (optimal).  A subset budget of
    # 2 only covers the empty subset and moving A: FEASIBLE with cost
    # 11, while a full budget proves OPTIMAL with cost 3.
    "limited_budget": {
        "now": 0,
        "tasks": [
            {"id": "A", "machine": "M1", "duration": 2, "start": 7},
            {"id": "B", "machine": "M1", "duration": 2, "start": 0},
            {"id": "C", "machine": "M1", "duration": 2, "start": 4},
        ],
        "new_tasks": [{"id": "N", "machine": "M1", "duration": 3}],
        "failures": [{"machine": "M1", "start": 10, "end": None}],
        "budget": {"subset": 2, "placement": 100000},
    },
    # Permanent shutdown [4,inf): displaced task B (duration 3) plus task A
    # need 5 units but only 4 remain free.  Provably infeasible.
    "permanent_shutdown": {
        "now": 0,
        "tasks": [
            {"id": "A", "machine": "M1", "duration": 2, "start": 0},
            {"id": "B", "machine": "M1", "duration": 3, "start": 5},
        ],
        "new_tasks": [],
        "failures": [{"machine": "M1", "start": 4, "end": None}],
    },
    # Rational data: A is displaced by a failure and moves earlier to 1,
    # cost 1/2.  Exact Fraction arithmetic, no floats anywhere.
    "rationals": {
        "now": "1/2",
        "tasks": [
            {"id": "A", "machine": "M1", "duration": "1/2", "start": "3/2"},
            {"id": "B", "machine": "M1", "duration": "1/2", "start": 3},
        ],
        "new_tasks": [],
        "failures": [{"machine": "M1", "start": "3/2", "end": "5/2"}],
    },
}
