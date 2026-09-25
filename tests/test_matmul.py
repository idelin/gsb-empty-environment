import random
import unittest

from tinymat import Tensor, iter_indices, matmul
from tests.naive_ref import naive_matmul, flatten


def rand_nested(shape, rng):
    if not shape:
        return rng.uniform(-2, 2)
    return [rand_nested(shape[1:], rng) for _ in range(shape[0])]


def rand_tensor(shape, rng):
    """Random tensor with an exact shape (robust to zero-sized dims)."""
    t = Tensor.zeros(shape)
    for idx in iter_indices(shape):
        t.set_at(idx, rng.uniform(-2, 2))
    return t


def check_against_naive(tc, a_t, b_t, got):
    want, want_shape = naive_matmul(a_t.to_nested(), b_t.to_nested(),
                                    a_t.shape, b_t.shape)
    tc.assertEqual(got.shape, want_shape)
    assert_nested_close(tc, got.to_nested(), want)


def assert_nested_close(tc, got_nested, want_nested, places=9):
    got, want = flatten(got_nested), flatten(want_nested)
    tc.assertEqual(len(got), len(want))
    for g, w in zip(got, want):
        tc.assertAlmostEqual(g, w, places=places)


class TestForwardExhaustive(unittest.TestCase):
    """Exhaustive small core shapes vs the independent triple loop."""

    def test_core_shapes_0_to_3(self):
        rng = random.Random(20260926)
        for m in range(4):
            for k in range(4):
                for n in range(4):
                    with self.subTest(m=m, k=k, n=n):
                        a = rand_tensor((m, k), rng)
                        b = rand_tensor((k, n), rng)
                        check_against_naive(self, a, b, matmul(a, b))

    def test_batch_broadcast_combos(self):
        rng = random.Random(7)
        combos = [
            ((2,), ()),
            ((), (3,)),
            ((1,), (3,)),
            ((2, 1), (3,)),
            ((2, 1), (1, 3)),
            ((2, 3), (2, 3)),
            ((1, 1), (2, 3)),
            ((4,), (1,)),
        ]
        for ab, bb in combos:
            for (m, k, n) in [(2, 3, 2), (1, 1, 1), (2, 0, 3)]:
                with self.subTest(ab=ab, bb=bb, core=(m, k, n)):
                    a = rand_tensor(ab + (m, k), rng)
                    b = rand_tensor(bb + (k, n), rng)
                    check_against_naive(self, a, b, matmul(a, b))

    def test_vector_special_cases(self):
        rng = random.Random(11)
        cases = [
            ((3,), (3,)),
            ((3,), (3, 2)),
            ((2, 3), (3,)),
            ((2, 3), (3, 4)),
            ((2, 3), (3,)),          # batched left, vector right
            ((3,), (2, 3, 4)),       # vector left, batched right
            ((5, 2, 3), (3,)),       # batch x matrix @ vector
            ((3,), (0,)) if False else ((0,), (0,)),  # empty vectors
            ((0,), (0, 4)),
            ((2, 0), (0,)),
        ]
        for sa, sb in cases:
            with self.subTest(sa=sa, sb=sb):
                a = rand_tensor(sa, rng)
                b = rand_tensor(sb, rng)
                check_against_naive(self, a, b, matmul(a, b))

    def test_inner_dim_zero_is_defined(self):
        a = Tensor.zeros((2, 0))
        b = Tensor.zeros((0, 3))
        out = matmul(a, b)
        self.assertEqual(out.shape, (2, 3))
        self.assertEqual(flatten(out.to_nested()), [0.0] * 6)
        # batched, broadcast, and vector forms
        out2 = matmul(Tensor.zeros((4, 2, 0)), Tensor.zeros((0, 5)))
        self.assertEqual(out2.shape, (4, 2, 5))
        self.assertEqual(flatten(out2.to_nested()), [0.0] * 40)
        self.assertEqual(matmul(Tensor.zeros((0,)),
                                Tensor.zeros((0,))).to_nested(), 0.0)

    def test_shape_errors(self):
        with self.assertRaises(ValueError):
            matmul(Tensor.zeros((2, 3)), Tensor.zeros((4, 2)))
        with self.assertRaises(ValueError):
            matmul(Tensor.zeros((2, 3, 4)), Tensor.zeros((3, 4, 5)))


class TestNonContiguousInputs(unittest.TestCase):
    """Views (transpose/slice/broadcast, composed) read correctly."""

    def _check(self, a_view, b_view, rng):
        check_against_naive(self, a_view, b_view, matmul(a_view, b_view))

    def test_transposed_operands(self):
        rng = random.Random(3)
        a = rand_tensor((3, 2), rng).transpose(1, 0)
        b = rand_tensor((4, 3), rng).transpose(1, 0)
        self._check(a, b, rng)

    def test_strided_slices(self):
        rng = random.Random(4)
        big_a = rand_tensor((6, 8), rng)
        a = big_a.slice_dim(0, 0, 6, 2).slice_dim(1, 1, 7, 2)  # (3,3)
        big_b = rand_tensor((5, 6), rng)
        b = big_b.slice_dim(0, 0, 3).slice_dim(1, 5, None, -1)  # (3,6) flipped
        self._check(a, b, rng)

    def test_broadcast_views_not_copied(self):
        rng = random.Random(5)
        a_small = rand_tensor((2, 3), rng)
        a = a_small.broadcast_to((7, 2, 3))     # stride-0 batch axis
        self.assertEqual(len(a.data), 6)        # buffer stays small: no copy
        b = rand_tensor((7, 3, 4), rng)
        self._check(a, b, rng)
        # both sides broadcast
        b2 = rand_tensor((3, 4), rng).broadcast_to((7, 3, 4))
        self._check(a, b2, rng)

    def test_composed_axis_transform(self):
        rng = random.Random(6)
        base = rand_tensor((4, 6), rng)
        a = (base.transpose(1, 0)               # (6,4)
             .slice_dim(0, 0, 6, 2)             # (3,4)
             .broadcast_to((5, 3, 4)))          # batched broadcast
        b = rand_tensor((4, 2), rng)
        self._check(a, b, rng)


class TestBlockedWorkspace(unittest.TestCase):
    def test_budget_invariance(self):
        rng = random.Random(9)
        a = rand_tensor((7, 9), rng)
        b = rand_tensor((9, 6), rng)
        ref = matmul(a, b).to_nested()
        for budget in (3, 4, 7, 12, 27, 100, 10 ** 9):
            with self.subTest(budget=budget):
                got = matmul(a, b, workspace_budget=budget).to_nested()
                # summation order differs across budgets; compare loosely
                assert_nested_close(self, got, ref, places=9)

    def test_budget_too_small_raises(self):
        a = Tensor.zeros((2, 2))
        b = Tensor.zeros((2, 2))
        with self.assertRaises(ValueError):
            matmul(a, b, workspace_budget=2)

    def test_batched_blocked(self):
        rng = random.Random(10)
        a = rand_tensor((3, 5, 4), rng)
        b = rand_tensor((3, 4, 6), rng)
        got = matmul(a, b, workspace_budget=3).to_nested()
        want, _ = naive_matmul(a.to_nested(), b.to_nested(), a.shape, b.shape)
        assert_nested_close(self, got, want)


if __name__ == "__main__":
    unittest.main()
