import math
import unittest

from migration import Batch, Column, Type, problem_from_batches


class TestColumnTyping(unittest.TestCase):
    def test_null_and_nan_are_distinct(self):
        b = Batch([Column("x", Type.FLOAT64, [None, float("nan"), 1.0])])
        self.assertTrue(b.is_null("x", 0))
        self.assertFalse(b.is_nan("x", 0))
        self.assertFalse(b.is_null("x", 1))
        self.assertTrue(b.is_nan("x", 1))
        self.assertFalse(b.is_null("x", 2))
        self.assertFalse(b.is_nan("x", 2))
        self.assertNotEqual(b.value("x", 0), b.value("x", 1))

    def test_is_nan_rejects_non_float(self):
        b = Batch([Column("x", Type.INT64, [1])])
        with self.assertRaises(TypeError):
            b.is_nan("x", 0)

    def test_int64_exact_big_integers(self):
        big = [2**62 + 1, 2**62 + 2, 2**62 + 3]
        b = Batch([Column("w", Type.INT64, big)])
        total = sum(b.column("w").values)
        self.assertEqual(total, 3 * 2**62 + 6)
        self.assertIsInstance(total, int)

    def test_type_validation(self):
        with self.assertRaises(TypeError):
            Column("w", Type.INT64, [True])
        with self.assertRaises(TypeError):
            Column("w", Type.INT64, [1.5])
        with self.assertRaises(TypeError):
            Column("x", Type.FLOAT64, ["1.0"])
        with self.assertRaises(TypeError):
            Column("s", Type.STRING, [1])
        with self.assertRaises(TypeError):
            Column("b", Type.BOOL, [1])

    def test_length_mismatch_rejected(self):
        with self.assertRaises(ValueError):
            Batch([Column("a", Type.INT64, [1, 2]), Column("b", Type.INT64, [1])])

    def test_nan_cannot_enter_int_column(self):
        with self.assertRaises(TypeError):
            Column("w", Type.INT64, [float("nan")])


def _slots_batch():
    return Batch([Column("sid", Type.STRING, ["s0", "s1"]),
                  Column("capacity", Type.INT64, [100, 100])])


class TestProblemFromBatches(unittest.TestCase):
    def test_loads_typed_batches(self):
        parts = Batch([Column("pid", Type.STRING, ["a", "b"]),
                       Column("weight", Type.INT64, [60, 30]),
                       Column("slot", Type.STRING, ["s0", "s1"])])
        problem = problem_from_batches(parts, _slots_batch(), budget=50)
        self.assertEqual(problem.weights, {"a": 60, "b": 30})
        self.assertEqual(problem.initial, {"a": "s0", "b": "s1"})
        self.assertEqual(problem.budget, 50)

    def test_null_in_required_column_rejected(self):
        parts = Batch([Column("pid", Type.STRING, ["a"]),
                       Column("weight", Type.INT64, [None]),
                       Column("slot", Type.STRING, ["s0"])])
        with self.assertRaises(ValueError):
            problem_from_batches(parts, _slots_batch(), budget=0)

    def test_wrong_column_type_rejected(self):
        parts = Batch([Column("pid", Type.STRING, ["a"]),
                       Column("weight", Type.FLOAT64, [1.0]),
                       Column("slot", Type.STRING, ["s0"])])
        with self.assertRaises(TypeError):
            problem_from_batches(parts, _slots_batch(), budget=0)

    def test_constraints_from_batches(self):
        parts = Batch([Column("pid", Type.STRING, ["a", "b"]),
                       Column("weight", Type.INT64, [10, 10]),
                       Column("slot", Type.STRING, ["s0", "s1"])])
        anti = Batch([Column("left", Type.STRING, ["a"]),
                      Column("right", Type.STRING, ["b"])])
        problem = problem_from_batches(parts, _slots_batch(), budget=0,
                                       anti_affinity=anti)
        self.assertEqual(problem.anti_affinity, frozenset({frozenset({"a", "b"})}))


if __name__ == "__main__":
    unittest.main()
