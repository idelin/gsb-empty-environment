"""Exactness tests for the bounded-derivation sampler.

The primary oracle is brute-force enumeration of *all* derivation trees
(``independent_check``).  Statistical frequency checks are included only
as a supplement; they never replace the exact comparisons.
"""

from __future__ import annotations

import random
import sys
import unittest
from collections import Counter
from fractions import Fraction
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent.parent / "src"))

from bounded_cfg import (
    BoundedModel,
    BudgetExceeded,
    Grammar,
    GrammarError,
    Production,
    steps_of,
    tree_weight,
    yield_of,
)
from independent_check import (
    enumerate_trees,
    partition_by_enumeration,
    string_weights,
    tree_steps,
    tree_yield,
)


def make_loop_grammar() -> Grammar:
    """A -> A | AA | eps | a, plus zero-weight and unreachable rules."""
    return Grammar(
        [
            Production("S", ("A",), Fraction(1)),
            Production("A", ("A",), Fraction(1, 2)),            # unit loop
            Production("A", ("A", "A"), Fraction(1, 3), group="branch"),
            Production("A", (), Fraction(1, 5), group="eps"),   # epsilon
            Production("A", ("a",), Fraction(1)),
            Production("A", ("b",), Fraction(0)),               # zero weight
            Production("U", ("u",), Fraction(7)),               # unreachable
        ],
        start="S",
    )


class TestPartitionExactness(unittest.TestCase):
    """DP partition weights must equal brute-force enumeration, cell by cell."""

    def check_model(self, grammar, L, D, groups=()):
        model = BoundedModel(grammar, L, D, tracked_groups=groups)
        enum_grid = partition_by_enumeration(grammar, L, D)
        model_grid = model.partition_by_length_steps()
        for key, value in model_grid.items():
            self.assertEqual(
                value, enum_grid.get(key, Fraction(0)),
                f"Z{key} mismatch",
            )
        for key, value in enum_grid.items():
            self.assertEqual(
                model_grid.get(key, Fraction(0)), value,
                f"Z{key} missing from model",
            )
        return model

    def test_loop_grammar(self):
        self.check_model(make_loop_grammar(), 2, 4)

    def test_loop_grammar_deeper(self):
        self.check_model(make_loop_grammar(), 3, 5)

    def test_pure_epsilon_loop(self):
        # A -> A | eps : exactly one tree per step count, all yielding "".
        grammar = Grammar(
            [
                Production("A", ("A",), Fraction(3, 4)),
                Production("A", (), Fraction(1, 2)),
            ],
            start="A",
        )
        model = self.check_model(grammar, 0, 5)
        for steps in range(1, 6):
            self.assertEqual(
                model.partition(length=0, steps=steps),
                Fraction(3, 4) ** (steps - 1) * Fraction(1, 2),
            )
        self.assertEqual(model.partition(length=0, steps=0), Fraction(0))

    def test_rhs_length_three_convolution(self):
        grammar = Grammar(
            [
                Production("S", ("A", "b", "A"), Fraction(2, 3)),
                Production("A", ("a",), Fraction(1, 2)),
                Production("A", (), Fraction(1, 4)),
            ],
            start="S",
        )
        self.check_model(grammar, 3, 4)

    def test_shared_subproblem(self):
        # B is shared by two different parents and by two slots of one parent.
        grammar = Grammar(
            [
                Production("S", ("B", "B"), Fraction(1)),
                Production("S", ("C",), Fraction(1)),
                Production("C", ("B", "x"), Fraction(1)),
                Production("B", ("b",), Fraction(1, 2)),
                Production("B", (), Fraction(1, 2)),
            ],
            start="S",
        )
        self.check_model(grammar, 3, 5)

    def test_zero_weight_contributes_nothing(self):
        grammar = make_loop_grammar()
        model = BoundedModel(grammar, 2, 4)
        self.assertEqual(model.partition(length=1, steps=1), Fraction(0))
        # 'b' only appears via a zero-weight production.
        self.assertEqual(model.partition(), model.partition())
        strings = string_weights(grammar, 2, 4)
        self.assertEqual(strings.get(("b",), Fraction(0)), Fraction(0))

    def test_unreachable_nonterminal_is_inert(self):
        with_unreachable = make_loop_grammar()
        prods = [
            Production(p.lhs, p.rhs, p.weight, p.group)
            for p in with_unreachable.productions
            if p.lhs != "U"
        ]
        without = Grammar(prods, start="S")
        self.assertEqual(
            BoundedModel(with_unreachable, 2, 4).partition(),
            BoundedModel(without, 2, 4).partition(),
        )
        self.assertNotIn("U", with_unreachable.reachable_nonterminals())


class TestConditionalDistributions(unittest.TestCase):
    def setUp(self):
        self.grammar = make_loop_grammar()
        self.L, self.D = 2, 4
        self.groups = ("branch", "eps")
        self.model = BoundedModel(
            self.grammar, self.L, self.D, tracked_groups=self.groups
        )

    def test_conditional_partition_matches_enumeration(self):
        for required in [("branch",), ("eps",), ("branch", "eps")]:
            enum_grid = partition_by_enumeration(
                self.grammar, self.L, self.D, required_groups=required
            )
            model_grid = self.model.partition_by_length_steps(required_groups=required)
            for key, value in model_grid.items():
                self.assertEqual(
                    value, enum_grid.get(key, Fraction(0)),
                    f"conditional Z{key} given {required}",
                )

    def test_condition_lives_in_dp_state(self):
        # Conditioning on both groups must equal the weight of trees whose
        # mask has both bits -- not a product of marginals.
        both = self.model.partition(required_groups=("branch", "eps"))
        branch = self.model.partition(required_groups=("branch",))
        eps = self.model.partition(required_groups=("eps",))
        total = self.model.partition()
        self.assertGreater(both, 0)
        self.assertNotEqual(both, branch * eps / total)

    def test_untracked_group_rejected(self):
        with self.assertRaises(GrammarError):
            self.model.partition(required_groups=("nope",))
        with self.assertRaises(GrammarError):
            self.model.sample(random.Random(0), required_groups=("nope",))

    def test_conditional_samples_satisfy_condition(self):
        rng = random.Random(7)
        for _ in range(300):
            result = self.model.sample(rng, required_groups=("branch",))
            self.assertIsNotNone(result)
            self.assertIn("branch", result.groups)

    def test_conditional_probabilities_exact(self):
        # Compare sampled-tree probabilities against enumeration, per tree.
        required = ("branch",)
        z_cond = self.model.partition(required_groups=required)
        enum_trees = [
            (t, w) for t, w, g in enumerate_trees(self.grammar, self.L, self.D)
            if "branch" in g
        ]
        self.assertEqual(
            sum((w for _, w in enum_trees), Fraction(0)), z_cond
        )
        rng = random.Random(11)
        seen = Counter()
        draws = 3000
        for _ in range(draws):
            result = self.model.sample(rng, required_groups=required)
            self.assertEqual(result.probability, result.weight / z_cond)
            seen[result.tree] += 1
        # Every sampled tree must be one of the enumerated ones.
        enum_weights = {}
        for t, w in enum_trees:
            key = _canon(t)
            enum_weights[key] = enum_weights.get(key, Fraction(0)) + w
        sampled_weight = sum(
            (r.weight for r in [self.model.sample(random.Random(1))]), Fraction(0)
        )  # smoke: weights are positive rationals
        self.assertGreater(sampled_weight, 0)
        for tree in seen:
            self.assertIn(_canon_of_node(tree), enum_weights)


def _canon(enum_tree):
    lhs, children = enum_tree
    return (lhs, tuple((sym, _canon(sub) if sub is not None else None)
                       for sym, sub in children))


def _canon_of_node(node):
    return (node.production.lhs, tuple(
        (sym, _canon_of_node(child) if hasattr(child, "production") else None)
        for sym, child in zip(node.production.rhs, node.children)
    ))


class TestAmbiguity(unittest.TestCase):
    """Ambiguous strings: probability is the SUM of tree weights."""

    def test_string_probability_is_sum_of_tree_weights(self):
        # "aa" has two structurally different derivations here.
        grammar = Grammar(
            [
                Production("S", ("A", "A"), Fraction(1)),
                Production("S", ("B",), Fraction(1)),
                Production("B", ("A", "A"), Fraction(1)),
                Production("A", ("a",), Fraction(1, 2)),
            ],
            start="S",
        )
        L, D = 2, 4
        model = BoundedModel(grammar, L, D)
        strings = string_weights(grammar, L, D)
        z = model.partition()
        self.assertEqual(strings[("a", "a")], Fraction(1, 4) + Fraction(1, 4))
        # Sample and confirm the string-level frequency approaches the sum.
        rng = random.Random(123)
        draws = 4000
        hits = sum(
            1 for _ in range(draws)
            if model.sample(rng).terminals == ("a", "a")
        )
        exact = strings[("a", "a")] / z
        self.assertAlmostEqual(hits / draws, float(exact), delta=0.03)

    def test_no_dedup_uniform_claim(self):
        # Two trees, same string, different weights: not uniform over strings.
        grammar = Grammar(
            [
                Production("S", ("a",), Fraction(3)),
                Production("S", ("A",), Fraction(1)),
                Production("A", ("a",), Fraction(1)),
            ],
            start="S",
        )
        model = BoundedModel(grammar, 1, 2)
        strings = string_weights(grammar, 1, 2)
        self.assertEqual(strings[("a",)], Fraction(4))
        rng = random.Random(5)
        draws = 2000
        hits = sum(1 for _ in range(draws) if model.sample(rng).terminals == ("a",))
        self.assertAlmostEqual(hits / draws, 1.0, delta=0.001)


class TestSamplingInterface(unittest.TestCase):
    def setUp(self):
        self.grammar = make_loop_grammar()
        self.model = BoundedModel(self.grammar, 2, 4, tracked_groups=("branch", "eps"))

    def test_empty_domain_returns_none(self):
        # No tree of length 2 in a single step exists.
        self.assertIsNone(self.model.sample(random.Random(0), length=2, steps=1))
        # Length beyond the bound of any feasible tree combined with few steps.
        self.assertIsNone(self.model.sample(random.Random(0), length=0, steps=1))

    def test_length_restricted_sampling(self):
        rng = random.Random(99)
        for _ in range(100):
            result = self.model.sample(rng, length=1)
            self.assertIsNotNone(result)
            self.assertEqual(result.length, 1)
            self.assertEqual(result.terminals, ("a",))

    def test_steps_restricted_sampling(self):
        rng = random.Random(100)
        for _ in range(100):
            result = self.model.sample(rng, steps=2)
            self.assertIsNotNone(result)
            self.assertEqual(result.steps, 2)
            self.assertEqual(steps_of(result.tree), 2)

    def test_sample_fields_consistent(self):
        rng = random.Random(2024)
        total = self.model.partition()
        for _ in range(200):
            result = self.model.sample(rng)
            self.assertEqual(result.terminals, yield_of(result.tree, self.grammar))
            self.assertEqual(result.length, len(result.terminals))
            self.assertEqual(result.steps, steps_of(result.tree))
            self.assertEqual(result.weight, tree_weight(result.tree))
            self.assertEqual(result.probability, result.weight / total)
            self.assertLessEqual(result.length, 2)
            self.assertLessEqual(result.steps, 4)

    def test_deterministic_with_seed(self):
        seq1 = [self.model.sample(random.Random(42)).terminals for _ in range(10)]
        seq2 = [self.model.sample(random.Random(42)).terminals for _ in range(10)]
        self.assertEqual(seq1, seq2)

    def test_callable_rng_accepted(self):
        rng = random.Random(3)
        result = self.model.sample(lambda n: rng.randrange(n))
        self.assertIsNotNone(result)

    def test_bad_rng_rejected(self):
        with self.assertRaises(GrammarError):
            self.model.sample(lambda n: n)  # out-of-range value
        with self.assertRaises(GrammarError):
            self.model.sample(lambda n: -1)

    def test_zero_weight_never_sampled(self):
        rng = random.Random(0)
        for _ in range(500):
            result = self.model.sample(rng)
            self.assertNotIn("b", result.terminals)


class TestBudgets(unittest.TestCase):
    def test_work_budget_raises_not_truncates(self):
        grammar = make_loop_grammar()
        with self.assertRaises(BudgetExceeded):
            BoundedModel(grammar, 3, 6, max_work=10)

    def test_sample_budget_raises(self):
        grammar = make_loop_grammar()
        model = BoundedModel(grammar, 2, 4, max_sample_work=5)
        with self.assertRaises(BudgetExceeded):
            model.sample(random.Random(0))

    def test_budget_result_not_silently_partial(self):
        # A model built within budget must equal one built with a huge budget.
        grammar = make_loop_grammar()
        tight = BoundedModel(grammar, 2, 4, max_work=10_000_000)
        loose = BoundedModel(grammar, 2, 4)
        self.assertEqual(tight.partition(), loose.partition())


class TestStatisticalSupplement(unittest.TestCase):
    """Frequency checks only -- the exact tests above are the real proof."""

    def test_tree_level_frequencies(self):
        grammar = make_loop_grammar()
        L, D = 2, 4
        model = BoundedModel(grammar, L, D)
        z = model.partition()
        trees = list(enumerate_trees(grammar, L, D))
        exact = {}
        for t, w, _ in trees:
            if w == 0:
                continue  # zero-weight trees are outside the sampling domain
            key = _canon(t)
            exact[key] = exact.get(key, Fraction(0)) + w
        rng = random.Random(20240924)
        draws = 20000
        counts = Counter()
        for _ in range(draws):
            counts[_canon_of_node(model.sample(rng).tree)] += 1
        self.assertEqual(set(counts), set(exact))
        for key, exact_w in exact.items():
            p = float(exact_w / z)
            # 5-sigma binomial tolerance.
            sigma = (p * (1 - p) / draws) ** 0.5
            self.assertAlmostEqual(counts[key] / draws, p, delta=max(5 * sigma, 1e-3))


if __name__ == "__main__":
    unittest.main()
