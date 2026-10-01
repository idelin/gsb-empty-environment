"""Demo: verify the reusable barrier model and hunt for round confusion.

Run with:  python3.11 run_demo.py
"""
from __future__ import annotations

from barrier_model import BarrierModel
from model_check import (bfs_safety, check_liveness, replay, replay_lasso)


def show_ce(model, result):
    print(f"    shortest counterexample ({len(result.counterexample)} steps):")
    for i, a in enumerate(result.counterexample):
        print(f"      {i:2d}. {a[0]} {a[1] if a[1] >= 0 else ''}".rstrip())
    rep = replay(model, result.counterexample)
    print(f"    replay: ok={rep.ok} violation={rep.violation}")


def main():
    print("=" * 72)
    print("1. Correct model, safety, full interleaving (n=3)")
    m = BarrierModel(n=3)
    r = bfs_safety(m)
    print(f"   verdict={r.verdict}  states={r.states}  transitions={r.transitions}")
    print("   (PASS = complete closure certificate: every reachable state checked)")

    print("=" * 72)
    print("2. Correct model, safety, symmetry reduction (n=3)")
    r = bfs_safety(m, use_symmetry=True)
    print(f"   verdict={r.verdict}  states={r.states}  transitions={r.transitions}")
    print("   same verdict as the full interleaving, ~5x fewer states")

    print("=" * 72)
    print("3. Round-confusion bugs: shortest counterexamples (n=3)")
    for name, kw in [
        ("stale arrival counted in new round", dict(bug_stale_arrival=True)),
        ("stale notification wakes new-round waiter", dict(bug_stale_release=True)),
        ("commit without reset -> double release", dict(bug_commit_no_reset=True)),
    ]:
        mb = BarrierModel(n=3, **kw)
        rb = bfs_safety(mb)
        print(f"   bug[{name}]: verdict={rb.verdict} violation={rb.violation}")
        show_ce(mb, rb)

    print("=" * 72)
    print("4. Fair progress (liveness) of the correct model (n=3)")
    r = check_liveness(m, fair=True)
    print(f"   weak-fair:   verdict={r.verdict}  states={r.states}")
    r = check_liveness(m, fair=False)
    print(f"   no fairness: verdict={r.verdict}  victim=p{r.victim}")
    if r.verdict == "FAIL":
        lr = replay_lasso(m, r.prefix, r.cycle, r.victim)
        print(f"     lasso: prefix={len(r.prefix)} steps, cycle={len(r.cycle)} steps")
        print(f"     replay: closed={lr.closed} victim_stuck={lr.victim_stuck}")
        print("     (an unfair scheduler can starve a waiter; fairness is required)")

    print("=" * 72)
    print("5. Liveness bug: blocking wait + commit never fires (n=2)")
    mb = BarrierModel(n=2, bug_no_commit=True, blocking_wait=True)
    rb = check_liveness(mb, fair=True)
    print(f"   verdict={rb.verdict}  victim=p{rb.victim}")
    print(f"   lasso prefix: {rb.prefix}")
    print(f"   lasso cycle:  {rb.cycle}")
    lr = replay_lasso(mb, rb.prefix, rb.cycle, rb.victim)
    print(f"   replay: prefix_ok={lr.prefix_ok} cycle_ok={lr.cycle_ok} "
          f"closed={lr.closed} victim_stuck={lr.victim_stuck}")

    print("=" * 72)
    print("6. Budget discipline: insufficient budget must not claim PASS")
    r = bfs_safety(m, max_states=100)
    print(f"   safety  max_states=100: verdict={r.verdict} states={r.states}")
    r = check_liveness(m, max_states=100)
    print(f"   liveness max_states=100: verdict={r.verdict} states={r.states}")
    print("   (UNKNOWN = search incomplete; the question stays undecided)")


if __name__ == "__main__":
    main()
