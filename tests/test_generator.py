import unittest

from cdc import TABLES, generate, validate_stream
from cdc.events import Change, Crash


class GeneratorTest(unittest.TestCase):
    def test_deterministic_same_seed(self):
        g1 = generate(seed=42)
        g2 = generate(seed=42)
        self.assertEqual(g1.history, g2.history)
        self.assertEqual(g1.stream, g2.stream)
        self.assertEqual(g1.stats, g2.stats)

    def test_different_seeds_differ(self):
        self.assertNotEqual(generate(seed=1).stream, generate(seed=2).stream)

    def test_generated_stream_is_valid(self):
        for seed in range(30):
            g = generate(seed=seed)
            validate_stream(g.stream, TABLES)  # 不抛异常即通过
            validate_stream(g.history, TABLES)

    def test_stream_contains_perturbations(self):
        g = generate(seed=0)
        self.assertGreater(g.stats["duplicates"], 0)
        self.assertGreater(g.stats["reordered_txns"], 0)
        self.assertGreater(g.stats["crashes"], 0)
        self.assertTrue(any(isinstance(ev, Crash) for ev in g.stream))
        self.assertGreater(len(g.stream), len(set(g.stream)))  # 有重复事件

    def test_changes_carry_required_fields(self):
        g = generate(seed=7)
        for ev in g.stream:
            if isinstance(ev, Change):
                self.assertIn(ev.table, TABLES)
                self.assertIsInstance(ev.pk, str)
                self.assertGreaterEqual(ev.seq, 0)
                # before/after 为 None 或已冻结的行
                for value in (ev.before, ev.after):
                    self.assertTrue(value is None or isinstance(value, tuple))


if __name__ == "__main__":
    unittest.main()
