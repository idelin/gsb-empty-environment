"""Runnable demo: solve, overload evidence, and propagation pruning."""

from cumulative import (
    InfeasibleError,
    Task,
    check,
    propagate,
    solve,
)


def demo_solve():
    print("=== 1. Solving an instance (capacity 2) ===")
    tasks = [
        Task("build", 3, 1, set(range(6))),
        Task("test", 2, 1, set(range(6))),
        Task("deploy", 4, 2, set(range(6))),
    ]
    assignment = solve(tasks, 2)
    print("schedule:", dict(sorted(assignment.items(), key=lambda kv: kv[1])))
    print("independent checker:", check(tasks, 2, assignment))
    usage = {}
    for task in tasks:
        for t in range(assignment[task.name], assignment[task.name] + task.duration):
            usage[t] = usage.get(t, 0) + task.demand
    print("resource profile:", {t: usage[t] for t in sorted(usage)})


def demo_overload():
    print("\n=== 2. Overload evidence (capacity 1) ===")
    tasks = [
        Task("x", 4, 1, {0, 1}),
        Task("y", 4, 1, {0, 1}),
    ]
    try:
        propagate(tasks, 1)
    except InfeasibleError as exc:
        print("infeasible:", exc)


def demo_pruning():
    print("\n=== 3. Timetable propagation pruning (capacity 2) ===")
    tasks = [
        Task("a", 5, 1, {0, 1, 2, 3}),
        Task("b", 2, 2, set(range(7))),
    ]
    print("before:", {t.name: sorted(t.domain) for t in tasks})
    domains = propagate(tasks, 2)
    print("after: ", {k: sorted(v) for k, v in domains.items()})


if __name__ == "__main__":
    demo_solve()
    demo_overload()
    demo_pruning()
