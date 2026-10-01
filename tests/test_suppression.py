import random
import unittest

from equivstats import (
    NullPolicy,
    Record,
    all_classes_pass,
    exact_min_suppression,
    failing_classes,
    greedy_suppression,
)


def skewed_records():
    # 类 ("A",)：x*8, y, z —— 熵约 0.922 (base 2)，阈值 1.0 不达标。
    records = [Record(("A",), "x") for _ in range(8)]
    records += [Record(("A",), "y"), Record(("A",), "z")]
    # 类 ("B",)：四个等频值，H=2.0 明确达标。
    records += [Record(("B",), v) for v in ("p", "q", "r", "s")]
    return records


class TestExactSuppression(unittest.TestCase):
    def test_minimal_suppression_for_entropy(self):
        records = skewed_records()
        removed = exact_min_suppression(records, 2, 1.0, NullPolicy.IGNORE, 2)
        self.assertIsNotNone(removed)
        self.assertEqual(len(removed), 2)  # 删 2 条 x 后 6/8,1/8,1/8 -> H≈1.061
        remaining = [r for i, r in enumerate(records) if i not in set(removed)]
        self.assertTrue(all_classes_pass(remaining, 2, 1.0, NullPolicy.IGNORE, 2))
        # 删 1 条不够：7/9,1/9,1/9 -> H≈0.986 < 1.0
        self.assertFalse(
            all_classes_pass(records[1:], 2, 1.0, NullPolicy.IGNORE, 2))

    def test_distinct_failure_requires_whole_class_suppression(self):
        records = [Record(("A",), "x") for _ in range(3)]
        records += [Record(("B",), v) for v in ("p", "q")]
        removed = exact_min_suppression(records, 2, 0.0, NullPolicy.IGNORE, 2)
        self.assertEqual(len(removed), 3)  # 删除无法增加不同数，只能整类抑制

    def test_already_compliant_needs_nothing(self):
        records = [Record(("A",), v) for v in ("p", "q", "p", "q")]
        removed = exact_min_suppression(records, 2, 0.5, NullPolicy.IGNORE, 2)
        self.assertEqual(removed, [])

    def test_nulls_counted_as_category_in_suppression(self):
        records = [Record(("A",), None), Record(("A",), "x"),
                   Record(("A",), None), Record(("A",), "x")]
        self.assertFalse(failing_classes(records, 2, 0.5, NullPolicy.AS_CATEGORY, 2))
        self.assertTrue(failing_classes(records, 2, 0.5, NullPolicy.IGNORE, 2))


class TestGreedySuppression(unittest.TestCase):
    def test_greedy_matches_exact_on_skewed(self):
        records = skewed_records()
        exact = exact_min_suppression(records, 2, 1.0, NullPolicy.IGNORE, 2)
        greedy = greedy_suppression(records, 2, 1.0, NullPolicy.IGNORE, 2)
        self.assertEqual(len(greedy), len(exact))

    def test_greedy_output_is_valid_and_exact_is_lower_bound(self):
        rng = random.Random(20261001)
        for _ in range(8):
            n = rng.randint(4, 9)
            records = [
                Record((f"q{rng.randint(0, 1)}",), rng.choice(["a", "b", "c", None]))
                for _ in range(n)
            ]
            min_entropy = rng.choice([0.5, 0.9, 1.0])
            exact = exact_min_suppression(
                records, 2, min_entropy, NullPolicy.AS_CATEGORY, 2)
            if exact is None:
                continue
            greedy = greedy_suppression(
                records, 2, min_entropy, NullPolicy.AS_CATEGORY, 2)
            remaining = [r for i, r in enumerate(records) if i not in set(greedy)]
            self.assertTrue(
                all_classes_pass(remaining, 2, min_entropy, NullPolicy.AS_CATEGORY, 2))
            self.assertGreaterEqual(len(greedy), len(exact))


if __name__ == "__main__":
    unittest.main()
