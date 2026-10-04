import math
import random
import unittest

from tinyreduce import (Tensor, NonDifferentiableError, value_and_grad)


class TestBasicGrads(unittest.TestCase):
    def test_sum_grad_is_ones(self):
        t = Tensor([[1.0, 2.0], [3.0, 4.0]]).requires_grad_()
        t.sum().backward()
        self.assertEqual(t.grad.to_flat(), [1.0] * 4)

    def test_sum_grad_masked_is_zero(self):
        t = Tensor([1.0, 2.0, 3.0]).requires_grad_()
        t.sum(mask=Tensor([0, 1, 0])).backward()
        self.assertEqual(t.grad.to_flat(), [1.0, 0.0, 1.0])

    def test_sum_upstream_broadcast(self):
        t = Tensor([[1.0, 2.0], [3.0, 4.0]]).requires_grad_()
        t.sum(axis=0).backward(Tensor([10.0, 100.0]))
        self.assertEqual(t.grad.to_nested(),
                         [[10.0, 100.0], [10.0, 100.0]])

    def test_mean_grad(self):
        t = Tensor([1.0, 2.0, 3.0, 4.0]).requires_grad_()
        t.mean().backward()
        self.assertEqual(t.grad.to_flat(), [0.25] * 4)

    def test_min_max_unique(self):
        t = Tensor([3.0, 1.0, 2.0]).requires_grad_()
        t.min().backward()
        self.assertEqual(t.grad.to_flat(), [0.0, 1.0, 0.0])
        t.zero_grad()
        t.max().backward()
        self.assertEqual(t.grad.to_flat(), [1.0, 0.0, 0.0])

    def test_grad_shape_matches_input_regardless_of_axes(self):
        t = Tensor.zeros((2, 3, 4)).requires_grad_()
        for axes in (None, (0,), (0, 2), (2, 0), (1, -1)):
            t.zero_grad()
            t.sum(axis=axes, keepdims=True).backward(
                Tensor.ones(t.sum(axis=axes, keepdims=True).shape))
            self.assertEqual(t.grad.shape, (2, 3, 4))


class TestTiePolicy(unittest.TestCase):
    def test_max_tie_splits_equally_not_first_element(self):
        _, g = value_and_grad(Tensor([1.0, 3.0, 3.0, 2.0]), "max")
        self.assertEqual(g.to_flat(), [0.0, 0.5, 0.5, 0.0])  # never [0,1,0,0]

    def test_min_three_way_tie(self):
        _, g = value_and_grad(Tensor([2.0, 2.0, 2.0]), "min")
        self.assertEqual(g.to_flat(), [1 / 3] * 3)

    def test_tie_policy_consistent_across_chunks(self):
        data = [5.0, 1.0, 5.0, 2.0, 5.0, 0.0, 5.0]
        grads = []
        for cs in (None, 1, 2, 3, 4, 100):
            _, g = value_and_grad(Tensor(data), "max", chunk_size=cs)
            grads.append(g.to_flat())
        for g in grads[1:]:
            self.assertEqual(g, grads[0])
        self.assertEqual(grads[0], [0.25, 0.0, 0.25, 0.0, 0.25, 0.0, 0.25])

    def test_ties_marked_nondifferentiable(self):
        _, g = value_and_grad(Tensor([1.0, 1.0, 2.0]), "min")
        self.assertEqual(g.nondiff_indices, ((0,), (1,)))
        _, g = value_and_grad(Tensor([1.0, 2.0, 3.0]), "min")
        self.assertEqual(g.nondiff_indices, ())

    def test_inf_ties(self):
        _, g = value_and_grad(Tensor([math.inf, math.inf, 1.0]), "max")
        self.assertEqual(g.to_flat(), [0.5, 0.5, 0.0])
        self.assertEqual(g.nondiff_indices, ((0,), (1,)))


class TestHigherOrder(unittest.TestCase):
    def test_second_order_through_tie_raises(self):
        t = Tensor([1.0, 1.0, 2.0])
        _, g = value_and_grad(t, "min")
        with self.assertRaises(NonDifferentiableError) as cm:
            g.backward(Tensor.ones(g.shape))
        self.assertIn("(0,)", str(cm.exception))

    def test_second_order_unique_extremum_is_zero(self):
        t = Tensor([1.0, 2.0, 3.0]).requires_grad_()
        _, g = value_and_grad(t, "max")
        g.backward(Tensor.ones(g.shape))
        self.assertEqual(t.grad.to_flat(), [0.0, 0.0, 0.0])

    def test_second_order_sum_mean_is_zero(self):
        for op in ("sum", "mean"):
            t = Tensor([1.0, 2.0, 3.0]).requires_grad_()
            _, g = value_and_grad(t, op)
            g.backward(Tensor.ones(g.shape))
            self.assertEqual(t.grad.to_flat(), [0.0, 0.0, 0.0], op)

    def test_autograd_path_also_marks(self):
        t = Tensor([2.0, 2.0]).requires_grad_()
        t.max().backward()
        self.assertEqual(t.grad.to_flat(), [0.5, 0.5])
        self.assertEqual(t.nondiff_indices, ((0,), (1,)))
        t.zero_grad()
        self.assertEqual(t.nondiff_indices, ())


class TestAliasAccumulation(unittest.TestCase):
    def test_overlapping_views_accumulate(self):
        t = Tensor([1.0, 2.0, 3.0, 4.0]).requires_grad_()
        v1, v2 = t[0:3], t[1:4]
        v1.sum().backward()
        v2.sum().backward()
        self.assertEqual(t.grad.to_flat(), [1.0, 2.0, 2.0, 1.0])

    def test_same_tensor_two_ops_accumulate(self):
        t = Tensor([1.0, 5.0, 3.0]).requires_grad_()
        t.sum().backward()
        t.max().backward()
        self.assertEqual(t.grad.to_flat(), [1.0, 2.0, 1.0])

    def test_repeated_backward_accumulates(self):
        t = Tensor([1.0, 2.0]).requires_grad_()
        t.sum().backward()
        t.sum().backward()
        self.assertEqual(t.grad.to_flat(), [2.0, 2.0])
        t.zero_grad()
        t.sum().backward()
        self.assertEqual(t.grad.to_flat(), [1.0, 1.0])


class TestFiniteDifferences(unittest.TestCase):
    def test_central_difference(self):
        rng = random.Random(20261004)
        for op in ("sum", "mean", "min", "max"):
            data = [rng.uniform(-3, 3) for _ in range(6)]  # ties: prob 0
            t = Tensor(data)
            out, g = value_and_grad(t, op)
            h = 1e-6
            for i in range(6):
                plus, minus = data[:], data[:]
                plus[i] += h
                minus[i] -= h
                f_plus = getattr(Tensor(plus), op)().item()
                f_minus = getattr(Tensor(minus), op)().item()
                num = (f_plus - f_minus) / (2 * h)
                self.assertAlmostEqual(num, g.get((i,)), places=4,
                                       msg=f"{op} index {i}")


if __name__ == "__main__":
    unittest.main()
