"""unittest suite for the transducer package.

Run:  python3 -m unittest discover -s tests -v
"""
import pathlib
import random
import sys
import unittest

sys.path.insert(0, str(pathlib.Path(__file__).resolve().parent.parent))

from transducer import (
    FST,
    Transition,
    brute_force_best,
    k_best_outputs,
    min_cost_transduction,
)


def replay(fst, text, steps):
    """Independently replay evidence: returns (state, pos, cost, output)."""
    pos = 0
    state = fst.start
    cost = 0
    out = []
    for st in steps:
        tr = fst.edge(st.edge_id)
        assert tr.src == state == st.src, "state chain broken"
        assert tr.dst == st.dst
        assert st.pos_before == pos, "input position chain broken"
        if tr.input_symbol is None:
            assert st.consumed == "", "epsilon edge must not consume"
        else:
            assert pos < len(text) and text[pos] == tr.input_symbol == st.consumed
            pos += 1
        assert st.pos_after == pos
        assert st.produced == tr.output and st.cost == tr.cost
        out.append(st.produced)
        cost += st.cost
        state = tr.dst
    return state, pos, cost, "".join(out)


class ValidationTests(unittest.TestCase):
    def test_negative_cost_rejected(self):
        with self.assertRaises(ValueError):
            FST({"q"}, {"a"}, [Transition(1, "q", "q", "a", "x", -1)], "q", {"q"})

    def test_duplicate_edge_id_rejected(self):
        edges = [Transition(1, "q", "q", "a", "x", 0),
                 Transition(1, "q", "q", "a", "y", 0)]
        with self.assertRaises(ValueError):
            FST({"q"}, {"a"}, edges, "q", {"q"})

    def test_unknown_state_rejected(self):
        with self.assertRaises(ValueError):
            FST({"q"}, {"a"}, [Transition(1, "q", "zzz", "a", "x", 0)], "q", {"q"})

    def test_symbol_outside_alphabet_rejected(self):
        with self.assertRaises(ValueError):
            FST({"q"}, {"a"}, [Transition(1, "q", "q", "b", "x", 0)], "q", {"q"})

    def test_input_text_outside_alphabet_rejected(self):
        fst = FST({"q"}, {"a"}, [Transition(1, "q", "q", "a", "x", 0)], "q", {"q"})
        with self.assertRaises(ValueError):
            min_cost_transduction(fst, "ab")


class MinCostTests(unittest.TestCase):
    def test_cheapest_path_wins(self):
        fst = FST({"q0", "q1"}, {"a"}, [
            Transition(1, "q0", "q1", "a", "x", 5),
            Transition(2, "q0", "q1", "a", "y", 2),
        ], "q0", {"q1"})
        res = min_cost_transduction(fst, "a")
        self.assertTrue(res.ok)
        self.assertEqual((res.cost, res.output), (2, "y"))

    def test_tie_on_cost_shorter_output_wins(self):
        fst = FST({"q0", "q1"}, {"a"}, [
            Transition(1, "q0", "q1", "a", "xy", 1),
            Transition(2, "q0", "q1", "a", "z", 1),
        ], "q0", {"q1"})
        res = min_cost_transduction(fst, "a")
        self.assertEqual(res.output, "z")

    def test_tie_on_cost_and_length_lexicographic_wins(self):
        fst = FST({"q0", "q1"}, {"a"}, [
            Transition(1, "q0", "q1", "a", "b", 1),
            Transition(2, "q0", "q1", "a", "a", 1),
        ], "q0", {"q1"})
        res = min_cost_transduction(fst, "a")
        self.assertEqual(res.output, "a")

    def test_length_dominates_string_comparison(self):
        # "b" (len 1) beats "aa" (len 2) even though "aa" < "b" as strings.
        fst = FST({"q0", "q1"}, {"a"}, [
            Transition(1, "q0", "q1", "a", "aa", 0),
            Transition(2, "q0", "q1", "a", "b", 0),
        ], "q0", {"q1"})
        res = min_cost_transduction(fst, "a")
        self.assertEqual(res.output, "b")

    def test_tie_on_output_edge_ids_decide(self):
        fst = FST({"q0", "q1"}, {"a"}, [
            Transition(5, "q0", "q1", "a", "x", 1),
            Transition(3, "q0", "q1", "a", "x", 1),
        ], "q0", {"q1"})
        res = min_cost_transduction(fst, "a")
        self.assertEqual([s.edge_id for s in res.steps], [3])

    def test_epsilon_edges_participate(self):
        fst = FST({"q0", "q1", "q2"}, {"a"}, [
            Transition(1, "q0", "q1", None, "pre-", 1),
            Transition(2, "q1", "q2", "a", "A", 1),
        ], "q0", {"q2"})
        res = min_cost_transduction(fst, "a")
        self.assertTrue(res.ok)
        self.assertEqual(res.output, "pre-A")
        state, pos, cost, out = replay(fst, "a", res.steps)
        self.assertEqual((state, pos, cost, out), ("q2", 1, 2, "pre-A"))

    def test_zero_cost_silent_cycle_normalised(self):
        fst = FST({"q0", "q1", "q2"}, {"a"}, [
            Transition(1, "q0", "q1", None, "", 0),
            Transition(2, "q1", "q0", None, "", 0),
            Transition(3, "q0", "q2", None, "", 0),
        ], "q0", {"q2"})
        res = min_cost_transduction(fst, "")
        self.assertTrue(res.ok)
        self.assertEqual((res.cost, res.output), (0, ""))
        self.assertEqual([s.edge_id for s in res.steps], [3])

    def test_zero_cost_output_cycle_minimum_exists(self):
        fst = FST({"q0"}, {"a"}, [
            Transition(1, "q0", "q0", None, "a", 0),
        ], "q0", {"q0"})
        res = min_cost_transduction(fst, "")
        self.assertTrue(res.ok)
        self.assertEqual((res.cost, res.output), (0, ""))

    def test_positive_cost_silent_cycle_ignored(self):
        fst = FST({"q0", "q1"}, {"a"}, [
            Transition(1, "q0", "q0", None, "", 2),
            Transition(2, "q0", "q1", "a", "a", 1),
        ], "q0", {"q1"})
        res = min_cost_transduction(fst, "a")
        self.assertEqual((res.cost, [s.edge_id for s in res.steps]), (1, [2]))

    def test_empty_input(self):
        fst = FST({"q0", "q1"}, {"a"}, [
            Transition(1, "q0", "q1", None, "hi", 0),
        ], "q0", {"q1"})
        res = min_cost_transduction(fst, "")
        self.assertTrue(res.ok)
        self.assertEqual(res.output, "hi")

    def test_no_accepting_path_fails(self):
        fst = FST({"q0", "q1"}, {"a"}, [
            Transition(1, "q0", "q1", "a", "a", 1),
        ], "q0", {"q1"})
        res = min_cost_transduction(fst, "aa")
        self.assertFalse(res.ok)
        self.assertIn("no accepting path", res.reason)

    def test_evidence_replays_exactly(self):
        fst = FST({"q0"}, {"a", "b"}, [
            Transition(1, "q0", "q0", "a", "4", 1),
            Transition(2, "q0", "q0", "a", "a", 2),
            Transition(3, "q0", "q0", "b", "8", 1),
            Transition(4, "q0", "q0", None, "!", 5),
        ], "q0", {"q0"})
        res = min_cost_transduction(fst, "ab")
        state, pos, cost, out = replay(fst, "ab", res.steps)
        self.assertEqual((state, pos), ("q0", 2))
        self.assertEqual((cost, out), (res.cost, res.output))


class KBestTests(unittest.TestCase):
    def test_duplicate_outputs_counted_once(self):
        fst = FST({"q0", "q1", "q2", "q3"}, {"a"}, [
            Transition(1, "q0", "q1", "a", "x", 1),
            Transition(2, "q0", "q2", "a", "x", 1),
            Transition(3, "q1", "q3", None, "", 0),
            Transition(4, "q2", "q3", None, "", 0),
        ], "q0", {"q3"})
        kb = k_best_outputs(fst, "a", k=2)
        self.assertEqual(kb.distinct_outputs, 1)
        self.assertTrue(kb.complete)
        self.assertEqual(kb.results[0].output, "x")
        self.assertEqual([s.edge_id for s in kb.results[0].steps], [1, 3])

    def test_results_in_key_order(self):
        fst = FST({"q0"}, {"a", "b"}, [
            Transition(1, "q0", "q0", "a", "4", 1),
            Transition(2, "q0", "q0", "a", "a", 2),
            Transition(3, "q0", "q0", "b", "8", 1),
            Transition(4, "q0", "q0", "b", "b", 3),
            Transition(5, "q0", "q0", None, "!", 5),
        ], "q0", {"q0"})
        kb = k_best_outputs(fst, "ab", k=6)
        keys = [r.key for r in kb.results]
        self.assertEqual(keys, sorted(keys))
        self.assertEqual(
            [r.output for r in kb.results],
            ["48", "a8", "4b", "ab", "!48", "4!8"],
        )

    def test_k_larger_than_distinct_outputs(self):
        fst = FST({"q0", "q1"}, {"a"}, [
            Transition(1, "q0", "q1", "a", "x", 1),
        ], "q0", {"q1"})
        kb = k_best_outputs(fst, "a", k=10)
        self.assertTrue(kb.complete)
        self.assertFalse(kb.budget_exhausted)
        self.assertEqual(kb.distinct_outputs, 1)

    def test_budget_exhaustion_keeps_frontier(self):
        fst = FST({"q0"}, {"a"}, [
            Transition(1, "q0", "q0", None, "a", 0),
        ], "q0", {"q0"})
        kb = k_best_outputs(fst, "", k=10, budget=5)
        self.assertTrue(kb.budget_exhausted)
        self.assertFalse(kb.complete)
        self.assertEqual(kb.distinct_outputs, 5)
        self.assertTrue(kb.frontier, "frontier must be preserved")
        self.assertEqual(kb.frontier[0][:3], (0, 5, "aaaaa"))

    def test_infinite_distinct_outputs_reaches_k(self):
        fst = FST({"q0"}, {"a"}, [
            Transition(1, "q0", "q0", None, "a", 0),
        ], "q0", {"q0"})
        kb = k_best_outputs(fst, "", k=4)
        self.assertTrue(kb.complete)
        self.assertEqual([r.output for r in kb.results], ["", "a", "aa", "aaa"])

    def test_k_must_be_positive(self):
        fst = FST({"q0"}, {"a"}, [], "q0", {"q0"})
        with self.assertRaises(ValueError):
            k_best_outputs(fst, "", k=0)


class CycleDiagnosticTests(unittest.TestCase):
    def test_finds_zero_cost_silent_cycle(self):
        fst = FST({"q0", "q1", "q2"}, {"a"}, [
            Transition(1, "q0", "q1", None, "", 0),
            Transition(2, "q1", "q0", None, "", 0),
            Transition(3, "q0", "q2", None, "", 0),
        ], "q0", {"q2"})
        cyc = fst.find_zero_cost_silent_cycle()
        self.assertIsNotNone(cyc)
        edges = [fst.edge(i) for i in cyc]
        for a, b in zip(edges, edges[1:] + edges[:1]):
            self.assertEqual(a.dst, b.src, "must form a closed walk")
        self.assertTrue(all(e.is_silent and e.cost == 0 for e in edges))

    def test_no_cycle_returns_none(self):
        fst = FST({"q0", "q1"}, {"a"}, [
            Transition(1, "q0", "q1", None, "", 0),
            Transition(2, "q1", "q1", None, "", 1),  # silent but costs 1
        ], "q0", {"q1"})
        self.assertIsNone(fst.find_zero_cost_silent_cycle())


def random_fst(rng):
    """Small random FST with an acyclic epsilon subgraph (so brute force
    with the bound n + (n+1)*|Q| is complete)."""
    nstates = rng.randint(1, 4)
    states = [f"q{i}" for i in range(nstates)]
    accepts = {s for s in states if rng.random() < 0.5}
    if rng.random() < 0.8:
        accepts.add(rng.choice(states))
    edges = []
    eid = 1
    for i, s in enumerate(states):
        for _ in range(rng.randint(1, 3)):
            if rng.random() < 0.5 or i == nstates - 1:
                sym = rng.choice("ab")
                dst = rng.choice(states)
            else:  # epsilon edge, only to a later state: keeps eps acyclic
                sym = None
                dst = states[rng.randint(i + 1, nstates - 1)]
            edges.append(Transition(
                eid, s, dst, sym, rng.choice(["", "x", "y", "xy"]),
                rng.randint(0, 3),
            ))
            eid += 1
    return FST(states, {"a", "b"}, edges, "q0", accepts)


class BruteForceCrossCheckTests(unittest.TestCase):
    def test_random_small_machines_against_oracle(self):
        for trial in range(200):
            rng = random.Random(trial)
            fst = random_fst(rng)
            text = "".join(rng.choice("ab") for _ in range(rng.randint(0, 3)))
            bound = len(text) + (len(text) + 1) * len(fst.states) + 1
            oracle = brute_force_best(fst, text, max_steps=bound)
            with self.subTest(trial=trial, text=text):
                res = min_cost_transduction(fst, text)
                if not oracle:
                    self.assertFalse(res.ok)
                    continue
                self.assertTrue(res.ok)
                best_key, best_out, _ = oracle[0]
                self.assertEqual(res.cost, best_key[0])
                self.assertEqual(res.output, best_out)
                self.assertEqual(
                    tuple(s.edge_id for s in res.steps), best_key[3]
                )
                state, pos, cost, out = replay(fst, text, res.steps)
                self.assertIn(state, fst.accepts)
                self.assertEqual(pos, len(text))
                self.assertEqual((cost, out), (res.cost, res.output))

                top = min(8, len(oracle))
                kb = k_best_outputs(fst, text, k=top)
                self.assertTrue(kb.complete)
                self.assertEqual(
                    [r.key for r in kb.results],
                    [key for key, _out, _steps in oracle[:top]],
                )
                self.assertEqual(
                    [r.output for r in kb.results],
                    [out for _key, out, _steps in oracle[:top]],
                )


if __name__ == "__main__":
    unittest.main()
