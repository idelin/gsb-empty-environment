#!/usr/bin/env python3.11
"""Runnable walkthrough for the retractable min/max interval network.

Run:  python3.11 demo.py
Demonstrates:
  1. exact B-A in [3,5] vs A-B >= 0 contradiction with constraint-ID cycle
  2. earliest/latest windows and an unconstrained event's full range
  3. anchoring the translational freedom
  4. push/pop hypotheses with bound relaxation on withdrawal
  5. named versions / history
  6. budget interruption that never returns a pseudo-feasible schedule
  7. exact integer time model: wall clock vs time point vs duration
  8. independent differential check against a full batch oracle
"""

from tempnet import (
    MinMaxNetwork,
    InconsistentNetwork,
    BudgetExceeded,
    Duration,
    TimePoint,
    MICROSECOND_TABLE,
)
from tempnet.verifier import run_differential, RandomScenario


def section(title: str) -> None:
    print("\n" + "=" * 68)
    print(title)
    print("=" * 68)


def main() -> None:
    section("1. Contradiction: B-A in [3,5] and A-B >= 0")
    net = MinMaxNetwork(["A", "B"])
    net.add_interval("C1:B-A<=5", "A", "B", None, 5)
    net.add_interval("C2:B-A>=3", "A", "B", 3, None)
    print("after adding the two bounds on B-A, a feasible schedule is",
          net.solve())
    try:
        net.add_interval("C3:A-B>=0", "B", "A", 0, None)
    except InconsistentNetwork as err:
        print("adding A-B >= 0 makes it infeasible:")
        print("   ", err.cycle.describe())
        print("    constraint IDs proving the contradiction:",
              err.cycle.constraint_ids)

    section("2. Earliest/latest windows; unconstrained event alone")
    plan = MinMaxNetwork(["start", "mid", "end", "free"])
    plan.add_exact("anchor-start", None, "start", 0)
    plan.add_interval("start->mid", "start", "mid", 3, 7)
    plan.add_interval("mid->end", "mid", "end", 2, 4)
    for name, b in plan.all_bounds().items():
        print("   ", b.describe())
    sched = plan.solve()
    print("one feasible schedule:", sched)
    print("direct verification of that schedule:", plan.verify_assignment(sched))

    section("3. Translation freedom before anchoring")
    rel = MinMaxNetwork(["P", "Q"])
    rel.add_interval("Q-P", "P", "Q", 3, 5)
    print("with no anchor, bounds are", rel.bounds("P").describe(),
          "and", rel.bounds("Q").describe())
    print("normalized feasible schedule:", rel.solve())
    rel.anchor("P", 100)  # pin the freedom
    print("after anchor(P=100):", rel.solve())

    section("4. push/pop hypotheses; withdrawal relaxes supported bounds")
    work = MinMaxNetwork(["X", "Y"])
    work.add_exact("X=0", None, "X", 0)
    work.add_interval("loose: 0<=Y-X<=10", "X", "Y", 0, 10)
    print("latest Y initially:", work.bounds("Y").latest)
    work.push()
    work.add_max("tight: Y-X<=4", "X", "Y", 4)
    print("latest Y under hypothesis:", work.bounds("Y").latest)
    work.pop()
    print("latest Y after the hypothesis is withdrawn:",
          work.bounds("Y").latest)

    section("5. Named versions (history)")
    work.save_version("baseline")
    work.add_min("new-min Y-X>=8", "X", "Y", 8)
    print("latest/earliest Y with the new minimum:",
          (work.bounds("Y").earliest, work.bounds("Y").latest))
    work.restore_version("baseline")
    print("restored window of Y:",
          (work.bounds("Y").earliest, work.bounds("Y").latest),
          "| stored versions:", work.versions())

    section("6. Budget interruption is atomic (no pseudo-feasible answer)")
    chain = MinMaxNetwork([f"n{i}" for i in range(6)])
    for i in range(5):
        chain.add_min(f"edge{i}", f"n{i}", f"n{i+1}", 1)
    try:
        chain.add_max("shortcut n0-n5<=9", "n0", "n5", 9, budget=3)
    except BudgetExceeded as err:
        print("propagation stopped:", err)
    print("network still feasible:", not chain.infeasible,
          "| shortcut stored:",
          "shortcut n0-n5<=9" in [c.cid for c in chain.constraints])
    chain.anchor("n0", 0)
    print("schedule returned after interruption is genuinely valid:",
          chain.verify_assignment(chain.solve()) == [])
    print("earliest n5 (chain of 1s) =", chain.bounds("n5").earliest)

    section("7. Exact integer time: duration / time point / wall clock")
    tab = MICROSECOND_TABLE
    gap = Duration(tab.ticks(1, "second") + tab.ticks(500, "millisecond"))
    t0 = TimePoint(0)
    t1 = t0 + gap
    print("1.5 s duration in microseconds:", gap.ticks)
    print("t1 - t0 is a Duration:", t1 - t0)
    wall = t1.to_wall(tab, epoch_label="mission")
    print("as wall clock:", wall.format())
    try:
        Duration(tab.ticks(1, "second") + 5).in_unit(tab, "second")
    except ValueError as err:
        print("inexact conversion is rejected, never rounded:", err)

    section("8. Independent full-algorithm cross-check on random updates")
    agreements = run_differential(
        RandomScenario(seed=20260926, n_events=7, n_steps=500), verbose=True
    )
    print(f"oracle agreed with incremental network at {agreements} checkpoints")
    print("ALL DEMONSTRATIONS COMPLETE")


if __name__ == "__main__":
    main()
