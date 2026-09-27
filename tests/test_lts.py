import unittest

from simcheck.lts import LTS, TAU
from simcheck.relations import divergent_states, tau_closure


class TestLTSModel(unittest.TestCase):
    def test_states_include_targets_and_sources(self):
        lts = LTS("m", "a", {"a": [["x", "b"]]})
        self.assertEqual(lts.states(), {"a", "b"})

    def test_initial_state_must_exist(self):
        with self.assertRaises(ValueError):
            LTS("m", "ghost", {"a": [["x", "b"]]})

    def test_duplicate_edges_removed_order_kept(self):
        lts = LTS("m", "a", {"a": [["x", "b"], ["x", "b"], ["y", "c"]]})
        self.assertEqual(lts.successors("a"), [("x", "b"), ("y", "c")])

    def test_deadlock_state(self):
        lts = LTS("m", "a", {"a": []})
        self.assertTrue(lts.is_deadlock("a"))

    def test_bad_shape_rejected(self):
        with self.assertRaises(ValueError):
            LTS("m", "a", {"a": [["x"]]})  # not a pair


class TestTauClosure(unittest.TestCase):
    def test_closure_includes_seed(self):
        lts = LTS("m", "a", {"a": []})
        self.assertEqual(tau_closure(lts, {"a"}), {"a"})

    def test_tau_loop_terminates(self):
        lts = LTS(
            "m",
            "a",
            {
                "a": [[TAU, "b"]],
                "b": [[TAU, "a"], [TAU, "c"]],
                "c": [],
            },
        )
        self.assertEqual(tau_closure(lts, {"a"}), {"a", "b", "c"})

    def test_visible_edges_not_in_closure(self):
        lts = LTS("m", "a", {"a": [["x", "b"]], "b": []})
        self.assertEqual(tau_closure(lts, {"a"}), {"a"})


class TestDivergence(unittest.TestCase):
    def test_tau_self_loop_is_divergent(self):
        lts = LTS("m", "a", {"a": [[TAU, "a"]]})
        self.assertEqual(divergent_states(lts), {"a"})

    def test_tau_cycle_and_its_ancestors(self):
        lts = LTS(
            "m",
            "a",
            {
                "a": [[TAU, "b"]],
                "b": [[TAU, "c"]],
                "c": [[TAU, "b"]],
            },
        )
        self.assertEqual(divergent_states(lts), {"a", "b", "c"})

    def test_acyclic_tau_is_not_divergent(self):
        lts = LTS("m", "a", {"a": [[TAU, "b"]], "b": []})
        self.assertEqual(divergent_states(lts), set())

    def test_long_tau_chain_no_recursion_error(self):
        n = 5000
        transitions = {f"s{i}": [[TAU, f"s{i+1}"]] for i in range(n - 1)}
        transitions[f"s{n-1}"] = [[TAU, "s0"]]
        lts = LTS("m", "s0", transitions)
        self.assertEqual(len(divergent_states(lts)), n)


if __name__ == "__main__":
    unittest.main()
