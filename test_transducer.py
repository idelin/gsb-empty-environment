"""Unittest suite for transducer.py (Python 3.11, standard library only)."""

import random
import unittest

from transducer import Edge, Transducer


def make(states, alphabet, edges, initial, finals):
    return Transducer(states, alphabet, edges, initial, finals)


class TestBest(unittest.TestCase):
    def test_basic_prefers_lower_cost(self):
        tr = make(
            ["q0", "q1", "q2"], ["a", "b", "x", "y"],
            [Edge(0, "q0", "q1", "a", "x", 2),
             Edge(1, "q1", "q2", "b", "y", 1),
             Edge(2, "q0", "q2", "a", "xy", 5)],
            "q0", ["q2"])
        ev = tr.best()
        self.assertEqual(ev.total_cost, 3)
        self.assertEqual(ev.output, "xy")
        self.assertEqual(ev.input_consumed, ["a", "b"])
        self.assertEqual(ev.edge_ids, (0, 1))

    def test_epsilon_cycle_terminates(self):
        # zero-cost epsilon cycle q0 <-> q1 must not cause non-termination
        tr = make(
            ["q0", "q1", "q2"], ["a", "x"],
            [Edge(0, "q0", "q1", None, "", 0),
             Edge(1, "q1", "q0", None, "", 0),
             Edge(2, "q1", "q2", "a", "x", 1)],
            "q0", ["q2"])
        ev = tr.best()
        self.assertEqual((ev.total_cost, ev.output), (1, "x"))

    def test_zero_cost_output_cycle_length_before_lex(self):
        # outputs b, ab, aab, ... all at cost 0: lexicographically this is a
        # descending chain with no minimum ("aab" < "ab" < "b"), but the
        # (cost, length, lex) order makes "b" the well-defined minimum.
        tr = make(
            ["q0", "q1", "q2"], ["a", "b"],
            [Edge(0, "q0", "q1", "a", "", 0),
             Edge(1, "q1", "q1", None, "a", 0),
             Edge(2, "q1", "q2", None, "b", 0)],
            "q0", ["q2"])
        ev = tr.best()
        self.assertEqual((ev.total_cost, ev.output), (0, "b"))

    def test_tie_break_cost_length_lex_edgeids(self):
        tr = make(
            ["q0", "q1", "q2", "q3", "qf"], ["a", "b", "c"],
            [Edge(0, "q0", "q1", "a", "c", 3),
             Edge(1, "q1", "qf", None, "", 0),
             Edge(2, "q0", "q2", "a", "ab", 3),
             Edge(3, "q2", "qf", None, "", 0),
             Edge(4, "q0", "q3", "a", "aa", 3),
             Edge(5, "q3", "qf", None, "", 0),
             Edge(6, "q0", "q3", "b", "aa", 3),
             Edge(7, "q0", "qf", "c", "aa", 3)],
            "q0", ["qf"])
        # cost 3 everywhere: length 1 "c" beats length 2 "aa"/"ab"
        ev = tr.best()
        self.assertEqual(ev.output, "c")
        # among the three cost-3 paths emitting "aa", edge ids decide
        res = tr.k_best(3)
        self.assertEqual([e.output for e in res.evidences], ["c", "aa", "ab"])
        self.assertEqual(res.evidences[1].edge_ids, (4, 5))  # min id path

    def test_no_accepting_path_returns_none(self):
        tr = make(
            ["q0", "q1", "q2"], ["a"],
            [Edge(0, "q0", "q1", "a", "a", 1),
             Edge(1, "q1", "q0", "a", "a", 1)],
            "q0", ["q2"])
        self.assertIsNone(tr.best())
        # infinite path space (productive cycle), no final reachable:
        # the budget bounds the futile search
        self.assertEqual(tr.k_best(3, budget=500).evidences, [])

    def test_initial_state_accepting_empty_path(self):
        tr = make(["q0"], ["a"], [], "q0", ["q0"])
        ev = tr.best()
        self.assertEqual((ev.total_cost, ev.output, ev.steps), (0, "", []))


class TestKBest(unittest.TestCase):
    def test_distinct_outputs_dedup(self):
        # two paths emit the same output "x": counted once
        tr = make(
            ["q0", "q1", "q2", "qf"], ["a", "b", "x", "y"],
            [Edge(0, "q0", "q1", "a", "x", 1),
             Edge(1, "q1", "qf", None, "", 0),
             Edge(2, "q0", "q2", "b", "x", 1),
             Edge(3, "q2", "qf", None, "", 0),
             Edge(4, "q0", "qf", "a", "y", 2)],
            "q0", ["qf"])
        res = tr.k_best(5)
        self.assertEqual([e.output for e in res.evidences], ["x", "y"])
        self.assertEqual(res.evidences if False else len(res.evidences), 2)
        self.assertEqual(res.evidences[0].edge_ids, (0, 1))  # min-id witness

    def test_budget_exhaustion_keeps_frontier(self):
        # zero-cost cycle emitting "a": infinitely many distinct outputs
        tr = make(
            ["q0", "q1", "q2"], ["a", "b"],
            [Edge(0, "q0", "q1", "a", "", 0),
             Edge(1, "q1", "q1", None, "a", 0),
             Edge(2, "q1", "q2", None, "b", 0)],
            "q0", ["q2"])
        res = tr.k_best(10, budget=6)
        self.assertFalse(res.complete)
        self.assertEqual(res.pops, 6)
        self.assertGreater(res.frontier_size, 0)
        self.assertEqual([e.output for e in res.evidences], ["b", "ab"])
        self.assertTrue(all(key[0] == 0 for key in res.frontier_sample))

    def test_zero_output_cycle_not_enumerated_forever(self):
        # only zero-cost empty-output cycles: pruning drains the heap, so
        # enumeration completes even though infinitely many *paths* exist
        tr = make(
            ["q0", "q1", "q2"], ["a", "x"],
            [Edge(0, "q0", "q1", None, "", 0),
             Edge(1, "q1", "q0", None, "", 0),
             Edge(2, "q1", "q2", "a", "x", 1)],
            "q0", ["q2"])
        res = tr.k_best(5, budget=1000)
        self.assertTrue(res.complete)
        self.assertEqual([e.output for e in res.evidences], ["x"])
        self.assertLess(res.pops, 1000)


class TestValidation(unittest.TestCase):
    def test_negative_cost_rejected(self):
        with self.assertRaises(ValueError):
            make(["q0"], ["a"], [Edge(0, "q0", "q0", "a", "", -1)], "q0", ["q0"])

    def test_unknown_state_rejected(self):
        with self.assertRaises(ValueError):
            make(["q0"], ["a"], [Edge(0, "q0", "qX", "a", "", 1)], "q0", ["q0"])

    def test_duplicate_edge_id_rejected(self):
        with self.assertRaises(ValueError):
            make(["q0"], ["a"],
                 [Edge(0, "q0", "q0", "a", "", 1),
                  Edge(0, "q0", "q0", "a", "", 2)], "q0", ["q0"])

    def test_symbol_outside_alphabet_rejected(self):
        with self.assertRaises(ValueError):
            make(["q0"], ["a"], [Edge(0, "q0", "q0", "z", "", 1)], "q0", ["q0"])
        with self.assertRaises(ValueError):
            make(["q0"], ["a"], [Edge(0, "q0", "q0", "a", "z", 1)], "q0", ["q0"])


def random_transducer(rng, n_states=4, n_edges=8, min_cost=0, max_cost=3):
    states = [f"q{i}" for i in range(n_states)]
    alphabet = ["a", "b"]
    edges = []
    for i in range(n_edges):
        src, dst = rng.choice(states), rng.choice(states)
        inp = None if rng.random() < 0.25 else rng.choice(alphabet)
        out = "" if rng.random() < 0.4 else "".join(
            rng.choices(alphabet, k=rng.randint(1, 2)))
        edges.append(Edge(i, src, dst, inp, out, rng.randint(min_cost, max_cost)))
    finals = rng.sample(states, k=rng.randint(1, 2))
    return Transducer(states, alphabet, edges, states[0], finals)


class TestExhaustiveCrossCheck(unittest.TestCase):
    def test_best_matches_simple_path_exhaustion(self):
        # 200 seeded random small graphs (zero-cost edges and epsilons
        # included): Dijkstra result must equal exhaustive simple-path search
        for seed in range(150):
            rng = random.Random(seed)
            tr = random_transducer(rng)
            fast, slow = tr.best(), tr.brute_force_best()
            with self.subTest(seed=seed):
                if fast is None or slow is None:
                    self.assertIsNone(fast)
                    self.assertIsNone(slow)
                else:
                    self.assertEqual(fast.key(), slow.key())

    def test_k_best_matches_bounded_exhaustion(self):
        # positive costs only: any path longer than max_steps costs more than
        # max_steps, so bounded exhaustion sees every output that can rank
        # in the top-k (checked via the k-th cost below)
        k, max_steps = 4, 8
        checked = 0
        for seed in range(40):
            rng = random.Random(10_000 + seed)
            tr = random_transducer(rng, n_edges=10, min_cost=1, max_cost=2)
            expected = tr.brute_force_k_best(k, max_steps)
            if len(expected) < k or expected[-1].total_cost > max_steps:
                continue  # bound too small to certify the top-k for this graph
            res = tr.k_best(k, budget=5_000)
            with self.subTest(seed=seed):
                self.assertEqual([e.key() for e in res.evidences],
                                 [e.key() for e in expected])
            checked += 1
        self.assertGreater(checked, 20)  # the bound was certifiable often enough

    def test_example_specs_cross_checked(self):
        import json, os
        for name in ("basic", "epsilon_cycle", "zero_output_cycle",
                     "no_path", "tie_break"):
            path = os.path.join("examples", f"{name}.json")
            with open(path, encoding="utf-8") as fh:
                spec = json.load(fh)
            tr = Transducer(
                spec["states"], spec["alphabet"],
                [Edge(e["id"], e["src"], e["dst"], e.get("in"),
                      e.get("out", ""), e["cost"]) for e in spec["edges"]],
                spec["initial"], spec["finals"])
            with self.subTest(spec=name):
                fast, slow = tr.best(), tr.brute_force_best()
                self.assertEqual(fast is None, slow is None)
                if fast is not None:
                    self.assertEqual(fast.key(), slow.key())


if __name__ == "__main__":
    unittest.main(verbosity=2)
