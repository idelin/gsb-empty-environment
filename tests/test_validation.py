import unittest

from axcon import (Budget, Executor, SparseTensor, ValidationError,
                   build_plan, dense_contract)


class TestValidation(unittest.TestCase):
    def test_illegal_dimensions_rejected_first(self):
        for bad in (-1, 2.5, "3", True, None):
            with self.assertRaises(ValidationError, msg=f"dim={bad!r}"):
                SparseTensor("T", ("i",), (bad,))

    def test_rank_mismatch(self):
        with self.assertRaises(ValidationError):
            SparseTensor("T", ("i", "j"), (3,))

    def test_illegal_label(self):
        with self.assertRaises(ValidationError):
            SparseTensor("T", ("",), (3,))
        with self.assertRaises(ValidationError):
            SparseTensor("T", (7,), (3,))

    def test_inconsistent_axis_sizes_across_inputs(self):
        a = SparseTensor("A", ("i",), (3,), {(0,): 1.0})
        b = SparseTensor("B", ("i",), (4,), {(0,): 1.0})
        with self.assertRaises(ValidationError):
            build_plan([a, b], ("i",))

    def test_unknown_output_label(self):
        a = SparseTensor("A", ("i",), (3,), {(0,): 1.0})
        with self.assertRaises(ValidationError):
            build_plan([a], ("zzz",))

    def test_duplicate_output_label(self):
        a = SparseTensor("A", ("i",), (3,), {(0,): 1.0})
        with self.assertRaises(ValidationError):
            build_plan([a], ("i", "i"))

    def test_off_diagonal_entry_rejected(self):
        with self.assertRaises(ValidationError):
            SparseTensor("D", ("i", "i"), (3, 3), {(0, 1): 1.0})

    def test_out_of_bounds_coordinate(self):
        with self.assertRaises(ValidationError):
            SparseTensor("T", ("i",), (3,), {(3,): 1.0})
        t = SparseTensor("T", ("i",), (3,))
        with self.assertRaises(ValidationError):
            t.apply_delta("d", {(-1,): 1.0})

    def test_empty_axis_gives_empty_result(self):
        a = SparseTensor("A", ("i", "j"), (0, 3))
        b = SparseTensor("B", ("j", "k"), (3, 2), {(0, 0): 1.0, (2, 1): 4.0})
        plan = build_plan([a, b], ("i", "k"))
        result = Executor().execute(plan, [a, b])
        self.assertTrue(result.completed)
        self.assertEqual(result.tensor.entries, {})
        self.assertEqual(result.tensor.shape, (0, 2))
        self.assertEqual(dense_contract([a, b], ("i", "k")), {})

    def test_scalar_tensor(self):
        s = SparseTensor("S", (), (), {(): 2.0})
        v = SparseTensor("V", ("i",), (3,), {(0,): 1.0, (2,): 3.0})
        plan = build_plan([s, v], ("i",))
        result = Executor().execute(plan, [s, v])
        self.assertEqual(result.tensor.entries, {(0,): 2.0, (2,): 6.0})

    def test_scalar_result(self):
        v = SparseTensor("V", ("i",), (3,), {(0,): 1.0, (2,): 3.0})
        w = SparseTensor("W", ("i",), (3,), {(0,): 1.0, (1,): 1.0, (2,): 2.0})
        plan = build_plan([v, w], ())
        result = Executor().execute(plan, [v, w])
        self.assertEqual(result.tensor.labels, ())
        self.assertEqual(result.tensor.entries, {(): 7.0})
        self.assertEqual(dense_contract([v, w], ()), {(): 7.0})

    def test_same_coordinate_contributions_cancel(self):
        t = SparseTensor("T", ("i",), (3,), {(0,): 5.0, (1,): 1.0})
        t.apply_delta("d1", {(0,): -5.0})          # cancels existing entry
        self.assertNotIn((0,), t.entries)
        t.apply_delta("d2", {(1,): 3.0})
        t.apply_delta("d3", {(1,): -3.0})          # two deltas cancel out
        self.assertEqual(t.entries, {(1,): 1.0})
        t.apply_delta("d4", {(2,): 0.0})           # zero contributions dropped
        self.assertEqual(t.entries, {(1,): 1.0})


if __name__ == "__main__":
    unittest.main()
