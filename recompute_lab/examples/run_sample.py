"""End-to-end sample: optimal chain plan, DAG heuristic + lower bound,
deterministic recompute, gradient equivalence and independent verification."""
import random
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent.parent))

import minitensor as mt
from chain_dp import solve_chain, brute_force_chain
from dag_plan import greedy_dag_plan
from engine import Engine, Node
from numerical import finite_difference_grad
from simulator import simulate_plan, verify_trace


def banner(title):
    print("\n" + "=" * 64)
    print(title)
    print("=" * 64)


# ---------------------------------------------------------------- 1. chain DP
banner("1. Chain: optimal checkpoint plan under a memory budget")
sizes = [2, 4, 3, 5, 2, 6, 3, 4]   # activation sizes of nodes 0..7
costs = [0, 5, 2, 8, 3, 7, 4, 6]   # recompute costs
budget = 18
plan = solve_chain(sizes, costs, budget)
bf = brute_force_chain(sizes, costs, budget)
print(f"sizes={sizes}\ncosts={costs}\nbudget={budget}")
print(f"checkpoints      : {plan.checkpoints}")
print(f"recompute cost   : {plan.recompute_cost}  (brute force agrees: {bf.recompute_cost})")
print(f"peak memory      : {plan.peak_memory} <= {budget}")

# ------------------------------------------------------- 2. DAG heuristic + LB
banner("2. DAG: greedy heuristic with a reported lower bound")
order = list(range(7))
parents = {0: [], 1: [0], 2: [1], 3: [2, 1], 4: [3], 5: [4, 2], 6: [5]}
dsizes = {v: 1 for v in order}
dcosts = {0: 0, 1: 4, 2: 2, 3: 7, 4: 3, 5: 5, 6: 1}
dbudget = 6
dag = greedy_dag_plan(order, parents, dsizes, dcosts, dbudget)
print(f"parents={parents}")
print(f"sizes={dsizes} costs={dcosts} budget={dbudget}")
print(f"checkpoints      : {sorted(dag.checkpoints)}")
print(f"heuristic cost   : {dag.recompute_cost}")
print(f"lower bound      : {dag.lower_bound}  (optimum lies in "
      f"[{dag.lower_bound}, {dag.recompute_cost}])")
print(f"peak memory      : {dag.peak_memory} <= {dbudget}")

# ------------------------------------------------- 3. engine: grads + RNG replay
banner("3. Engine: checkpointed backward == full-save backward (exact)")
rng3 = random.Random(3)
nodes = [Node("x", "input")]
inputs = {"x": mt.Tensor.rand((2, 3), rng3)}
prev = "x"
for layer in range(3):
    w = f"w{layer}"
    nodes.append(Node(w, "input"))
    inputs[w] = mt.Tensor.rand((3, 3), rng3)
    nodes.append(Node(f"mm{layer}", "matmul", (prev, w)))
    nodes.append(Node(f"nl{layer}", "tanh", (f"mm{layer}",)))
    nodes.append(Node(f"dr{layer}", "dropout", (f"nl{layer}",), param=0.3))
    prev = f"dr{layer}"                       # random op, must replay exactly
nodes.append(Node("rv", "reshape", (prev,), param=(6,)))  # shared-storage view
nodes.append(Node("ls", "sum", ("rv",)))
names = [n.name for n in nodes]
full = Engine(nodes, seed=2026).run(inputs, set(names))
plan3 = {n.name for n in nodes if n.op == "input"} | {"mm0", "mm1", "mm2"}
ckpt = Engine(nodes, seed=2026).run(inputs, plan3)
print(f"full-save peak   : {full[2]['peak_memory']} elements")
print(f"checkpoint peak  : {ckpt[2]['peak_memory']} elements")
print(f"recomputes       : {ckpt[2]['recompute_counts']}")
print(f"outputs identical: {full[0].exactly_equal(ckpt[0])}")
print(f"grad x identical : {full[1]['x'].exactly_equal(ckpt[1]['x'])}")
print(f"grad w1 identical: {full[1]['w1'].exactly_equal(ckpt[1]['w1'])}")

# --------------------------------------------- 4. independent event simulation
banner("4. Independent event simulation of the same plan")
order = names
parents = {n.name: list(n.inputs) for n in nodes}
esizes = {n.name: (inputs[n.name].numel() if n.op == "input" else 6) for n in nodes}
esizes["ls"] = 1
ecosts = {n.name: 1.0 for n in nodes}
sim = simulate_plan(order, parents, esizes, ecosts, plan3 | {"ls"}, budget=10**6)
peak = verify_trace(ckpt[2]["events"], {e[1]: e[2] for e in ckpt[2]["events"]})
print(f"simulator: feasible={sim.feasible} peak={sim.peak_memory} "
      f"recompute_counts={sim.recompute_counts}")
print(f"engine trace re-validated by verify_trace: peak={peak} "
      f"(== engine stats {ckpt[2]['peak_memory']})")

# ---------------------------------------------------- 5. finite differences
banner("5. Independent numerical verification (finite differences)")
fd = finite_difference_grad(nodes, inputs, "x", eps=1e-5, seed=2026)
analytic = full[1]["x"].tolist_flat()
numeric = fd.tolist_flat()
max_err = max(abs(a - b) for a, b in zip(analytic, numeric))
print(f"analytic grad x  : {[round(v, 6) for v in analytic]}")
print(f"finite-difference: {[round(v, 6) for v in numeric]}")
print(f"max abs error    : {max_err:.3e}")
