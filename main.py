"""Runnable demo for the budgeted-coverage toolkit (Python 3.11, stdlib only).

Run:  python3.11 main.py
"""
from __future__ import annotations

import os
import tempfile
from fractions import Fraction

from budgeted_coverage import (
    BranchAndBound,
    CheckpointMismatch,
    CoverageSet,
    Instance,
    Status,
    brute_force,
    gap_report,
    greedy,
    make_witness,
    verify_witness,
)


def show(title, result, instance):
    print(f"  {title:<28} status={result.status.value:<16} "
          f"profit={result.profit}  cost={instance.total_cost(result.selected)} "
          f"(budget {instance.budget})  selected={sorted(result.selected)}")


def demo_integer_instance():
    print("== 1. Integer instance with dependencies, conflicts and min-cover ==")
    sets = [
        CoverageSet("free_boost", cost=0, elements=["a"]),
        CoverageSet("empty", cost=1, elements=[]),                # adds no profit
        CoverageSet("core", cost=3, elements=["a", "b", "c"]),
        CoverageSet("addon", cost=2, elements=["d"], requires=["core"]),
        CoverageSet("rival", cost=2, elements=["b", "c"], excludes=["core"]),
        CoverageSet("pair1", cost=1, elements=["e"]),
        CoverageSet("pair2", cost=1, elements=["e"]),
    ]
    weights = {"a": 4, "b": 3, "c": 3, "d": 5, "e": 6}
    min_cover = {"e": 2}  # element e only pays out when covered twice
    inst = Instance(sets, weights, budget=5, min_cover=min_cover)

    g = greedy(inst)
    show("greedy", g, inst)
    exact = brute_force(inst)
    show("exact enumeration", exact, inst)
    bnb = BranchAndBound(inst).solve()
    show("branch-and-bound", bnb, inst)
    print(f"  b&b explored {bnb.nodes_explored} nodes, {len(bnb.prunes)} prunes")
    print("  " + str(gap_report(g.profit, exact.profit)).replace("\n", "\n  "))
    print()
    return inst, exact


def demo_rational_instance():
    print("== 2. Rational (fractional) costs and weights ==")
    sets = [
        CoverageSet("s1", cost=Fraction(1, 2), elements=["x", "y"]),
        CoverageSet("s2", cost=Fraction(3, 4), elements=["y", "z"]),
        CoverageSet("s3", cost=Fraction(1, 1), elements=["x", "z"]),
    ]
    weights = {"x": Fraction(5, 2), "y": Fraction(3, 2), "z": Fraction(7, 3)}
    inst = Instance(sets, weights, budget=Fraction(3, 2))
    g = greedy(inst)
    show("greedy", g, inst)
    exact = brute_force(inst)
    show("exact enumeration", exact, inst)
    print("  " + str(gap_report(g.profit, exact.profit)).replace("\n", "\n  "))
    print()


def demo_resume():
    print("== 3. Resumable branch-and-bound with prune justifications ==")
    import random

    rng = random.Random(20261001)  # fixed seed: synthetic but reproducible
    elements = [f"e{j}" for j in range(14)]
    sets = [CoverageSet("freebie", cost=0, elements=rng.sample(elements, 2))]
    for i in range(15):
        kwargs = {}
        if i == 2:
            kwargs["excludes"] = ["set03"]
        if i == 5:
            kwargs["requires"] = ["set06"]
        sets.append(
            CoverageSet(
                f"set{i:02d}",
                cost=rng.randint(1, 4),
                elements=rng.sample(elements, 3),
                **kwargs,
            )
        )
    weights = {e: rng.randint(1, 4) for e in elements}
    inst = Instance(sets, weights, budget=8)

    solver = BranchAndBound(inst)
    partial = solver.solve(max_nodes=3)
    print(f"  after 3 nodes: status={partial.status.value} "
          f"incumbent={partial.profit} reliable_upper_bound={partial.upper_bound}")
    assert partial.status is Status.BUDGET_UNDECIDED

    path = os.path.join(tempfile.gettempdir(), "bnb_checkpoint.json")
    solver.save_checkpoint(path)
    resumed = BranchAndBound.load(inst, path)
    final = resumed.solve()
    print(f"  resumed to completion: status={final.status.value} profit={final.profit} "
          f"selected={sorted(final.selected)}")
    print(f"  total nodes={final.nodes_explored}, prunes={len(final.prunes)}; sample reasons:")
    seen = set()
    for p in final.prunes:
        if p.reason in seen:
            continue
        seen.add(p.reason)
        print(f"    [{p.reason}] depth={p.depth}: {p.detail}")
    os.remove(path)
    print()
    return inst, final


def demo_dynamic_removal(inst, result):
    print("== 4. Dynamic set deletion invalidates old witnesses ==")
    witness = make_witness(inst, result.selected, result.profit, result.status)
    check = verify_witness(inst, witness)
    print(f"  witness valid before change: {check.ok}")
    victim = sorted(result.selected)[0] if result.selected else sorted(inst.sets)[0]
    smaller = inst.remove_set(victim)
    stale = verify_witness(smaller, witness)
    print(f"  removed set {victim!r}; old witness re-validates: {stale.ok}")
    for reason in stale.reasons:
        print(f"    - {reason}")
    try:
        path = os.path.join(tempfile.gettempdir(), "bnb_checkpoint.json")
        BranchAndBound(inst).save_checkpoint(path)
        BranchAndBound.load(smaller, path)
    except CheckpointMismatch as exc:
        print(f"  checkpoint resume refused: {exc}")
    finally:
        os.remove(path)
    rerun = BranchAndBound(smaller).solve()
    show("re-solved after deletion", rerun, smaller)
    print()


def main():
    inst, exact = demo_integer_instance()
    demo_rational_instance()
    demo_resume()
    demo_dynamic_removal(inst, exact)


if __name__ == "__main__":
    main()
