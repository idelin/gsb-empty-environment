"""Independent numerical verification: finite differences, tiling, checkpoint.

Finite differences are computed straight from the forward functions, fully
independent of the hand-written backward code.  Probe points are chosen away
from integer nodes and boundary kinks (those non-differentiable points are
covered separately in tests/test_sampling.py).
"""

import unittest

from bilinear2d.affine import affine_apply, affine_backward
from bilinear2d.sampling import (
    ALIGN_CENTERS,
    ALIGN_CORNERS,
    BOUNDARY_CLAMP,
    BOUNDARY_REFLECT,
    BOUNDARY_ZEROS,
    sample,
    sample_backward,
)
from bilinear2d.tiling import sample_grid_tiled, sample_grid_tiled_backward

DATA = [
    [3.0, 1.0, 4.0, 1.5],
    [5.0, 9.0, 2.0, 6.5],
    [8.0, 7.0, 0.5, 2.5],
]

H = 1e-6


def loss(data, coords, grad_out, align, boundary):
    values, _, _ = sample(data, coords, align=align, boundary=boundary)
    return sum(v * g for v, g in zip(values, grad_out))


def fd_grad_data(data, coords, grad_out, align, boundary):
    data = [row[:] for row in data]
    grad = []
    for i in range(len(data)):
        row = []
        for j in range(len(data[0])):
            orig = data[i][j]
            data[i][j] = orig + H
            plus = loss(data, coords, grad_out, align, boundary)
            data[i][j] = orig - H
            minus = loss(data, coords, grad_out, align, boundary)
            data[i][j] = orig
            row.append((plus - minus) / (2 * H))
        grad.append(row)
    return grad


def fd_grad_coords(data, coords, grad_out, align, boundary):
    grad = []
    for k, (u, v) in enumerate(coords):
        perturbed = list(coords)
        perturbed[k] = (u + H, v)
        plus = loss(data, perturbed, grad_out, align, boundary)
        perturbed[k] = (u - H, v)
        minus = loss(data, perturbed, grad_out, align, boundary)
        gu = (plus - minus) / (2 * H)
        perturbed[k] = (u, v + H)
        plus = loss(data, perturbed, grad_out, align, boundary)
        perturbed[k] = (u, v - H)
        minus = loss(data, perturbed, grad_out, align, boundary)
        gv = (plus - minus) / (2 * H)
        grad.append((gu, gv))
    return grad


def assert_nested_close(test, got, want, delta=1e-6):
    for row_g, row_w in zip(got, want):
        for g, w in zip(row_g, row_w):
            test.assertAlmostEqual(g, w, delta=delta)


class TestFiniteDifferences(unittest.TestCase):
    # Coordinates kept strictly inside cells and away from boundary kinks.
    CASES = {
        BOUNDARY_CLAMP: [(0.23, -0.47), (-0.61, 0.83), (2.5, -1.7)],
        BOUNDARY_REFLECT: [(2.0, 0.4), (-2.7, 1.3), (0.31, -0.56)],
        BOUNDARY_ZEROS: [(0.23, -0.47), (-0.61, 0.83), (1.5, 0.2)],
    }

    def test_grad_data_and_coords(self):
        grad_out = [0.7, -1.3, 2.1]
        for boundary, coords in self.CASES.items():
            for align in (ALIGN_CORNERS, ALIGN_CENTERS):
                with self.subTest(boundary=boundary, align=align):
                    _, _, cache = sample(DATA, coords, align=align,
                                         boundary=boundary)
                    grad_data, grad_coords = sample_backward(
                        DATA, grad_out, cache)
                    assert_nested_close(
                        self, grad_data,
                        fd_grad_data(DATA, coords, grad_out, align, boundary))
                    for got, want in zip(
                            grad_coords,
                            fd_grad_coords(DATA, coords, grad_out, align,
                                           boundary)):
                        self.assertAlmostEqual(got[0], want[0], delta=1e-6)
                        self.assertAlmostEqual(got[1], want[1], delta=1e-6)

    def test_affine_param_gradients(self):
        # Full pipeline: base points -> affine -> normalized coords -> sample.
        base_points = [(0.3, -0.7), (-1.1, 0.4), (0.9, 1.3)]
        params = (0.4, 0.1, 0.05, -0.2, 0.5, 0.1)
        grad_out = [0.7, -1.3, 2.1]
        align, boundary = ALIGN_CENTERS, BOUNDARY_CLAMP

        def pipeline_loss(p):
            coords = affine_apply(p, base_points)
            return loss(DATA, coords, grad_out, align, boundary)

        coords = affine_apply(params, base_points)
        _, _, cache = sample(DATA, coords, align=align, boundary=boundary)
        _, grad_coords = sample_backward(DATA, grad_out, cache)
        grad_points, grad_params = affine_backward(params, base_points,
                                                   grad_coords)

        for k in range(6):
            plus = list(params)
            minus = list(params)
            plus[k] += H
            minus[k] -= H
            fd = (pipeline_loss(plus) - pipeline_loss(minus)) / (2 * H)
            self.assertAlmostEqual(grad_params[k], fd, delta=1e-6)

        for k, (x, y) in enumerate(base_points):
            for axis in range(2):
                plus = list(base_points)
                minus = list(base_points)
                plus[k] = (x + H, y) if axis == 0 else (x, y + H)
                minus[k] = (x - H, y) if axis == 0 else (x, y - H)
                lp = loss(DATA, affine_apply(params, plus), grad_out, align,
                          boundary)
                lm = loss(DATA, affine_apply(params, minus), grad_out, align,
                          boundary)
                self.assertAlmostEqual(grad_points[k][axis],
                                       (lp - lm) / (2 * H), delta=1e-6)


class TestTiling(unittest.TestCase):
    def test_tiled_matches_full_execution(self):
        # Coordinates span beyond [-1, 1] so boundary handling is exercised
        # inside every tile; a tile-local clamp/reflect would mismatch the
        # full execution here.
        coords_grid = [
            [(1.3 * (2 * ix / 5 - 1), 1.2 * (2 * iy / 3 - 1))
             for ix in range(6)]
            for iy in range(4)
        ]
        grad_grid = [[(iy * 1.7 - ix * 0.3 + 0.2) for ix in range(6)]
                     for iy in range(4)]
        flat_coords = [c for row in coords_grid for c in row]
        flat_grad = [g for row in grad_grid for g in row]

        for boundary in (BOUNDARY_CLAMP, BOUNDARY_REFLECT, BOUNDARY_ZEROS):
            with self.subTest(boundary=boundary):
                # Full (untiled) reference.
                values, masks, cache = sample(DATA, flat_coords,
                                              align=ALIGN_CENTERS,
                                              boundary=boundary)
                grad_data, grad_coords = sample_backward(DATA, flat_grad,
                                                         cache)

                # Tiled with ragged edges (4 = 3 + 1 rows, 6 = 2 * 3 cols).
                forward = sample_grid_tiled(DATA, coords_grid, (3, 2),
                                            ALIGN_CENTERS, boundary)
                tiled_values = [v for row in forward["values"] for v in row]
                tiled_masks = [m for row in forward["masks"] for m in row]
                self.assertEqual(tiled_values, values)
                self.assertEqual(tiled_masks, masks)

                t_grad_data, t_grad_coords = sample_grid_tiled_backward(
                    DATA, grad_grid, forward)
                flat_t_coords = [c for row in t_grad_coords for c in row]
                self.assertEqual(flat_t_coords, grad_coords)
                # grad_data accumulates across tiles in a different order.
                assert_nested_close(self, t_grad_data, grad_data, delta=1e-12)


class TestCheckpoint(unittest.TestCase):
    def test_recompute_matches_cached(self):
        coords = [(0.23, -0.47), (2.0, 0.4), (-1.5, 1.3), (0.0, 0.0)]
        grad_out = [0.7, -1.3, 2.1, 0.4]
        for boundary in (BOUNDARY_CLAMP, BOUNDARY_REFLECT, BOUNDARY_ZEROS):
            for align in (ALIGN_CORNERS, ALIGN_CENTERS):
                with self.subTest(boundary=boundary, align=align):
                    v1, m1, cache = sample(DATA, coords, align=align,
                                           boundary=boundary)
                    v2, m2, cache_cp = sample(DATA, coords, align=align,
                                              boundary=boundary,
                                              checkpoint=True)
                    self.assertIsNone(cache_cp["points"])  # nothing stored
                    self.assertEqual(v1, v2)
                    self.assertEqual(m1, m2)
                    g1 = sample_backward(DATA, grad_out, cache)
                    g2 = sample_backward(DATA, grad_out, cache_cp)
                    # Deterministic recompute: bitwise-identical results.
                    self.assertEqual(g1[0], g2[0])
                    self.assertEqual(g1[1], g2[1])


if __name__ == "__main__":
    unittest.main()
