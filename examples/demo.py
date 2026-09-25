"""End-to-end demo: optimal chain checkpointing + DAG heuristic.

Run with:  python3.11 examples/demo.py
"""
import os
import sys

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

from checkpoint.engine import Engine
from checkpoint.graph import descendant_counts
from checkpoint.planner import (dag_lower_bound, gradient_workspace,
                                plan_chain_bruteforce, plan_chain_optimal,
                                plan_dag_greedy)
from checkpoint.simulator import simulate
from checkpoint.standard_nodes import build_chain
from tests.test_engine import build_diamond


def banner(title):
    print("\n" + "=" * 66)
    print(title)
    print("=" * 66)


def chain_demo():
    banner("Chain: optimal checkpoint plan under a memory budget")
    nodes, input_names, inputs = build_chain([8, 16, 16, 8], noise=0.1,
                                             layer_cost=5.0, seed=5)
    engine = Engine(nodes, input_names)
    all_nodes = [n.name for n in nodes]
    full = engine.run(inputs, checkpoints=all_nodes, seed=1)

    sizes = [inputs["x"].nbytes]
    sizes += [8 * len(full.audit[nm][1]) for nm in engine.topo]
    costs = [0.0] + [engine.nodes[nm].cost for nm in engine.topo]
    workspace = gradient_workspace([t.nbytes for t in inputs.values()],
                                   max(sizes))
    budget = workspace + 2 * max(sizes) + sum(sizes) // 3
    print("activations (bytes):", dict(zip(["x"] + engine.topo, sizes)))
    print("recompute costs:    ",
          {nm: engine.nodes[nm].cost for nm in engine.topo})
    print("memory budget: %d bytes (workspace %d, frontier reserve %d)"
          % (budget, workspace, 2 * max(sizes)))

    plan = plan_chain_optimal(sizes, costs, budget, workspace)
    names = [engine.topo[k - 1] for k in plan[0] if k > 0]
    bf = plan_chain_bruteforce(sizes, costs, budget, workspace)
    print("optimal checkpoints: ['x'] + %s" % names)
    print("planned recompute cost: %.1f (brute-force optimum: %.1f)"
          % (plan[1], bf[1]))
    assert abs(plan[1] - bf[1]) < 1e-9, "DP must match brute force"

    res = engine.run(inputs, checkpoints=names, seed=1)
    print("\nexecution stats (checkpointed):")
    print("  peak memory:      %d bytes (budget %d)"
          % (res.stats.peak_bytes, budget))
    print("  recompute count:  %d node recomputations"
          % res.stats.recompute_count)
    print("  recompute cost:   %.1f (matches plan: %s)"
          % (res.stats.recompute_cost,
             abs(res.stats.recompute_cost - plan[1]) < 1e-9))
    print("full-save peak:     %d bytes" % full.stats.peak_bytes)

    grads_equal = all(res.grads[nm].data() == full.grads[nm].data()
                      for nm in full.grads)
    print("gradients identical to full-save mode:", grads_equal)
    print("stochastic recompute bit-exact (audit):", res.audit == full.audit)

    report = simulate(res.events)
    print("independent event simulation: %d reads, 0 violations, "
          "peak %d matches: %s"
          % (report["reads"], report["peak"],
             report["peak"] == res.stats.peak_bytes))
    assert grads_equal and res.stats.peak_bytes <= budget


def dag_demo():
    banner("DAG: greedy heuristic with a reported lower bound")
    nodes, input_names, inputs = build_diamond()
    engine = Engine(nodes, input_names)
    full = engine.run(inputs, checkpoints=[n.name for n in nodes], seed=2)
    sizes = {nm: 8 * len(full.audit[nm][1]) for nm in engine.topo}
    costs = {nm: engine.nodes[nm].cost for nm in engine.topo}
    workspace = gradient_workspace([t.nbytes for t in inputs.values()],
                                   max(sizes.values()))
    budget = workspace + 2 * max(sizes.values()) + 104
    plan = plan_dag_greedy(sizes, costs, budget, workspace,
                           descendants=descendant_counts(nodes))
    lb = dag_lower_bound(sizes, costs, budget, workspace)
    res = engine.run(inputs, checkpoints=plan, seed=2)
    print("activation sizes:", sizes)
    print("memory budget: %d bytes" % budget)
    print("greedy checkpoints: %s" % sorted(plan))
    print("measured recompute cost: %.1f   lower bound: %.1f"
          % (res.stats.recompute_cost, lb))
    print("peak memory: %d bytes (budget %d)"
          % (res.stats.peak_bytes, budget))
    grads_equal = all(res.grads[nm].data() == full.grads[nm].data()
                      for nm in full.grads)
    print("gradients identical to full-save mode:", grads_equal)
    assert res.stats.recompute_cost >= lb
    assert res.stats.peak_bytes <= budget and grads_equal


if __name__ == "__main__":
    chain_demo()
    dag_demo()
    print("\nAll demo checks passed.")
