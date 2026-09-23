"""Exact verification tests (enumeration vs dynamic program).

Sampling frequencies are only supplementary; distributional claims are
checked against exact rational quantities obtained two independent ways
(brute-force tree enumeration and the integer-scaled dynamic program).
"""

import random
import unittest
from dataclasses import replace
from fractions import Fraction as F

from bounded_sampler import (
    BoundedSampler,
    BudgetExhaustedError,
    Grammar,
    PartitionTable,
    Production,
    ResourceBudget,
    TreeNode,
    enumerate_trees,
    randbelow_from_random,
    string_mass,
    tree_weight,
    tree_yield,
    validate_tree,
)


def weighted_parens_grammar():
    """S -> SS (w2, group struct) | a (w3, group leaf) | epsilon (w1, leaf)."""
    prods = (
        Production("S", ("S", "S"), 2, group="struct"),
        Production("S", ("a",), 3, group="leaf"),
        Production("S", (), 1, group="leaf"),
    )
    return Grammar.create("S", prods)


class EnumerationTests(unittest.TestCase):
    def setUp(self):
        self.grammar = weighted_parens_grammar()
        self.L, self.D = 4, 7
        self.table = PartitionTable(self.grammar, self.L, self.D)
        self.enum = enumerate_trees(self.grammar, self.L, self.D)

    def test_partition_matches_enumeration(self):
        joint = {}
        struct_joint = {}
        total = F(0)
        struct_total = F(0)
        leaf_total = F(0)
        for et in self.enum:
            key = (et.tree.length, et.tree.steps)
            joint[key] = joint.get(key, F(0)) + et.weight
            total += et.weight
            if "struct" in et.tree.used_groups:
                struct_joint[key] = struct_joint.get(key, F(0)) + et.weight
                struct_total += et.weight
            if "leaf" in et.tree.used_groups:
                leaf_total += et.weight

        struct_bit = self.table.group_bit["struct"]
        leaf_bit = self.table.group_bit["leaf"]
        self.assertEqual(self.table.total_mass(), total)
        self.assertEqual(self.table.total_mass(require_mask=struct_bit), struct_total)
        self.assertEqual(self.table.total_mass(require_mask=leaf_bit), leaf_total)
        dp_joint = self.table.joint_partition()
        for key in set(joint) | set(dp_joint):
            self.assertEqual(dp_joint.get(key, F(0)), joint.get(key, F(0)), f"joint {key}")
        dp_cond = self.table.joint_partition(require_mask=struct_bit)
        for key in set(struct_joint) | set(dp_cond):
            self.assertEqual(dp_cond.get(key, F(0)), struct_joint.get(key, F(0)),
                             f"conditional joint {key}")

    def test_enumerated_tree_masks_match_dp_cells(self):
        counts = {}
        for et in self.enum:
            mask = self.table.mask_of_groups(sorted(et.tree.used_groups, key=repr))
            key = ("S", et.tree.length, et.tree.steps, mask)
            counts[key] = counts.get(key, F(0)) + et.weight
        for (nt, l, d, m), exact in counts.items():
            self.assertEqual(F(self.table.cell(nt, l, d, m), self.table.Q ** d), exact)
        for (nt, l, d, m), z in self.table.dp.items():
            if nt != "S" or z == 0:
                continue
            self.assertIn(("S", l, d, m), counts,
                          f"DP mass has no enumeration witness {(l, d, m)}")

    def test_every_sampled_tree_has_exact_probability(self):
        rng = random.Random(20260923)
        sampler = BoundedSampler(self.table, randbelow_from_random(rng))
        denom = self.table.total_mass()
        for _ in range(300):
            s = sampler.sample()
            self.assertIsNotNone(s)
            v = validate_tree(s.tree, self.grammar, self.L, self.D)
            self.assertTrue(v.ok, v.reason)
            self.assertEqual(v.length, len(s.terminals))
            self.assertEqual(v.steps, s.steps)
            self.assertEqual(v.terminals, s.terminals)
            self.assertEqual(v.length, s.tree.length)
            self.assertEqual(v.steps, s.tree.steps)
            self.assertEqual(s.probability, v.weight / denom)
            self.assertGreaterEqual(s.steps, 1)
            self.assertLessEqual(s.steps, self.D)
            self.assertLessEqual(len(s.terminals), self.L)

    def test_fixed_length_and_infeasible(self):
        rng = random.Random(11)
        sampler = BoundedSampler(self.table, randbelow_from_random(rng))
        denom = self.table.total_mass(length=3)
        self.assertGreater(denom, 0)
        for _ in range(200):
            s = sampler.sample(length=3)
            self.assertIsNotNone(s)
            self.assertEqual(len(s.terminals), 3)
            self.assertEqual(s.probability, tree_weight(s.tree) / denom)
        self.assertIsNone(sampler.sample(length=self.L + 1))
        self.assertIsNone(sampler.sample(length=2, steps=1))

    def test_conditional_distribution(self):
        rng = random.Random(7)
        mask = self.table.mask_of_groups(["struct"])
        denom = self.table.total_mass(require_mask=mask)
        self.assertGreater(denom, 0)
        sampler = BoundedSampler(self.table, randbelow_from_random(rng))
        for _ in range(200):
            s = sampler.sample(require_groups=("struct",))
            self.assertIsNotNone(s)
            v = validate_tree(s.tree, self.grammar, self.L, self.D)
            self.assertTrue(v.ok, v.reason)
            self.assertIn("struct", v.used_groups)
            self.assertEqual(s.probability, v.weight / denom)
        self.assertIsNone(sampler.sample(steps=1, require_groups=("struct",)))

    def test_reproducible_seed(self):
        def draw(seed):
            rng = random.Random(seed)
            sampler = BoundedSampler(self.table, randbelow_from_random(rng))
            out = []
            for _ in range(25):
                s = sampler.sample()
                out.append((s.terminals, s.steps, s.probability))
            return out
        self.assertEqual(draw(1234), draw(1234))
        self.assertNotEqual(draw(1), draw(2))


class DegenerateGrammarTests(unittest.TestCase):
    def test_self_loop_A_to_A(self):
        grammar = Grammar.create("A", (
            Production("A", ("A",), 1, group="loop"),
            Production("A", ("a",), 2, group="leaf"),
        ))
        table = PartitionTable(grammar, 2, 4)
        trees = enumerate_trees(grammar, 2, 4)
        total = sum((et.weight for et in trees), F(0))
        self.assertEqual(table.total_mass(), total)
        self.assertEqual(total, F(8))  # four a-trees (steps 1..4), weight 2 each
        rng = random.Random(0)
        sampler = BoundedSampler(table, randbelow_from_random(rng))
        for _ in range(30):
            s = sampler.sample()
            self.assertEqual(s.terminals, ("a",))
            self.assertEqual(s.probability, F(1, 4))
        self.assertIsNone(sampler.sample(length=0))
        self.assertIsNone(sampler.sample(length=2))
        self.assertEqual(table.total_mass(require_mask=table.group_bit["loop"]), F(6))

    def test_A_to_AA_with_epsilon(self):
        grammar = Grammar.create("A", (
            Production("A", ("A", "A"), 1),
            Production("A", (), 1),
        ))
        table = PartitionTable(grammar, 1, 5)
        joint = table.joint_partition(length=0)
        # full binary trees with exactly d nodes: d=1 -> 1, d=3 -> 1, d=5 -> 2
        self.assertEqual(joint, {(0, 1): F(1), (0, 3): F(1), (0, 5): F(2)})
        self.assertEqual(table.total_mass(length=0), F(4))
        trees = enumerate_trees(grammar, 1, 5)
        self.assertEqual(len(trees), 4)
        self.assertEqual(table.total_mass(), sum((et.weight for et in trees), F(0)))

    def test_zero_weight_productions_never_sampled(self):
        grammar = Grammar.create("S", (
            Production("S", ("a",), 0),
            Production("S", ("b",), 1),
        ))
        table = PartitionTable(grammar, 1, 1)
        self.assertEqual(table.total_mass(), F(1))
        rng = random.Random(3)
        sampler = BoundedSampler(table, randbelow_from_random(rng))
        for _ in range(50):
            s = sampler.sample()
            self.assertEqual(s.terminals, ("b",))
            self.assertEqual(s.probability, F(1))

    def test_unreachable_nonterminal_ignored(self):
        grammar = Grammar.create("S", (
            Production("S", ("a",), 1),
            Production("U", ("U",), 1),
            Production("U", ("u",), 5),
        ))
        table = PartitionTable(grammar, 2, 6)
        trees = enumerate_trees(grammar, 2, 6)
        self.assertEqual(table.total_mass(), sum((et.weight for et in trees), F(0)))
        self.assertEqual(table.total_mass(), F(1))
        rng = random.Random(1)
        sampler = BoundedSampler(table, randbelow_from_random(rng))
        for _ in range(20):
            s = sampler.sample()
            self.assertEqual(s.terminals, ("a",))
            self.assertEqual(s.probability, F(1))
        self.assertGreater(table.cell("U", 1, 1, 0), 0)

    def test_rational_weights(self):
        grammar = Grammar.create("S", (
            Production("S", ("a",), F(1, 2)),
            Production("S", ("b",), F(1, 3)),
        ))
        table = PartitionTable(grammar, 1, 1)
        trees = enumerate_trees(grammar, 1, 1)
        self.assertEqual(table.total_mass(), sum((et.weight for et in trees), F(0)))
        self.assertEqual(table.total_mass(), F(5, 6))
        rng = random.Random(9)
        sampler = BoundedSampler(table, randbelow_from_random(rng))
        seen = set()
        for _ in range(100):
            s = sampler.sample()
            seen.add(s.terminals)
            if s.terminals == ("a",):
                self.assertEqual(s.probability, F(3, 5))
            else:
                self.assertEqual(s.probability, F(2, 5))
        self.assertEqual(seen, {("a",), ("b",)})


class StringMassTests(unittest.TestCase):
    def test_ternary_rhs_convolution(self):
        # arbitrary-length rhs: S -> SSS exercises 3-way budget convolution
        grammar = Grammar.create("S", (
            Production("S", ("S", "S", "S"), 2, group="t"),
            Production("S", ("a",), 1, group="leaf"),
            Production("S", (), 1, group="leaf"),
        ))
        table = PartitionTable(grammar, 3, 7)
        trees = enumerate_trees(grammar, 3, 7)
        joint = {}
        for et in trees:
            key = (et.tree.length, et.tree.steps)
            joint[key] = joint.get(key, F(0)) + et.weight
        dp_joint = table.joint_partition()
        for key in set(joint) | set(dp_joint):
            self.assertEqual(dp_joint.get(key, F(0)), joint.get(key, F(0)), f"joint {key}")
        rng = random.Random(17)
        sampler = BoundedSampler(table, randbelow_from_random(rng))
        denom = table.total_mass()
        for _ in range(100):
            s = sampler.sample()
            v = validate_tree(s.tree, grammar, 3, 7)
            self.assertTrue(v.ok, v.reason)
            self.assertEqual(s.probability, v.weight / denom)

    def test_catalan_string_masses(self):
        grammar = Grammar.create("S", (
            Production("S", ("S", "S"), 1),
            Production("S", ("a",), 1),
        ))
        table = PartitionTable(grammar, 4, 7)
        self.assertEqual(string_mass(table, "aa"), F(1))
        self.assertEqual(string_mass(table, "aaa"), F(2))
        self.assertEqual(string_mass(table, "aaaa"), F(5))
        self.assertEqual(string_mass(table, "aaaaa"), F(0))  # longer than L

    def test_weighted_string_mass_matches_enumeration(self):
        grammar = weighted_parens_grammar()
        table = PartitionTable(grammar, 4, 7)
        trees = enumerate_trees(grammar, 4, 7)
        for word in ("", "a", "aa", "aaa", "aaaa"):
            exact = sum((et.weight for et in trees
                         if tree_yield(et.tree, grammar) == tuple(word)), F(0))
            self.assertEqual(string_mass(table, word), exact, word)

    def test_string_mass_is_sum_not_dedup(self):
        grammar = Grammar.create("S", (
            Production("S", ("S", "S"), 2),
            Production("S", ("a",), 3),
        ))
        table = PartitionTable(grammar, 2, 3)
        # unique 'aa' tree: S->SS then two a's, weight 2*3*3 = 18
        self.assertEqual(string_mass(table, "aa", steps=3), F(18))


class FrequencySanityTests(unittest.TestCase):
    """Supplementary empirical check only; exact probabilities tested above."""

    def test_step_frequency_matches_exact_probabilities(self):
        grammar = weighted_parens_grammar()
        table = PartitionTable(grammar, 2, 5)
        rng = random.Random(424242)
        sampler = BoundedSampler(table, randbelow_from_random(rng))
        n = 3000
        counts = {}
        for _ in range(n):
            s = sampler.sample(length=2, require_groups=("struct",))
            counts[s.steps] = counts.get(s.steps, 0) + 1
        # exact expectations derived from the partition table itself
        struct_bit = table.group_bit["struct"]
        denom = table.total_mass(length=2, require_mask=struct_bit)
        for d, c in counts.items():
            exact = table.total_mass(length=2, steps=d, require_mask=struct_bit) / denom
            self.assertAlmostEqual(c / n, float(exact), delta=0.04)
        self.assertEqual(set(counts), {3, 5})


class BudgetTests(unittest.TestCase):
    def test_operation_budget_aborts_completely(self):
        grammar = weighted_parens_grammar()
        with self.assertRaises(BudgetExhaustedError):
            PartitionTable(grammar, 4, 7, budget=ResourceBudget(max_ops=1))

    def test_bit_length_budget_aborts(self):
        grammar = Grammar.create("S", (Production("S", ("S", "S"), 10),
                                       Production("S", ("a",), 10)))
        with self.assertRaises(BudgetExhaustedError):
            PartitionTable(grammar, 20, 30, budget=ResourceBudget(max_int_bits=10))


class ValidatorNegativeTests(unittest.TestCase):
    def setUp(self):
        self.grammar = weighted_parens_grammar()
        self.L, self.D = 4, 7
        self.table = PartitionTable(self.grammar, self.L, self.D)

    def test_tampered_tree_is_rejected_or_recomputed(self):
        rng = random.Random(5)
        sampler = BoundedSampler(self.table, randbelow_from_random(rng))
        s = sampler.sample()
        bad = replace(s.tree, children=s.tree.children[:-1])
        self.assertFalse(validate_tree(bad, self.grammar).ok)
        tampered = replace(s.tree, steps=999, length=999)
        v = validate_tree(tampered, self.grammar, self.L, self.D)
        self.assertTrue(v.ok)
        self.assertEqual(v.steps, s.steps)
        self.assertEqual(v.length, len(s.terminals))
        bad_root = replace(s.tree, production=Production("B", ("a",), 1))
        self.assertFalse(validate_tree(bad_root, self.grammar).ok)
        foreign = replace(s.tree, production=Production("S", ("z",), 1))
        self.assertFalse(validate_tree(foreign, self.grammar).ok)

    def test_bounds_checked(self):
        grammar = Grammar.create("S", (Production("S", ("a", "a"), 1),))
        node = TreeNode(grammar.productions[0], (), length=2, steps=1)
        v = validate_tree(node, grammar, max_length=1, max_steps=2)
        self.assertFalse(v.ok)
        self.assertEqual(v.length, 2)
        v2 = validate_tree(node, grammar, max_length=2, max_steps=2)
        self.assertTrue(v2.ok)
        v3 = validate_tree(node, grammar, max_length=2, max_steps=0)
        self.assertFalse(v3.ok)


if __name__ == "__main__":
    unittest.main()
