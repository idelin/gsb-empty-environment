"""Conventions, boundary rules, accumulation and degenerate cases."""

import unittest

from bilinear2d.sampling import (
    ALIGN_CENTERS,
    ALIGN_CORNERS,
    BOUNDARY_CLAMP,
    BOUNDARY_REFLECT,
    BOUNDARY_ZEROS,
    sample,
    sample_backward,
)

# H=3, W=4, non-uniform so left/right derivatives differ.
DATA = [
    [3.0, 1.0, 4.0, 1.5],
    [5.0, 9.0, 2.0, 6.5],
    [8.0, 7.0, 0.5, 2.5],
]

# 3x3: u=0 / v=0 map exactly to the integer node p=1 under align="corners".
DATA3 = [
    [1.0, 2.0, 4.0],
    [8.0, 16.0, 32.0],
    [3.0, 9.0, 27.0],
]


def run(data, coords, grad_out, align=ALIGN_CORNERS, boundary=BOUNDARY_CLAMP):
    values, masks, cache = sample(data, coords, align=align, boundary=boundary)
    grad_data, grad_coords = sample_backward(data, grad_out, cache)
    return values, masks, grad_data, grad_coords


def flat(grid):
    return [x for row in grid for x in row]


class TestAlignMapping(unittest.TestCase):
    def test_corners_endpoints_hit_corner_pixels(self):
        values, masks, _, _ = run(DATA, [(-1.0, -1.0), (1.0, 1.0)], [1.0, 1.0])
        self.assertEqual(values, [DATA[0][0], DATA[-1][-1]])
        self.assertEqual(masks, [1.0, 1.0])

    def test_centers_shifts_by_half_pixel(self):
        # u = -1 + 1/W, v = -1 + 1/H land exactly on pixel (0, 0).
        u = -1.0 + 1.0 / 4
        v = -1.0 + 1.0 / 3
        values, _, _, _ = run(DATA, [(u, v)], [1.0], align=ALIGN_CENTERS)
        self.assertAlmostEqual(values[0], DATA[0][0], places=15)
        # u = -1 maps to p = -0.5: clamped to edge, or masked out with zeros.
        values, _, _, _ = run(DATA, [(-1.0, v)], [1.0], align=ALIGN_CENTERS,
                              boundary=BOUNDARY_CLAMP)
        self.assertAlmostEqual(values[0], DATA[0][0], places=15)
        _, masks, _, _ = run(DATA, [(-1.0, v)], [1.0], align=ALIGN_CENTERS,
                             boundary=BOUNDARY_ZEROS)
        self.assertEqual(masks, [0.0])


class TestSharedPositionAccumulation(unittest.TestCase):
    def test_repeated_sampling_gradients_add_up(self):
        coord = (0.23, -0.47)
        grad_out = [1.0, -0.5, 2.0]
        _, _, grad_data, grad_coords = run(DATA, [coord] * 3, grad_out)

        # Independent reference: sum of three separate backward passes.
        ref_data = [[0.0] * 4 for _ in range(3)]
        ref_coords = []
        for g in grad_out:
            _, _, gd, gc = run(DATA, [coord], [g])
            for i in range(3):
                for j in range(4):
                    ref_data[i][j] += gd[i][j]
            ref_coords.append(gc[0])

        self.assertEqual(flat(grad_data), flat(ref_data))
        self.assertEqual(grad_coords, ref_coords)
        # Each sample's own coordinate gradient scales with its grad_out.
        _, _, _, single = run(DATA, [coord], [1.0])
        for k, g in enumerate(grad_out):
            self.assertAlmostEqual(grad_coords[k][0], single[0][0] * g,
                                   places=15)
            self.assertAlmostEqual(grad_coords[k][1], single[0][1] * g,
                                   places=15)
        # Interior sample: bilinear weights sum to 1, so grad_data sums to
        # the total outgoing gradient.
        self.assertAlmostEqual(sum(flat(grad_data)), sum(grad_out), places=12)


class TestBoundaryRules(unittest.TestCase):
    def test_reflect_region_gradient_carries_direction(self):
        # u=2.0 -> p=4.5, reflected to q=1.5 with dq/dp = -1.
        # u=0.0 -> p=1.5 directly with dq/dp = +1.
        v = 0.4
        vals_ref, _, _, gc_ref = run(DATA, [(2.0, v)], [1.0],
                                     boundary=BOUNDARY_REFLECT)
        vals_dir, _, _, gc_dir = run(DATA, [(0.0, v)], [1.0],
                                     boundary=BOUNDARY_CLAMP)
        self.assertAlmostEqual(vals_ref[0], vals_dir[0], places=15)
        self.assertAlmostEqual(gc_ref[0][0], -gc_dir[0][0], places=15)
        self.assertAlmostEqual(gc_ref[0][1], gc_dir[0][1], places=15)

    def test_clamp_outside_has_zero_coordinate_gradient(self):
        vals_edge, _, _, _ = run(DATA, [(1.0, 0.4)], [1.0])
        vals_out, masks, _, gc_out = run(DATA, [(2.5, 0.4)], [1.0])
        self.assertEqual(masks, [1.0])  # clamp stays valid
        self.assertAlmostEqual(vals_out[0], vals_edge[0], places=15)
        self.assertEqual(gc_out[0][0], 0.0)  # clamped axis: no gradient
        self.assertNotEqual(gc_out[0][1], 0.0)  # other axis still flows

    def test_zeros_mask_closed_interval_and_outside(self):
        # u=1.0 -> p = W-1 exactly: still valid (closed interval).
        _, masks, _, gc_in = run(DATA, [(1.0, 0.0)], [1.0],
                                 boundary=BOUNDARY_ZEROS)
        self.assertEqual(masks, [1.0])
        self.assertNotEqual(gc_in[0][0], 0.0)
        # Strictly outside: value 0, mask 0, all gradients 0.
        vals, masks, gd, gc = run(DATA, [(1.5, 0.0), (-1.5, 0.0)], [1.0, 1.0],
                                  boundary=BOUNDARY_ZEROS)
        self.assertEqual(vals, [0.0, 0.0])
        self.assertEqual(masks, [0.0, 0.0])
        self.assertEqual(flat(gd), [0.0] * 12)
        self.assertEqual(gc, [(0.0, 0.0), (0.0, 0.0)])


class TestSubgradientConventions(unittest.TestCase):
    """Non-differentiable points follow the documented explicit rules."""

    def test_integer_node_uses_right_cell(self):
        # align="corners", 3x3: u=0 -> p=1 exactly (integer node).
        _, _, _, gc = run(DATA3, [(0.0, 0.0)], [1.0])
        right_cell = DATA3[1][2] - DATA3[1][1]  # 16.0
        left_cell = DATA3[1][1] - DATA3[1][0]   # 8.0
        self.assertNotEqual(right_cell, left_cell)
        self.assertEqual(gc[0][0], right_cell)   # dp/du = (N-1)/2 = 1
        self.assertEqual(gc[0][1], DATA3[2][1] - DATA3[1][1])  # right cell, y

    def test_last_node_uses_last_cell(self):
        # u=1.0 -> p = N-1 = 2: derivative taken inside cell [N-2, N-1].
        values, _, _, gc = run(DATA3, [(1.0, 0.0)], [1.0])
        self.assertEqual(values[0], DATA3[1][2])
        self.assertEqual(gc[0][0], DATA3[1][2] - DATA3[1][1])

    def test_clamp_endpoint_uses_interior_side(self):
        # Exactly on the boundary: gradient flows (interior side)...
        _, _, _, gc_on = run(DATA, [(1.0, 0.4)], [1.0])
        self.assertNotEqual(gc_on[0][0], 0.0)
        # ...strictly outside: zero.
        _, _, _, gc_off = run(DATA, [(1.0 + 1e-9, 0.4)], [1.0])
        self.assertEqual(gc_off[0][0], 0.0)

    def test_reflect_turning_point_takes_plus_one_branch(self):
        # u=1.0 -> p=3.0 = L (turning point): dq/dp = +1, same as clamp.
        _, _, _, gc_ref = run(DATA, [(1.0, 0.4)], [1.0],
                              boundary=BOUNDARY_REFLECT)
        _, _, _, gc_clamp = run(DATA, [(1.0, 0.4)], [1.0],
                                boundary=BOUNDARY_CLAMP)
        self.assertEqual(gc_ref, gc_clamp)
        # u=3.0 -> p=6.0 = 2L: folds back to q=0 with dq/dp = +1,
        # identical to clamp at u=-1.0 (p=0).
        vals_ref, _, _, gc_ref2 = run(DATA, [(3.0, 0.4)], [1.0],
                                      boundary=BOUNDARY_REFLECT)
        vals_cl, _, _, gc_cl2 = run(DATA, [(-1.0, 0.4)], [1.0],
                                    boundary=BOUNDARY_CLAMP)
        self.assertEqual(vals_ref, vals_cl)
        self.assertEqual(gc_ref2, gc_cl2)


class TestDegenerateCases(unittest.TestCase):
    def test_zero_length_width(self):
        data = [[1.0], [2.0], [4.0]]  # W=1
        coords = [(-0.9, 0.0), (0.0, 0.0), (0.7, 0.0)]
        values, masks, gd, gc = run(data, coords, [1.0, 1.0, 1.0])
        self.assertEqual(values, [2.0, 2.0, 2.0])  # independent of u
        self.assertEqual(masks, [1.0, 1.0, 1.0])
        self.assertEqual([g[0] for g in gc], [0.0, 0.0, 0.0])  # no u gradient
        self.assertEqual(gd, [[0.0], [3.0], [0.0]])  # all weight on row 1

    def test_zero_length_height(self):
        data = [[1.0, 2.0, 4.0]]  # H=1
        coords = [(0.0, -0.8), (0.0, 0.2), (0.0, 0.95)]
        values, _, gd, gc = run(data, coords, [1.0, 1.0, 1.0])
        self.assertEqual(values, [2.0, 2.0, 2.0])  # independent of v
        self.assertEqual([g[1] for g in gc], [0.0, 0.0, 0.0])
        self.assertEqual(gd, [[0.0, 3.0, 0.0]])

    def test_single_pixel_image(self):
        values, masks, gd, gc = run([[7.0]], [(0.3, -0.6)], [2.0])
        self.assertEqual(values, [7.0])
        self.assertEqual(masks, [1.0])
        self.assertEqual(gd, [[2.0]])
        self.assertEqual(gc, [(0.0, 0.0)])

    def test_zero_length_axis_with_zeros_boundary(self):
        # W=1 under align="centers": p = u/2, so only u=0 lands in range.
        _, masks, _, _ = run([[1.0], [2.0], [4.0]], [(0.0, 0.0), (0.5, 0.0)],
                             [1.0, 1.0], align=ALIGN_CENTERS,
                             boundary=BOUNDARY_ZEROS)
        self.assertEqual(masks, [1.0, 0.0])

    def test_full_mask_all_invalid(self):
        coords = [(5.0, 0.0), (0.0, -5.0), (9.0, 9.0), (-3.0, 2.0)]
        values, masks, gd, gc = run(DATA, coords, [1.0, 2.0, 3.0, 4.0],
                                    boundary=BOUNDARY_ZEROS)
        self.assertEqual(values, [0.0] * 4)
        self.assertEqual(masks, [0.0] * 4)
        self.assertEqual(flat(gd), [0.0] * 12)
        self.assertEqual(gc, [(0.0, 0.0)] * 4)


if __name__ == "__main__":
    unittest.main()
