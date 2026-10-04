import math
import unittest

from tinyreduce import (Tensor, AxisError, EmptyReductionError,
                        normalize_axes)
from tests import reference


class TestAxes(unittest.TestCase):
    def test_negative_normalized(self):
        self.assertEqual(normalize_axes((-1, -2), 3), (1, 2))
        self.assertEqual(normalize_axes(-1, 3), (2,))

    def test_none_means_all(self):
        self.assertEqual(normalize_axes(None, 3), (0, 1, 2))

    def test_sorted_canonical(self):
        self.assertEqual(normalize_axes((2, 0), 3), (0, 2))

    def test_duplicates_rejected(self):
        with self.assertRaises(AxisError):
            normalize_axes((1, -2), 3)  # 1 and -2 are the same axis
        with self.assertRaises(AxisError):
            normalize_axes((0, 0), 2)

    def test_out_of_range(self):
        with self.assertRaises(AxisError):
            normalize_axes(3, 3)
        with self.assertRaises(AxisError):
            normalize_axes(-4, 3)

    def test_bad_types(self):
        with self.assertRaises(AxisError):
            normalize_axes(1.5, 3)
        with self.assertRaises(AxisError):
            normalize_axes(True, 3)
        with self.assertRaises(AxisError):
            normalize_axes("a", 3)


class TestBasicReductions(unittest.TestCase):
    def setUp(self):
        self.t = Tensor([[1, 2, 3], [4, 5, 6]])

    def test_sum_axes(self):
        self.assertEqual(self.t.sum().item(), 21.0)
        self.assertEqual(self.t.sum(axis=0).to_flat(), [5.0, 7.0, 9.0])
        self.assertEqual(self.t.sum(axis=1).to_flat(), [6.0, 15.0])
        self.assertEqual(self.t.sum(axis=(0, 1)).item(), 21.0)

    def test_mean(self):
        self.assertEqual(self.t.mean().item(), 3.5)
        self.assertEqual(self.t.mean(axis=0).to_flat(), [2.5, 3.5, 4.5])

    def test_min_max(self):
        self.assertEqual(self.t.min().item(), 1.0)
        self.assertEqual(self.t.max(axis=1).to_flat(), [3.0, 6.0])
        self.assertEqual(self.t.min(axis=0).to_flat(), [1.0, 2.0, 3.0])

    def test_keepdims(self):
        self.assertEqual(self.t.sum(axis=1, keepdims=True).shape, (2, 1))
        self.assertEqual(self.t.sum(keepdims=True).shape, (1, 1))
        self.assertEqual(self.t.sum(axis=(0, 1), keepdims=True).to_nested(),
                         [[21.0]])
        self.assertEqual(self.t.max(axis=-1, keepdims=True).to_nested(),
                         [[3.0], [6.0]])

    def test_axis_permutation_invariant(self):
        t = Tensor([[[(i * 7 + j * 3 + k) % 11 - 5 for k in range(5)]
                     for j in range(4)] for i in range(3)])
        for op in ("sum", "mean", "min", "max"):
            a = getattr(t, op)(axis=(0, 2))
            b = getattr(t, op)(axis=(2, 0))
            self.assertEqual(a.to_flat(), b.to_flat())  # exact, same order
            c = getattr(t, op)(axis=(-3, -1))
            self.assertEqual(a.to_flat(), c.to_flat())
        # sequential reduction equals joint reduction (integer data: exact)
        joint = t.sum(axis=(0, 2), keepdims=True)
        seq = t.sum(axis=2, keepdims=True).sum(axis=0, keepdims=True)
        self.assertEqual(joint.to_flat(), seq.to_flat())

    def test_noncontiguous_views(self):
        base = Tensor([[i * 6 + j for j in range(6)] for i in range(4)])
        views = [base.transpose(), base[::-1], base[:, ::2],
                 base[1:4:2, ::-1], base[::-1, ::-2]]
        for v in views:
            nested, shape = v.to_nested(), v.shape
            for axes in [None] + [(a,) for a in range(v.ndim)]:
                axes_n = normalize_axes(axes, v.ndim)
                for op in ("sum", "mean", "min", "max"):
                    ref, _, _ = reference.forward_and_grad(
                        nested, shape, axes_n, op)
                    got = getattr(v, op)(axis=axes)
                    self.assertEqual(got.shape,
                                     tuple(s for d, s in enumerate(shape)
                                           if d not in axes_n))
                    for idx in got.indices():
                        self.assertTrue(
                            reference.close(got.get(idx), ref[idx]),
                            f"{op} view mismatch at {idx}")


class TestEmptySemantics(unittest.TestCase):
    def test_sum_of_empty_is_zero(self):
        self.assertEqual(Tensor([]).sum().item(), 0.0)
        self.assertEqual(Tensor.zeros((2, 0, 3)).sum(axis=1).to_flat(),
                         [0.0] * 6)
        out = Tensor.zeros((2, 0, 3)).sum(axis=1, keepdims=True)
        self.assertEqual(out.shape, (2, 1, 3))

    def test_mean_of_empty_undefined(self):
        with self.assertRaises(EmptyReductionError):
            Tensor([]).mean()
        with self.assertRaises(EmptyReductionError):
            Tensor.zeros((2, 0, 3)).mean(axis=1)

    def test_extremes_of_empty_undefined(self):
        for op in ("min", "max"):
            with self.assertRaises(EmptyReductionError):
                getattr(Tensor([]), op)()
            with self.assertRaises(EmptyReductionError):
                getattr(Tensor.zeros((2, 0, 3)), op)(axis=1)

    def test_zero_sized_output_never_raises(self):
        # reducing non-empty axes of a tensor with an empty dim: no slices
        t = Tensor.zeros((2, 0, 3))
        for op in ("sum", "mean", "min", "max"):
            out = getattr(t, op)(axis=(0, 2))
            self.assertEqual(out.shape, (0,))
            self.assertEqual(out.reduction_stats.slices, 0)

    def test_all_masked_is_empty(self):
        t = Tensor([1.0, 2.0])
        mask = Tensor([1, 1])
        self.assertEqual(t.sum(mask=mask).item(), 0.0)
        for op in ("mean", "min", "max"):
            with self.assertRaises(EmptyReductionError):
                getattr(t, op)(mask=mask)

    def test_partial_mask(self):
        t = Tensor([[1.0, 2.0], [3.0, 4.0]])
        mask = Tensor([[0, 1], [0, 0]])
        self.assertEqual(t.sum(axis=1, mask=mask).to_flat(), [1.0, 7.0])
        self.assertEqual(t.mean(axis=1, mask=mask).to_flat(), [1.0, 3.5])
        self.assertEqual(t.max(mask=mask).item(), 4.0)
        with self.assertRaises(ValueError):
            t.sum(mask=Tensor([0, 1]))  # shape mismatch


class TestSpecialValues(unittest.TestCase):
    def test_infinities(self):
        self.assertEqual(Tensor([1.0, math.inf]).max().item(), math.inf)
        self.assertEqual(Tensor([1.0, -math.inf]).min().item(), -math.inf)
        self.assertEqual(Tensor([math.inf, math.inf]).max().item(), math.inf)
        self.assertEqual(Tensor([1.0, math.inf]).sum().item(), math.inf)
        self.assertEqual(Tensor([1.0, math.inf]).mean().item(), math.inf)

    def test_inf_minus_inf_is_nan(self):
        self.assertTrue(math.isnan(Tensor([math.inf, -math.inf]).sum().item()))

    def test_nan_propagates(self):
        for op in ("sum", "mean", "min", "max"):
            self.assertTrue(
                math.isnan(getattr(Tensor([1.0, math.nan, 2.0]), op)().item()),
                op)


class TestChunkedTreeExecution(unittest.TestCase):
    def setUp(self):
        self.t = Tensor([float(i % 7) for i in range(1024)])

    def test_chunk_size_invariant(self):
        want = {op: getattr(self.t, op)().item() for op in
                ("sum", "mean", "min", "max")}
        for cs in (1, 2, 3, 5, 64, 1024, 10_000):
            for op, w in want.items():
                got = getattr(self.t, op)(chunk_size=cs).item()
                self.assertEqual(got, w, f"{op} chunk_size={cs}")

    def test_stats_recorded(self):
        out = self.t.sum(chunk_size=1)
        st = out.reduction_stats
        self.assertEqual(st.elements, 1024)
        self.assertEqual(st.chunks, 1024)
        self.assertEqual(st.combines, 1023)  # elements - slices, always
        self.assertEqual(st.peak_workspace, 12)  # binary-tree peak, O(log n)

    def test_peak_workspace_bounded(self):
        st = self.t.sum(chunk_size=1024).reduction_stats
        self.assertEqual(st.chunks, 1)
        self.assertEqual(st.peak_workspace, 1)
        st = self.t.sum(chunk_size=3).reduction_stats
        self.assertLessEqual(st.peak_workspace,
                             math.ceil(math.log2(st.chunks)) + 3)
        self.assertGreaterEqual(st.peak_workspace, 2)

    def test_combines_invariant(self):
        # every chunking of the same data performs exactly n-1 combines
        for cs in (1, 2, 7, 100):
            st = self.t.min(chunk_size=cs).reduction_stats
            self.assertEqual(st.combines, 1023)

    def test_invalid_chunk_size(self):
        for bad in (0, -1, 1.5, True):
            with self.assertRaises(ValueError):
                self.t.sum(chunk_size=bad)


if __name__ == "__main__":
    unittest.main()
