import math
import random
import unittest

from tinyreduce import (Tensor, GradStore, WorkspaceTracker,
                        UndefinedReductionError, NonDifferentiableError,
                        normalize_axes, reduce, backward)
from tinyreduce.verify import (naive_reduce, run_enumeration,
                               run_alias_enumeration, finite_difference_check)


class AxisNormalizationTest(unittest.TestCase):
    def test_negative_axes(self):
        self.assertEqual(normalize_axes(-1, 3), (2,))
        self.assertEqual(normalize_axes((-3, -1), 3), (0, 2))
        self.assertEqual(normalize_axes(None, 2), (0, 1))

    def test_duplicate_axes_rejected(self):
        with self.assertRaises(ValueError):
            normalize_axes((1, -1), 2)  # same axis after normalization
        with self.assertRaises(ValueError):
            normalize_axes((0, 0), 2)
        t = Tensor.from_nested([[1.0, 2.0], [3.0, 4.0]])
        with self.assertRaises(ValueError):
            reduce(t, "sum", axes=(0, -2))

    def test_out_of_range_rejected(self):
        with self.assertRaises(ValueError):
            normalize_axes(3, 2)
        with self.assertRaises(ValueError):
            normalize_axes(-3, 2)


class EmptySemanticsTest(unittest.TestCase):
    def test_sum_of_empty_is_zero(self):
        self.assertEqual(reduce(Tensor([], (0,)), "sum").value.get(()), 0.0)
        t = Tensor.from_nested([1.0, 2.0], mask_nested=[False, False])
        self.assertEqual(reduce(t, "sum").value.get(()), 0.0)

    def test_mean_min_max_of_empty_undefined(self):
        for op in ("mean", "min", "max"):
            with self.assertRaises(UndefinedReductionError):
                reduce(Tensor([], (0,)), op)
            t = Tensor.from_nested([1.0, 2.0], mask_nested=[False, False])
            with self.assertRaises(UndefinedReductionError, msg=op):
                reduce(t, op)

    def test_empty_dims(self):
        t = Tensor([], (2, 0))
        self.assertEqual(reduce(t, "sum").value.get(()), 0.0)
        # reducing axis 0 leaves zero output groups: no error, empty tensor
        r = reduce(t, "mean", axes=0)
        self.assertEqual(r.value.shape, (0,))
        # reducing axis 1 leaves two empty groups: undefined for mean
        with self.assertRaises(UndefinedReductionError):
            reduce(t, "mean", axes=1)
        self.assertEqual(reduce(t, "sum", axes=1).value.logical_values(),
                         [0.0, 0.0])


class KeepdimsTest(unittest.TestCase):
    def test_keepdims_shape_and_values(self):
        t = Tensor.from_nested([[1.0, 2.0, 3.0], [4.0, 5.0, 6.0]])
        r = reduce(t, "sum", axes=1, keepdims=True)
        self.assertEqual(r.value.shape, (2, 1))
        self.assertEqual(r.value.logical_values(), [6.0, 15.0])
        r = reduce(t, "sum", axes=(0, 1), keepdims=True)
        self.assertEqual(r.value.shape, (1, 1))
        self.assertEqual(r.value.get((0, 0)), 21.0)
        r = reduce(t, "sum", axes=1, keepdims=False)
        self.assertEqual(r.value.shape, (2,))

    def test_upstream_accepts_both_shapes(self):
        t = Tensor.from_nested([[1.0, 2.0], [3.0, 4.0]])
        r = reduce(t, "sum", axes=1, keepdims=True)
        g1 = backward(t, r, Tensor.from_nested([[1.0], [2.0]])).logical_values()
        g2 = backward(t, r, Tensor.from_nested([1.0, 2.0])).logical_values()
        self.assertEqual(g1, g2)
        self.assertEqual(g1, [1.0, 1.0, 2.0, 2.0])


class NonContiguousViewTest(unittest.TestCase):
    def test_transpose_view_matches_contiguous(self):
        base = Tensor.from_nested([[float(i * 4 + j) for j in range(4)]
                                   for i in range(3)])
        view = base.transpose((1, 0))
        cont = view.to_contiguous()
        for op in ("sum", "mean", "min", "max"):
            for axes in (None, 0, 1, (0, 1)):
                rv = reduce(view, op, axes=axes)
                rc = reduce(cont, op, axes=axes)
                self.assertEqual(rv.value, rc.value)
                gv = backward(view, rv, 1.0).logical_values()
                gc = backward(cont, rc, 1.0).logical_values()
                self.assertEqual(gv, gc)

    def test_strided_slice_view(self):
        base = Tensor.from_nested([[1.0, 9.0, 2.0, 8.0],
                                   [3.0, 7.0, 4.0, 6.0]])
        view = base.slice_axis(1, 0, 4, 2)  # columns 0 and 2
        self.assertEqual(view.shape, (2, 2))
        r = reduce(view, "sum", axes=0)
        self.assertEqual(r.value.logical_values(), [4.0, 6.0])
        g = backward(view, r, 1.0)
        self.assertEqual(g.logical_values(), [1.0, 1.0, 1.0, 1.0])
        # gradient lands in the shared buffer at the right positions
        store = GradStore()
        backward(view, r, 1.0, store=store)
        self.assertEqual(store.buffer_for(base),
                         [1.0, 0.0, 1.0, 0.0, 1.0, 0.0, 1.0, 0.0])


class ChunkInvarianceTest(unittest.TestCase):
    def test_values_and_grads_identical_across_chunks(self):
        rng = random.Random(42)
        values = [rng.choice([1.0, 2.0, 3.0]) for _ in range(24)]
        t = Tensor(list(values), (4, 6))
        for op in ("sum", "mean", "min", "max"):
            ref = reduce(t, op, chunk_size=1 << 30)
            ref_g = backward(t, ref, 1.0).logical_values()
            for chunk in (1, 2, 3, 5, 7, 24):
                res = reduce(t, op, chunk_size=chunk)
                self.assertEqual(res.value.logical_values(),
                                 ref.value.logical_values(),
                                 msg=f"{op} chunk={chunk}")
                self.assertEqual(res.nondiff_outputs, ref.nondiff_outputs)
                g = backward(t, res, 1.0).logical_values()
                self.assertEqual(g, ref_g, msg=f"{op} grad chunk={chunk}")


class TiePolicyTest(unittest.TestCase):
    def test_uniform_split_and_marking(self):
        t = Tensor.from_nested([2.0, 1.0, 1.0, 3.0])
        r = reduce(t, "min")
        self.assertEqual(r.nondiff_outputs, frozenset({()}))
        g = backward(t, r, 2.0).logical_values()
        self.assertEqual(g, [0.0, 1.0, 1.0, 0.0])

    def test_first_policy(self):
        t = Tensor.from_nested([2.0, 1.0, 1.0, 3.0])
        r = reduce(t, "min")
        g = backward(t, r, 2.0, tie_policy="first").logical_values()
        self.assertEqual(g, [0.0, 2.0, 0.0, 0.0])
        # still marked non-differentiable: policy only picks a subgradient
        self.assertTrue(r.nondiff_outputs)

    def test_strict_mode_raises(self):
        t = Tensor.from_nested([1.0, 1.0])
        r = reduce(t, "max")
        with self.assertRaises(NonDifferentiableError):
            backward(t, r, 1.0, on_nondiff="raise")
        # no ties -> strict mode is quiet
        t2 = Tensor.from_nested([1.0, 2.0])
        r2 = reduce(t2, "max")
        backward(t2, r2, 1.0, on_nondiff="raise")

    def test_nondiff_inputs_are_buffer_positions(self):
        t = Tensor.from_nested([5.0, 5.0, 1.0])
        r = reduce(t, "max")
        self.assertEqual(r.nondiff_inputs, frozenset({0, 1}))


class AxisPermutationTest(unittest.TestCase):
    def test_permutation_invariance(self):
        t = Tensor(list(range(1, 25)), (2, 3, 4))
        t = Tensor([float(v) for v in t.data], t.shape)
        for op in ("sum", "mean", "min", "max"):
            base = reduce(t, op, axes=(0, 2))
            for axes in ((2, 0), (-1, 0), (0, -1), (-3, -1)):
                res = reduce(t, op, axes=axes)
                self.assertEqual(res.value.shape, base.value.shape)
                self.assertEqual(res.value.logical_values(),
                                 base.value.logical_values())
                g = backward(t, res, 1.0)
                self.assertEqual(g.shape, t.shape)
                self.assertEqual(g.logical_values(),
                                 backward(t, base, 1.0).logical_values())


class InfNanTest(unittest.TestCase):
    def test_inf_extremes(self):
        t = Tensor.from_nested([1.0, math.inf, 2.0])
        self.assertEqual(reduce(t, "max").value.get(()), math.inf)
        g = backward(t, reduce(t, "max"), 1.0).logical_values()
        self.assertEqual(g, [0.0, 1.0, 0.0])
        t2 = Tensor.from_nested([1.0, -math.inf, 2.0])
        self.assertEqual(reduce(t2, "min").value.get(()), -math.inf)

    def test_inf_ties_split_and_marked(self):
        t = Tensor.from_nested([math.inf, math.inf, 1.0])
        r = reduce(t, "max")
        self.assertEqual(r.value.get(()), math.inf)
        self.assertEqual(r.nondiff_outputs, frozenset({()}))
        self.assertEqual(backward(t, r, 1.0).logical_values(),
                         [0.5, 0.5, 0.0])

    def test_sum_with_inf_and_nan(self):
        t = Tensor.from_nested([math.inf, 1.0])
        self.assertEqual(reduce(t, "sum").value.get(()), math.inf)
        t2 = Tensor.from_nested([math.inf, -math.inf])
        self.assertTrue(math.isnan(reduce(t2, "sum").value.get(())))
        t3 = Tensor.from_nested([float("nan"), 1.0])
        self.assertTrue(math.isnan(reduce(t3, "min").value.get(())))


class MaskTest(unittest.TestCase):
    def test_masked_elements_excluded(self):
        t = Tensor.from_nested([1.0, 99.0, 3.0],
                               mask_nested=[True, False, True])
        self.assertEqual(reduce(t, "sum").value.get(()), 4.0)
        self.assertEqual(reduce(t, "mean").value.get(()), 2.0)
        self.assertEqual(reduce(t, "max").value.get(()), 3.0)
        g = backward(t, reduce(t, "sum"), 1.0).logical_values()
        self.assertEqual(g, [1.0, 0.0, 1.0])

    def test_mean_gradient_uses_included_count(self):
        t = Tensor.from_nested([1.0, 2.0, 3.0, 4.0],
                               mask_nested=[True, False, True, True])
        r = reduce(t, "mean")
        self.assertEqual(r.value.get(()), 8.0 / 3.0)
        g = backward(t, r, 6.0).logical_values()
        self.assertEqual(g, [2.0, 0.0, 2.0, 2.0])


class AliasAccumulationTest(unittest.TestCase):
    def test_overlapping_views_accumulate(self):
        data = [1.0, 2.0, 3.0, 4.0, 5.0]
        base = Tensor(data, (5,))
        a = base.slice_axis(0, 0, 3)
        b = base.slice_axis(0, 2, 5)
        store = GradStore()
        backward(a, reduce(a, "sum"), 1.0, store=store)
        backward(b, reduce(b, "sum"), 1.0, store=store)
        self.assertEqual(store.buffer_for(base), [1.0, 1.0, 2.0, 1.0, 1.0])

    def test_same_tensor_reduced_twice_accumulates(self):
        t = Tensor.from_nested([[1.0, 2.0], [3.0, 4.0]])
        store = GradStore()
        backward(t, reduce(t, "sum", axes=0), Tensor.from_nested([1.0, 1.0]),
                 store=store)
        backward(t, reduce(t, "sum", axes=1), Tensor.from_nested([10.0, 20.0]),
                 store=store)
        self.assertEqual(store.grad_tensor(t).logical_values(),
                         [11.0, 11.0, 21.0, 21.0])


class WorkspaceTest(unittest.TestCase):
    def test_peak_matches_chunk_count(self):
        t = Tensor(list(range(10)), (10,))
        for chunk, expected in ((1, 10), (2, 5), (3, 4), (4, 3), (10, 1),
                                (100, 1)):
            r = reduce(t, "sum", chunk_size=chunk)
            self.assertEqual(r.workspace_peak, expected,
                             msg=f"chunk={chunk}")

    def test_tracker_shared_across_groups(self):
        tracker = WorkspaceTracker()
        t = Tensor(list(range(12)), (3, 4))
        reduce(t, "sum", axes=1, chunk_size=2, tracker=tracker)
        self.assertEqual(tracker.peak, 2)  # ceil(4/2) per group
        self.assertEqual(tracker.current, 0)  # fully released


class FiniteDifferenceTest(unittest.TestCase):
    def test_analytic_grad_matches_finite_difference(self):
        errors = finite_difference_check()
        for op, err in errors.items():
            self.assertLess(err, 1e-5, msg=f"{op} fd error {err}")


class EnumerationTest(unittest.TestCase):
    """Independent small-domain enumeration vs the naive reference."""

    def test_engine_matches_naive_reference(self):
        report = run_enumeration()
        self.assertEqual(report["failures"], [],
                         msg="\n".join(report["failures"]))
        self.assertGreater(report["grad_checks"], 0)
        self.assertGreater(report["nondiff_checks"], 0)
        print(f"\nenumeration: {report['configs']} configs, "
              f"{report['reductions']} reductions, "
              f"{report['grad_checks']} grad checks, "
              f"peak workspace {report['peak_workspace']}")

    def test_alias_gradient_accumulation(self):
        report = run_alias_enumeration()
        self.assertEqual(report["failures"], [],
                         msg="\n".join(report["failures"]))
        print(f"\nalias enumeration: {report['configs']} configs OK")


if __name__ == "__main__":
    unittest.main()
