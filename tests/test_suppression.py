import itertools
import random
import unittest

from metrics import Config
from suppression import (
    is_feasible,
    min_suppression_exact,
    min_suppression_greedy,
    total_deletions,
)


def brute_force_min_deletions(counts, config):
    """独立的小域枚举基准：itertools.product 全量扫描删除向量。"""
    counts = {v: c for v, c in counts.items() if c > 0}
    values = sorted(counts, key=repr)
    best = None
    for vec in itertools.product(*(range(counts[v] + 1) for v in values)):
        remaining = {v: counts[v] - d for v, d in zip(values, vec)}
        if is_feasible(remaining, config):
            total = sum(vec)
            if best is None or total < best:
                best = total
    return best


class TestHandComputedCases(unittest.TestCase):
    def test_entropy_fixed_by_removing_dominant_records(self):
        # {a:4, b:1} base2 熵约 0.722；删 1 个 a -> 0.811；删 2 个 a -> 0.918
        config = Config(distinct_threshold=1, entropy_threshold=0.9, log_base=2.0)
        plan = min_suppression_exact({"a": 4, "b": 1}, config)
        self.assertEqual(plan, {"a": 2})
        self.assertEqual(min_suppression_greedy({"a": 4, "b": 1}, config), {"a": 2})

    def test_distinct_failure_requires_emptying_class(self):
        config = Config(distinct_threshold=2, entropy_threshold=0.0, log_base=2.0)
        plan = min_suppression_exact({"a": 3}, config)
        self.assertEqual(total_deletions(plan), 3)

    def test_already_feasible_needs_no_suppression(self):
        config = Config(distinct_threshold=2, entropy_threshold=0.5, log_base=2.0)
        self.assertEqual(min_suppression_exact({"a": 2, "b": 2}, config), {})

    def test_undecided_entropy_treated_as_infeasible(self):
        # 熵恰为 1.0 时区间为未决，保守起见继续抑制
        config = Config(distinct_threshold=1, entropy_threshold=1.0, log_base=2.0)
        plan = min_suppression_exact({"a": 2, "b": 2}, config)
        self.assertGreaterEqual(total_deletions(plan), 1)


class TestExactAgainstBruteForce(unittest.TestCase):
    def test_small_domain_enumeration_cross_check(self):
        rng = random.Random(2024)
        for _ in range(40):
            k = rng.randint(1, 4)
            counts = {f"v{i}": rng.randint(1, 4) for i in range(k)}
            config = Config(
                distinct_threshold=rng.randint(1, 3),
                entropy_threshold=rng.choice([0.0, 0.5, 0.9, 1.2]),
                log_base=2.0,
            )
            exact_total = total_deletions(min_suppression_exact(counts, config))
            brute_total = brute_force_min_deletions(counts, config)
            self.assertEqual(exact_total, brute_total)

    def test_greedy_never_beats_exact(self):
        rng = random.Random(7)
        for _ in range(40):
            k = rng.randint(1, 4)
            counts = {f"v{i}": rng.randint(1, 4) for i in range(k)}
            config = Config(
                distinct_threshold=rng.randint(1, 3),
                entropy_threshold=rng.choice([0.0, 0.5, 0.9]),
                log_base=2.0,
            )
            exact_total = total_deletions(min_suppression_exact(counts, config))
            greedy_plan = min_suppression_greedy(counts, config)
            self.assertLessEqual(exact_total, total_deletions(greedy_plan))
            remaining = {v: c - greedy_plan.get(v, 0) for v, c in counts.items()}
            self.assertTrue(is_feasible(remaining, config))


if __name__ == "__main__":
    unittest.main()
