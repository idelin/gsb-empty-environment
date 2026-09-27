import unittest

from simcheck.fixpoint import FAIL, PASS, UNDECIDED, Budget, greatest_simulation
from simcheck.lts import LTS, TAU
from simcheck.relations import weak_attacks


def load(pair):
    return LTS.load(f"examples/impl_{pair[0]}.json"), LTS.load(f"examples/spec_{pair[1]}.json")


class TestBasicSimulation(unittest.TestCase):
    def test_internal_impl_tau_absorbed_weak_and_branching(self):
        impl, spec = load(("machine", "machine"))
        for mode in ("weak", "branching"):
            result = greatest_simulation(impl, spec, mode=mode)
            self.assertEqual(result.status, PASS, mode)

    def test_deadlock_difference_is_not_ignored(self):
        impl, spec = load(("deadlock", "progress"))
        result = greatest_simulation(impl, spec)
        self.assertEqual(result.status, FAIL)
        attacks = weak_attacks(impl, spec, result.relation | set(result.rank), "d0", "s0")
        self.assertEqual([a.kind for a in attacks], ["visible"])
        self.assertEqual(attacks[0].action, "a")

    def test_extra_impl_actions_are_allowed(self):
        # simulation is directional: the impl may do *more* than the spec
        impl = LTS("i", "q0", {"q0": [["a", "q1"], ["b", "q2"]], "q1": [], "q2": []})
        spec = LTS("s", "p0", {"p0": [["a", "p1"]], "p1": []})
        self.assertEqual(greatest_simulation(impl, spec).status, PASS)

    def test_missing_impl_action_fails(self):
        impl = LTS("i", "q0", {"q0": [["a", "q1"]], "q1": []})
        spec = LTS("s", "p0", {"p0": [["a", "p1"], ["b", "p2"]], "p1": [], "p2": []})
        self.assertEqual(greatest_simulation(impl, spec).status, FAIL)

    def test_spec_internal_move_matched_by_zero_impl_moves(self):
        impl = LTS("i", "q0", {"q0": [["a", "q1"]], "q1": []})
        spec = LTS("s", "p0", {"p0": [[TAU, "p1"]], "p1": [["a", "p2"]], "p2": []})
        for mode in ("weak", "branching"):
            self.assertEqual(greatest_simulation(impl, spec, mode=mode).status, PASS, mode)


class TestWeakVsBranching(unittest.TestCase):
    def test_stuttering_example_distinguishes_modes(self):
        impl, spec = load(("stutter", "choice"))
        self.assertEqual(greatest_simulation(impl, spec, mode="weak").status, PASS)
        self.assertEqual(greatest_simulation(impl, spec, mode="branching").status, FAIL)

    def test_branching_relation_is_subset_of_weak(self):
        impl, spec = load(("stutter", "choice"))
        weak = greatest_simulation(impl, spec, mode="weak")
        branching = greatest_simulation(impl, spec, mode="branching")
        self.assertTrue(branching.relation <= weak.relation)


class TestDivergenceSensitivity(unittest.TestCase):
    def test_tau_loop_passes_plain_weak_simulation(self):
        impl, spec = load(("divergent", "simple"))
        self.assertEqual(greatest_simulation(impl, spec).status, PASS)

    def test_tau_loop_fails_divergence_sensitive_mode(self):
        impl, spec = load(("divergent", "simple"))
        result = greatest_simulation(impl, spec, divergence_sensitive=True)
        self.assertEqual(result.status, FAIL)

    def test_shared_divergence_is_accepted(self):
        impl = LTS("i", "q0", {"q0": [[TAU, "q0"], ["a", "q1"]], "q1": []})
        spec = LTS("s", "p0", {"p0": [[TAU, "p0"], ["a", "p1"]], "p1": []})
        result = greatest_simulation(impl, spec, divergence_sensitive=True)
        self.assertEqual(result.status, PASS)


class TestBudgets(unittest.TestCase):
    def test_zero_round_budget_is_undecided(self):
        impl, spec = load(("machine", "machine"))
        result = greatest_simulation(impl, spec, budget=Budget(max_rounds=0))
        self.assertEqual(result.status, UNDECIDED)
        self.assertIn("budget", result.reason)

    def test_round_budget_short_of_fixpoint_is_undecided(self):
        impl, spec = load(("stutter", "choice"))
        full = greatest_simulation(impl, spec, mode="branching")
        self.assertEqual(full.status, FAIL)
        init_rank = full.rank[(impl.initial, spec.initial)]
        # budget stops before the round that deletes the initial pair
        result = greatest_simulation(
            impl, spec, mode="branching", budget=Budget(max_rounds=init_rank)
        )
        self.assertEqual(result.status, UNDECIDED)
        # a budget covering the deletion round still proves the failure
        result = greatest_simulation(
            impl, spec, mode="branching", budget=Budget(max_rounds=init_rank + 1)
        )
        self.assertEqual(result.status, FAIL)

    def test_pair_check_budget_mid_round_is_undecided(self):
        impl, spec = load(("machine", "machine"))
        result = greatest_simulation(impl, spec, budget=Budget(max_pair_checks=1))
        self.assertEqual(result.status, UNDECIDED)

    def test_budget_still_allows_proven_failure(self):
        # the very first pair checked is the initial pair and it is losing
        impl, spec = load(("deadlock", "progress"))
        result = greatest_simulation(impl, spec, budget=Budget(max_pair_checks=1))
        self.assertEqual(result.status, FAIL)

    def test_negative_budget_rejected(self):
        with self.assertRaises(ValueError):
            Budget(max_rounds=-1)


if __name__ == "__main__":
    unittest.main()
