"""Affine transform algebra, chained vs composed gradients, singular maps."""

import unittest

from bilinear2d.affine import affine_apply, affine_backward, affine_compose
from bilinear2d.sampling import (
    ALIGN_CENTERS,
    BOUNDARY_CLAMP,
    sample,
    sample_backward,
)

DATA = [
    [3.0, 1.0, 4.0, 1.5],
    [5.0, 9.0, 2.0, 6.5],
    [8.0, 7.0, 0.5, 2.5],
]

POINTS = [(0.3, -0.7), (-1.1, 0.4), (0.9, 1.3)]
T1 = (0.5, 0.1, 0.2, -0.3, 0.8, -0.1)
T2 = (1.2, -0.4, 0.05, 0.2, 0.9, 0.3)
SEEDS = [0.7, -1.3, 2.1]


class TestChainVsDirect(unittest.TestCase):
    def test_compose_matches_sequential_application(self):
        composed = affine_compose(T2, T1)
        chained = affine_apply(T2, affine_apply(T1, POINTS))
        direct = affine_apply(composed, POINTS)
        for (uc, vc), (ud, vd) in zip(chained, direct):
            self.assertAlmostEqual(uc, ud, places=15)
            self.assertAlmostEqual(vc, vd, places=15)

    def test_chain_rule_matches_composed_transform(self):
        # Two-stage chain: T1 then T2, gradients propagated step by step.
        mid = affine_apply(T1, POINTS)
        out_chain = affine_apply(T2, mid)
        grad_out = [(s, -0.5 * s) for s in SEEDS]
        grad_mid, _ = affine_backward(T2, mid, grad_out)
        grad_pts_chain, _ = affine_backward(T1, POINTS, grad_mid)

        # Direct: single composed transform.
        composed = affine_compose(T2, T1)
        out_direct = affine_apply(composed, POINTS)
        grad_pts_direct, _ = affine_backward(composed, POINTS, grad_out)

        for (uc, vc), (ud, vd) in zip(out_chain, out_direct):
            self.assertAlmostEqual(uc, ud, places=14)
            self.assertAlmostEqual(vc, vd, places=14)
        for (gc_u, gc_v), (gd_u, gd_v) in zip(grad_pts_chain, grad_pts_direct):
            self.assertAlmostEqual(gc_u, gd_u, places=14)
            self.assertAlmostEqual(gc_v, gd_v, places=14)

    def test_chain_through_sampling_pipeline(self):
        # Same comparison, but gradients flow through bilinear sampling too.
        mid = affine_apply(T1, POINTS)
        coords_chain = affine_apply(T2, mid)
        values_c, _, cache_c = sample(DATA, coords_chain,
                                      align=ALIGN_CENTERS,
                                      boundary=BOUNDARY_CLAMP)
        _, grad_coords_c = sample_backward(DATA, SEEDS, cache_c)
        grad_mid, _ = affine_backward(T2, mid, grad_coords_c)
        grad_pts_chain, _ = affine_backward(T1, POINTS, grad_mid)

        composed = affine_compose(T2, T1)
        coords_direct = affine_apply(composed, POINTS)
        values_d, _, cache_d = sample(DATA, coords_direct,
                                      align=ALIGN_CENTERS,
                                      boundary=BOUNDARY_CLAMP)
        _, grad_coords_d = sample_backward(DATA, SEEDS, cache_d)
        grad_pts_direct, _ = affine_backward(composed, POINTS, grad_coords_d)

        for vc, vd in zip(values_c, values_d):
            self.assertAlmostEqual(vc, vd, places=14)
        for (gc_u, gc_v), (gd_u, gd_v) in zip(grad_pts_chain, grad_pts_direct):
            self.assertAlmostEqual(gc_u, gd_u, places=12)
            self.assertAlmostEqual(gc_v, gd_v, places=12)


class TestSingularTransform(unittest.TestCase):
    def test_rank_zero_collapses_everything(self):
        t_rank0 = (0.0, 0.0, 0.3, 0.0, 0.0, -0.2)
        mapped = affine_apply(t_rank0, POINTS)
        self.assertEqual(mapped, [(0.3, -0.2)] * 3)

        grad_out = [(0.5, 1.5), (-0.5, 0.5), (2.0, -1.0)]
        grad_points, grad_params = affine_backward(t_rank0, POINTS, grad_out)
        # Input-coordinate gradients vanish (row space is {0}).
        self.assertEqual(grad_points, [(0.0, 0.0)] * 3)
        # Parameter gradients remain well defined.
        exp = (
            sum(gu * x for (gu, _), (x, _) in zip(grad_out, POINTS)),
            sum(gu * y for (gu, _), (_, y) in zip(grad_out, POINTS)),
            sum(gu for gu, _ in grad_out),
            sum(gv * x for (_, gv), (x, _) in zip(grad_out, POINTS)),
            sum(gv * y for (_, gv), (_, y) in zip(grad_out, POINTS)),
            sum(gv for _, gv in grad_out),
        )
        for got, want in zip(grad_params, exp):
            self.assertAlmostEqual(got, want, places=15)

    def test_rank_one_through_sampling(self):
        # v is pinned to -0.4; only the u direction carries gradient.
        t_rank1 = (1.0, 0.0, 0.1, 0.0, 0.0, -0.4)
        coords = affine_apply(t_rank1, POINTS)
        self.assertEqual([v for _, v in coords], [-0.4] * 3)
        _, _, cache = sample(DATA, coords, align=ALIGN_CENTERS,
                             boundary=BOUNDARY_CLAMP)
        _, grad_coords = sample_backward(DATA, SEEDS, cache)
        grad_points, grad_params = affine_backward(t_rank1, POINTS,
                                                   grad_coords)
        for (gx, gy), (gu, _) in zip(grad_points, grad_coords):
            self.assertAlmostEqual(gx, gu, places=15)  # du/dx = 1
            self.assertEqual(gy, 0.0)                  # dv/dy = 0
        # grad w.r.t. the pinned-v parameters still accumulates normally.
        self.assertAlmostEqual(
            grad_params[5], sum(gv for _, gv in grad_coords), places=15)


if __name__ == "__main__":
    unittest.main()
