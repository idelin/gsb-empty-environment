import random
import unittest

from tinymat import (Tensor, inner, iter_indices, matmul, matmul_backward,
                     matmul_hvp, matmul_jvp)
from tinymat.verify import (classify_discrepancy, fd_first_directional,
                            fd_grad_physical, fd_second_directional,
                            first_derivative_bounds, perturb,
                            physical_cells_of, second_derivative_bounds)


def rand_tensor(shape, rng, lo=-2.0, hi=2.0):
    t = Tensor.zeros(shape)
    for idx in iter_indices(shape):
        t.set_at(idx, rng.uniform(lo, hi))
    return t


def loss_of(seed, a, b):
    return inner(seed, matmul(a, b))


class TestGradFiniteDifference(unittest.TestCase):
    """Analytic VJP vs central finite differences on *physical* cells."""

    CASES = []

    def _make_cases(self):
        rng = random.Random(1234)
        cases = []
        # (name, a, b) pairs covering dense / batched / broadcast / vec /
        # non-contiguous views / k == 0
        cases.append(("dense", rand_tensor((2, 3), rng), rand_tensor((3, 4), rng)))
        cases.append(("batched", rand_tensor((3, 2, 3), rng),
                      rand_tensor((3, 3, 2), rng)))
        cases.append(("broadcast_a", rand_tensor((1, 2, 3), rng),
                      rand_tensor((4, 3, 2), rng)))
        cases.append(("broadcast_both", rand_tensor((2, 1, 2, 3), rng),
                      rand_tensor((1, 3, 3, 2), rng)))
        cases.append(("vec_vec", rand_tensor((3,), rng), rand_tensor((3,), rng)))
        cases.append(("vec_mat", rand_tensor((3,), rng),
                      rand_tensor((2, 3, 4), rng)))
        cases.append(("mat_vec", rand_tensor((2, 3), rng),
                      rand_tensor((3,), rng)))
        a_t = rand_tensor((3, 2), rng).transpose(1, 0)          # (2,3) view
        b_s = rand_tensor((5, 6), rng).slice_dim(0, 0, 3).slice_dim(1, 1, 5)  # (3,4)
        cases.append(("views", a_t, b_s))
        a_bc = rand_tensor((2, 3), rng).broadcast_to((4, 2, 3))  # stride-0
        cases.append(("broadcast_view", a_bc, rand_tensor((4, 3, 2), rng)))
        cases.append(("k_zero", rand_tensor((2, 0), rng),
                      rand_tensor((0, 3), rng)))
        return cases

    def test_grad_matches_finite_differences(self):
        rng = random.Random(99)
        for name, a, b in self._make_cases():
            with self.subTest(case=name):
                seed = rand_tensor(matmul(a, b).shape, rng)
                grad_a, grad_b = matmul_backward(seed, a, b)
                self.assertEqual(grad_a.shape, a.shape)
                self.assertEqual(grad_b.shape, b.shape)

                h = 1e-5
                f_scale = 1.0 + abs(loss_of(seed, a, b))
                trunc, rnd = first_derivative_bounds(f_scale, h)
                for tensor, grad in ((a, grad_a), (b, grad_b)):
                    cells = physical_cells_of(tensor)
                    fd = fd_grad_physical(lambda: loss_of(seed, a, b),
                                          cells, h=h)
                    for (buf, p), fd_g in zip(cells, fd):
                        verdict, err, bound = classify_discrepancy(
                            grad.data[p], fd_g, trunc, rnd)
                        self.assertEqual(
                            verdict, "numerical",
                            f"{name}: grad mismatch at cell {p}: "
                            f"analytic={grad.data[p]} fd={fd_g} err={err:.3e} "
                            f"> bound={bound:.3e}")

    def test_broadcast_gradient_sums_over_batches(self):
        """A broadcast to many batches: grad_A = sum of per-batch grads."""
        rng = random.Random(5)
        a = rand_tensor((3, 4), rng)                 # no batch dim
        b = rand_tensor((5, 4, 2), rng)              # 5 batches
        seed = rand_tensor((5, 3, 2), rng)
        grad_a, _ = matmul_backward(seed, a, b)
        # independent accumulation: run 5 separate single-batch problems
        expect = Tensor.zeros((3, 4))
        for i in range(5):
            b_i = b.slice_dim(0, i, i + 1)           # (1,4,2) view
            s_i = seed.slice_dim(0, i, i + 1)        # (1,3,2) view
            g_i, _ = matmul_backward(s_i, a, b_i)
            for idx in iter_indices((3, 4)):
                expect.set_at(idx, expect.value_at(idx) + g_i.value_at(idx))
        for idx in iter_indices((3, 4)):
            self.assertAlmostEqual(grad_a.value_at(idx), expect.value_at(idx),
                                   places=10)

    def test_physical_shared_storage_accumulates(self):
        """All logical elements share ONE physical cell: contributions sum."""
        buf = [0.7]
        t = Tensor(buf, (2, 3), (0, 0))              # 6 aliases of buf[0]
        rng = random.Random(6)
        b = rand_tensor((3, 4), rng)
        seed = rand_tensor((2, 4), rng)
        grad_t, _ = matmul_backward(seed, t, b)
        self.assertEqual(len(grad_t.data), 1)
        # finite difference of the single physical cell
        h = 1e-6
        orig = buf[0]
        buf[0] = orig + h
        fp = loss_of(seed, t, b)
        buf[0] = orig - h
        fm = loss_of(seed, t, b)
        buf[0] = orig
        fd = (fp - fm) / (2 * h)
        self.assertAlmostEqual(grad_t.data[0], fd, places=4)
        # and it equals the sum of the 6 per-element derivatives
        per_elem = 0.0
        for i in range(2):
            for kk in range(3):
                for j in range(4):
                    per_elem += seed.value_at((i, j)) * b.value_at((kk, j))
        self.assertAlmostEqual(grad_t.data[0], per_elem, places=10)

    def test_inner_dim_zero_gradients(self):
        rng = random.Random(8)
        a = rand_tensor((2, 0), rng)
        b = rand_tensor((0, 3), rng)
        seed = rand_tensor((2, 3), rng)
        grad_a, grad_b = matmul_backward(seed, a, b)
        self.assertEqual(grad_a.shape, (2, 0))
        self.assertEqual(grad_b.shape, (0, 3))
        self.assertEqual(grad_a.data, [])           # no cells, no crash
        self.assertEqual(grad_b.data, [])


class TestJVP(unittest.TestCase):
    def test_jvp_vs_directional_fd(self):
        rng = random.Random(21)
        for name, sa, sb in [("dense", (2, 3), (3, 4)),
                             ("broadcast", (1, 2, 3), (5, 3, 2)),
                             ("vec", (4,), (4,)),
                             ("mat_vec", (3, 4), (4,))]:
            with self.subTest(case=name):
                a, b = rand_tensor(sa, rng), rand_tensor(sb, rng)
                da, db = rand_tensor(sa, rng), rand_tensor(sb, rng)
                jvp = matmul_jvp(a, b, da, db)

                def f_at(t):
                    perturb(a, da, t)
                    perturb(b, db, t)
                    out = matmul(a, b).to_nested()
                    perturb(a, da, -t)
                    perturb(b, db, -t)
                    return out

                h = 1e-6
                plus, minus = f_at(h), f_at(-h)

                def flat(x):
                    return [x] if not isinstance(x, list) else \
                        [v for e in x for v in flat(e)]

                fp, fm, fj = flat(plus), flat(minus), flat(jvp.to_nested())
                for p, m, j in zip(fp, fm, fj):
                    self.assertAlmostEqual((p - m) / (2 * h), j, places=4)

    def test_jvp_composes_with_views(self):
        """Tangents of transposed/broadcast views give the same JVP as the
        equivalent contiguous problem."""
        rng = random.Random(22)
        base_a = rand_tensor((3, 2), rng)
        a = base_a.transpose(1, 0)                   # (2,3) non-contiguous
        b = rand_tensor((3, 4), rng).broadcast_to((2, 3, 4))
        da = rand_tensor((2, 3), rng)
        db = rand_tensor((2, 3, 4), rng)
        got = matmul_jvp(a, b, da, db)
        want = matmul_jvp(a.contiguous(), b.contiguous(), da, db)
        for idx in iter_indices(got.shape):
            self.assertAlmostEqual(got.value_at(idx), want.value_at(idx),
                                   places=10)


class TestHVP(unittest.TestCase):
    def _setup(self, rng, sa=(2, 3), sb=(3, 4)):
        a, b = rand_tensor(sa, rng), rand_tensor(sb, rng)
        u, v = rand_tensor(sa, rng), rand_tensor(sb, rng)
        seed = rand_tensor(matmul(a, b).shape, rng)
        return a, b, u, v, seed

    def test_hvp_vs_gradient_fd(self):
        """HVP equals the directional derivative of the gradient."""
        rng = random.Random(31)
        a, b, u, v, seed = self._setup(rng)
        h_a, h_b = matmul_hvp(seed, a, b, u, v)
        eps = 1e-5
        perturb(a, u, eps)
        perturb(b, v, eps)
        gp_a, gp_b = matmul_backward(seed, a, b)
        perturb(a, u, -2 * eps)
        perturb(b, v, -2 * eps)
        gm_a, gm_b = matmul_backward(seed, a, b)
        perturb(a, u, eps)
        perturb(b, v, eps)
        for idx in iter_indices(a.shape):
            fd = (gp_a.value_at(idx) - gm_a.value_at(idx)) / (2 * eps)
            self.assertAlmostEqual(h_a.value_at(idx), fd, places=4)
        for idx in iter_indices(b.shape):
            fd = (gp_b.value_at(idx) - gm_b.value_at(idx)) / (2 * eps)
            self.assertAlmostEqual(h_b.value_at(idx), fd, places=4)

    def test_hvp_vs_second_directional_difference(self):
        """<u,H_a> + <v,H_b> matches the second difference of the loss."""
        rng = random.Random(32)
        for sa, sb in [((2, 3), (3, 4)), ((1, 2, 3), (4, 3, 2)), ((3,), (3,))]:
            with self.subTest(sa=sa, sb=sb):
                a, b, u, v, seed = self._setup(rng, sa, sb)
                h_a, h_b = matmul_hvp(seed, a, b, u, v)
                quad = inner(u, h_a) + inner(v, h_b)

                def f_at(t):
                    perturb(a, u, t)
                    perturb(b, v, t)
                    val = loss_of(seed, a, b)
                    perturb(a, u, -t)
                    perturb(b, v, -t)
                    return val

                h = 1e-3
                fd2 = fd_second_directional(f_at, h)
                f_scale = 1.0 + abs(f_at(0.0))
                trunc, rnd = second_derivative_bounds(f_scale, h)
                verdict, err, bound = classify_discrepancy(quad, fd2,
                                                           trunc, rnd)
                self.assertEqual(verdict, "numerical",
                                 f"quad={quad} fd2={fd2} err={err:.3e}")

    def test_hvp_composes_with_views_and_broadcast(self):
        rng = random.Random(33)
        a = rand_tensor((2, 3), rng).broadcast_to((4, 2, 3))  # stride-0 batch
        b = rand_tensor((4, 2, 3), rng).transpose(0, 2, 1)    # (4,3,2) view
        u = rand_tensor((4, 2, 3), rng)
        v = rand_tensor((4, 3, 2), rng)
        seed = rand_tensor((4, 2, 2), rng)
        h_a, h_b = matmul_hvp(seed, a, b, u, v)
        # reference: same computation on contiguous copies
        ra, rb = a.contiguous(), b.contiguous()
        rh_a, rh_b = matmul_hvp(seed, ra, rb, u, v)
        # a is broadcast over 4 batches: its HVP must be the batch-sum of
        # the per-batch reference, accumulated into shared physical cells.
        self.assertEqual(len(h_a.data), len(a.data))
        for i in range(2):
            for kk in range(3):
                expect = sum(rh_a.value_at((j, i, kk)) for j in range(4))
                self.assertAlmostEqual(h_a.value_at((0, i, kk)), expect,
                                       places=10)
        for idx in iter_indices(b.shape):
            self.assertAlmostEqual(h_b.value_at(idx), rh_b.value_at(idx),
                                   places=10)


class TestErrorClassification(unittest.TestCase):
    def test_numerical_vs_genuine(self):
        # a discrepancy inside the FD error model -> "numerical"
        verdict, _, _ = classify_discrepancy(1.0, 1.0 + 1e-8, 1e-6, 1e-9)
        self.assertEqual(verdict, "numerical")
        # far outside the model -> "genuine" (bug / non-differentiable point)
        verdict, _, _ = classify_discrepancy(1.0, 2.0, 1e-6, 1e-9)
        self.assertEqual(verdict, "genuine")

    def test_directional_first_difference_helper(self):
        # d/dt (t^3) at t=0 is 0; central difference error ~ h^2
        val = fd_first_directional(lambda t: t ** 3, h=1e-3)
        self.assertAlmostEqual(val, 1e-6, places=12)


if __name__ == "__main__":
    unittest.main()
