import random
import unittest

from simcheck.enumerate import OK, UNDECIDED, enumerate_greatest_simulation
from simcheck.fixpoint import FAIL, PASS, greatest_simulation
from simcheck.lts import LTS, TAU


def random_lts(rng, name, n_states, labels=("a", "b"), edge_prob=0.35, tau_prob=0.3):
    states = [f"{name}{i}" for i in range(n_states)]
    transitions = {}
    for src in states:
        edges = []
        for dst in states:
            if rng.random() < edge_prob:
                label = TAU if rng.random() < tau_prob else rng.choice(labels)
                edges.append([label, dst])
        transitions[src] = edges
    return LTS(name, states[0], transitions)


class TestEnumerationOracle(unittest.TestCase):
    """Independent check: the iterative greatest fixpoint must equal the union
    of all post-fixed relations found by exhaustive enumeration."""

    def cross_check(self, impl, spec):
        for mode in ("weak", "branching"):
            for div in (False, True):
                enum = enumerate_greatest_simulation(
                    impl, spec, mode=mode, divergence_sensitive=div
                )
                self.assertEqual(enum.status, OK)
                result = greatest_simulation(
                    impl, spec, mode=mode, divergence_sensitive=div
                )
                expected = PASS if enum.initial_simulated else FAIL
                self.assertEqual(result.status, expected, (mode, div))
                self.assertEqual(result.relation, enum.greatest, (mode, div))

    def test_example_models(self):
        pairs = [
            ("examples/impl_machine.json", "examples/spec_machine.json"),
            ("examples/impl_deadlock.json", "examples/spec_progress.json"),
            ("examples/impl_divergent.json", "examples/spec_simple.json"),
            ("examples/impl_stutter.json", "examples/spec_choice.json"),
        ]
        for impl_path, spec_path in pairs:
            self.cross_check(LTS.load(impl_path), LTS.load(spec_path))

    def test_random_small_models(self):
        rng = random.Random(20260928)
        for _ in range(40):
            impl = random_lts(rng, "i", rng.randint(1, 3))
            spec = random_lts(rng, "s", rng.randint(1, 3))
            self.cross_check(impl, spec)

    def test_tau_heavy_random_models(self):
        rng = random.Random(7)
        for _ in range(20):
            impl = random_lts(rng, "i", 3, edge_prob=0.5, tau_prob=0.6)
            spec = random_lts(rng, "s", 3, edge_prob=0.5, tau_prob=0.6)
            self.cross_check(impl, spec)


class TestEnumerationBudget(unittest.TestCase):
    def test_enumeration_budget_exhausted_is_undecided(self):
        impl = LTS("i", "q0", {"q0": [["a", "q1"]], "q1": []})
        spec = LTS("s", "p0", {"p0": [["a", "p1"]], "p1": []})
        # 4 state pairs -> 16 candidate relations; allow only 2
        result = enumerate_greatest_simulation(impl, spec, max_relations=2)
        self.assertEqual(result.status, UNDECIDED)
        self.assertIsNone(result.initial_simulated)


if __name__ == "__main__":
    unittest.main()
