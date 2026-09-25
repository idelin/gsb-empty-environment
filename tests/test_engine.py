import unittest

from checkpoint.engine import Engine, RecomputeError
from checkpoint.graph import Node, descendant_counts
from checkpoint.planner import (dag_lower_bound, gradient_workspace,
                                plan_chain_optimal, plan_dag_greedy)
from checkpoint.standard_nodes import (add_bwd, add_fwd, build_chain,
                                       loss_bwd, loss_fwd)
from checkpoint.tensor import Tensor, relu, relu_backward


def chain_setup(dims=(4, 6, 6, 3), seed=1):
    nodes, input_names, inputs = build_chain(list(dims), noise=0.1, seed=5)
    engine = Engine(nodes, input_names)
    full = engine.run(inputs, checkpoints=[n.name for n in nodes], seed=seed)
    sizes = [inputs["x"].nbytes]
    sizes += [8 * len(full.audit[nm][1]) for nm in engine.topo]
    costs = [0.0] + [engine.nodes[nm].cost for nm in engine.topo]
    workspace = gradient_workspace(
        [t.nbytes for t in inputs.values()], max(sizes))
    return engine, nodes, input_names, inputs, full, sizes, costs, workspace


def plan_to_names(plan, engine):
    return [engine.topo[k - 1] for k in plan[0] if k > 0]


class TestChainExecution(unittest.TestCase):
    def test_checkpointed_matches_full_save(self):
        (engine, nodes, input_names, inputs, full,
         sizes, costs, workspace) = chain_setup()
        budget = workspace + 2 * max(sizes) + sum(sizes) // 2
        plan = plan_chain_optimal(sizes, costs, budget, workspace)
        self.assertIsNotNone(plan)
        names = plan_to_names(plan, engine)
        self.assertLess(len(names), len(nodes))
        res = engine.run(inputs, checkpoints=names, seed=1)
        # gradients identical to full-save mode
        self.assertEqual(set(res.grads), set(full.grads))
        for nm in full.grads:
            self.assertEqual(res.grads[nm].data(), full.grads[nm].data(),
                             msg="gradient mismatch on %s" % nm)
        # recompute cost statistics match the plan exactly
        self.assertAlmostEqual(res.stats.recompute_cost, plan[1])
        self.assertGreater(res.stats.recompute_count, 0)
        # peak memory (incl. temporary gradients) respects the budget
        self.assertLessEqual(res.stats.peak_bytes, budget)
        # output identical too
        self.assertEqual(res.output.data(), full.output.data())

    def test_rng_replay_is_bit_exact(self):
        # the engine audits every recomputed tensor against the forward
        # pass; a stochastic-replay bug would raise AuditMismatch here
        (engine, nodes, input_names, inputs, full,
         sizes, costs, workspace) = chain_setup()
        budget = workspace + 2 * max(sizes) + sizes[0]  # minimal: only x
        plan = plan_chain_optimal(sizes, costs, budget, workspace)
        self.assertIsNotNone(plan)
        res = engine.run(inputs, checkpoints=plan_to_names(plan, engine),
                         seed=1)
        self.assertEqual(res.audit, full.audit)

    def test_finite_difference_gradient_check(self):
        # independent numerical verification of the backward pass
        (engine, nodes, input_names, inputs, full,
         sizes, costs, workspace) = chain_setup()
        eps = 1e-6

        def loss_value():
            return engine.run(inputs, checkpoints=[], seed=1).output.data()[0]

        for target in ("x", "W1"):
            base = inputs[target]
            grads = full.grads[target].data()
            for k in range(len(grads)):
                orig = base.storage.data[k]
                base.storage.data[k] = orig + eps
                plus = loss_value()
                base.storage.data[k] = orig - eps
                minus = loss_value()
                base.storage.data[k] = orig
                num = (plus - minus) / (2 * eps)
                self.assertAlmostEqual(num, grads[k], delta=1e-4,
                                       msg="finite-diff mismatch %s[%d]"
                                           % (target, k))


class TestSideEffects(unittest.TestCase):
    def build(self, log, replayable=False):
        def se_fwd(rng, x):
            log.append("ran")
            return x.clone()

        def se_bwd(g, inputs, out):
            return [g.clone()]

        nodes = [
            Node("SE", ["x"], se_fwd, se_bwd, cost=1.0,
                 side_effect=True, replayable=replayable),
            Node("loss", ["SE"], loss_fwd, loss_bwd, cost=1.0),
        ]
        inputs = {"x": Tensor([1.0, 2.0], (1, 2))}
        return Engine(nodes, ["x"]), inputs

    def test_undeclared_side_effect_cannot_recompute(self):
        log = []
        engine, inputs = self.build(log)
        with self.assertRaises(RecomputeError):
            engine.run(inputs, checkpoints=[], seed=0)

    def test_checkpointed_side_effect_is_safe(self):
        log = []
        engine, inputs = self.build(log)
        res = engine.run(inputs, checkpoints=["SE"], seed=0)
        self.assertEqual(log, ["ran"])  # executed exactly once
        self.assertEqual(res.stats.recompute_count, 0)

    def test_declared_replayable_may_recompute(self):
        log = []
        engine, inputs = self.build(log, replayable=True)
        res = engine.run(inputs, checkpoints=[], seed=0)
        self.assertGreater(len(log), 1)  # side effect re-ran: why we declare
        self.assertGreater(res.stats.recompute_count, 0)


def relu_fwd(rng, x):
    return relu(x)


def relu_bwd(g, inputs, out):
    return relu_backward(g, inputs[0])


def view_fwd(rng, x):
    return x.view()


def view_bwd(g, inputs, out):
    return [g.clone()]


class TestViewsInEngine(unittest.TestCase):
    def test_view_alias_identity_and_zero_cost(self):
        nodes = [
            Node("A", ["x"], relu_fwd, relu_bwd, cost=1.0),
            Node("V", ["A"], view_fwd, view_bwd, cost=0.0),
            Node("loss", ["V"], loss_fwd, loss_bwd, cost=1.0),
        ]
        engine = Engine(nodes, ["x"])
        inputs = {"x": Tensor([1.0, -2.0], (1, 2))}
        res = engine.run(inputs, checkpoints=["A", "V"], seed=0)
        # identity check: V recognised as a view of A's storage
        self.assertEqual(res.aliases.get("V"), "A")
        # the view costs no extra memory: only one 16-byte buffer for A
        alloc_v = [s for k, t, s in res.events if k == "alloc"
                   and t == "act:V"]
        self.assertEqual(alloc_v, [0])

    def test_stale_view_detected_on_replay(self):
        def inplace_fwd(rng, x):
            x.add_(Tensor.full(x.shape, 1.0))
            return x

        def inplace_bwd(g, inputs, out):
            return [g.clone()]

        nodes = [
            Node("A", ["x"], relu_fwd, relu_bwd, cost=1.0),
            Node("V", ["A"], view_fwd, view_bwd, cost=0.0),
            Node("M", ["A"], inplace_fwd, inplace_bwd, cost=1.0),
            Node("loss", ["V"], loss_fwd, loss_bwd, cost=1.0),
        ]
        engine = Engine(nodes, ["x"])
        inputs = {"x": Tensor([1.0, -2.0], (1, 2))}
        from checkpoint.tensor import VersionError
        with self.assertRaises(VersionError):
            engine.run(inputs, checkpoints=["A", "V"], seed=0)


def build_diamond():
    from checkpoint.standard_nodes import layer_bwd, layer_fwd
    from checkpoint.rng import Rng
    rng = Rng(9)
    input_names = ["x", "W1", "W2", "W3"]
    inputs = {
        "x": Tensor.randn((1, 4), rng, scale=0.5),
        "W1": Tensor.randn((4, 6), rng, scale=0.3),
        "W2": Tensor.randn((6, 6), rng, scale=0.3),
        "W3": Tensor.randn((6, 6), rng, scale=0.3),
    }
    nodes = [
        Node("A", ["x", "W1"], layer_fwd, layer_bwd, cost=4.0),
        Node("B", ["A", "W2"], layer_fwd, layer_bwd, cost=4.0),
        Node("C", ["A", "W3"], layer_fwd, layer_bwd, cost=4.0),
        Node("D", ["B", "C"], add_fwd, add_bwd, cost=1.0),
        Node("loss", ["D"], loss_fwd, loss_bwd, cost=1.0),
    ]
    return nodes, input_names, inputs


class TestDagExecution(unittest.TestCase):
    def test_dag_heuristic_plan_with_lower_bound(self):
        nodes, input_names, inputs = build_diamond()
        engine = Engine(nodes, input_names)
        full = engine.run(inputs, checkpoints=[n.name for n in nodes],
                          seed=2)
        sizes = {nm: 8 * len(full.audit[nm][1]) for nm in engine.topo}
        costs = {nm: engine.nodes[nm].cost for nm in engine.topo}
        workspace = gradient_workspace(
            [t.nbytes for t in inputs.values()], max(sizes.values()))
        budget = workspace + 2 * max(sizes.values()) + 2 * 48 + 8
        plan = plan_dag_greedy(sizes, costs, budget, workspace,
                               descendants=descendant_counts(nodes))
        self.assertIsNotNone(plan)
        lb = dag_lower_bound(sizes, costs, budget, workspace)
        res = engine.run(inputs, checkpoints=plan, seed=2)
        # gradients identical to full-save mode
        for nm in full.grads:
            self.assertEqual(res.grads[nm].data(), full.grads[nm].data())
        # memory budget respected, lower bound reported and not violated
        self.assertLessEqual(res.stats.peak_bytes, budget)
        self.assertGreaterEqual(res.stats.recompute_cost, lb)
        self.assertGreater(res.stats.recompute_count, 0)


if __name__ == "__main__":
    unittest.main()
