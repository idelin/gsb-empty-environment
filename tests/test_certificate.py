import unittest

from sparse_trunc import (
    SparseMatrix,
    TruncatedChain,
    check_corners,
    dense_matmul,
    dense_matvec,
    sparse_to_dense,
)


def make_amplification_chain():
    # Tiny layer-0 truncation (0.01) is amplified x100 by layer 1.
    a0 = SparseMatrix(
        2, 2,
        [(0, 0, 1.0), (0, 1, -0.01), (1, 0, 0.0), (1, 1, 1.0)])
    a1 = SparseMatrix(
        2, 2,
        [(0, 0, 100.0), (0, 1, 0.0), (1, 0, 0.0), (1, 1, 1.0)])
    return TruncatedChain([a0, a1], [-1.0, -1.0], [1.0, 1.0])


class CertificateTests(unittest.TestCase):

    def test_explicit_dimensions_and_indices(self):
        with self.assertRaises(ValueError):
            SparseMatrix(0, 2, [(0, 0, 1.0)])
        with self.assertRaises(ValueError):
            SparseMatrix(2, 2, [(2, 0, 1.0)])
        with self.assertRaises(ValueError):
            SparseMatrix(2, 2, [(0, -1, 1.0)])
        chain = make_amplification_chain()
        with self.assertRaises(ValueError):
            TruncatedChain(chain.layers, [-1.0], [1.0])
        with self.assertRaises(ValueError):
            TruncatedChain([SparseMatrix(2, 3, [])], [0.0, 0.0],
                           [1.0, 1.0])

    def test_negative_coefficients_flip_interval_endpoints(self):
        mat = SparseMatrix(
            2, 2,
            [(0, 0, -2.0), (0, 1, 3.0), (1, 0, 1.0), (1, 1, -1.0)])
        chain = TruncatedChain([mat], [1.0, 1.0], [2.0, 2.0])
        lo, hi, bound = chain.exact_propagation()
        self.assertEqual(lo[-1], [-1.0, -1.0])
        self.assertEqual(hi[-1], [4.0, 1.0])
        self.assertEqual(bound[1], [4.0, 1.0])

    def test_later_large_norm_amplifies_early_truncation(self):
        chain = make_amplification_chain()
        cert = chain.schedule_truncate(2.0, [2, 4])
        self.assertTrue(cert.feasible)
        self.assertAlmostEqual(cert.final_linf_bound, 1.0)
        check = check_corners(chain, cert.final_linf_bound)
        self.assertTrue(check.bound_holds)
        # Local injected error is 0.01; final certificate is x100 of it.
        self.assertAlmostEqual(cert.layers[0].injected_linf, 0.01)
        self.assertAlmostEqual(cert.layers[0].final_contribution_linf, 1.0)

    def test_independent_layer_budgets_do_not_imply_chain_budget(self):
        # Dropping the -0.01 entry costs only 0.01 locally at layer 0 and
        # nothing at layer 1; independent "each layer < 0.5" would accept it.
        # The global final deviation is 1.0, so a 0.5 global budget rejects it.
        chain = make_amplification_chain()
        cert = chain.schedule_truncate(0.5, [2, 4])
        self.assertFalse(cert.feasible)
        self.assertTrue(cert.within_budget)
        self.assertFalse(cert.within_caps)
        self.assertEqual(cert.unsatisfied_layers, [0])
        # Exact part retained: no truncation, bound is zero.
        self.assertEqual(cert.final_linf_bound, 0.0)
        self.assertEqual(chain.layers[0].dropped, {})

    def test_corner_enumeration_never_uses_observed_error_as_bound(self):
        chain = make_amplification_chain()
        chain.schedule_truncate(2.0, [2, 4])
        check = check_corners(chain, 2.0)
        self.assertEqual(check.points, 4)
        self.assertTrue(check.bound_holds)
        self.assertLessEqual(check.max_observed_linf, 2.0)
        # An artificially tightened claimed bound must be exposed as violated.
        bad = check_corners(chain, check.max_observed_linf - 0.1)
        self.assertFalse(bad.bound_holds)

    def test_equal_absolute_values_use_stable_coordinate_order(self):
        mat = SparseMatrix(
            2, 2, [(0, 1, 0.5), (1, 0, 0.5), (1, 1, 0.5)])
        chain = TruncatedChain([mat], [0.0, 0.0], [1.0, 1.0])
        cert = chain.schedule_truncate(10.0, [1])
        self.assertEqual(cert.layers[0].kept_nnz, 1)
        # (|v|, row, col) ascending: drop (0,1) then (1,0); keep (1,1).
        self.assertEqual(set(chain.layers[0].dropped),
                         {(0, 1), (1, 0)})

    def test_threshold_reports_max_dropped_magnitude(self):
        mat = SparseMatrix(
            1, 3, [(0, 0, 0.1), (0, 1, 0.4), (0, 2, 0.2)])
        chain = TruncatedChain([mat], [0.0, 0.0, 0.0], [1.0, 1.0, 1.0])
        cert = chain.schedule_truncate(10.0, [1])
        self.assertEqual(cert.layers[0].threshold, 0.2)
        self.assertEqual(set(chain.layers[0].dropped),
                         {(0, 0), (0, 2)})

    def test_incremental_input_update_refreshes_certificate(self):
        chain = make_amplification_chain()
        chain.schedule_truncate(2.0, [2, 4])
        chain.update_input([0.5, 0.5], [0.5, 0.5])
        cert = chain.certify(2.0, [2, 4])
        self.assertAlmostEqual(cert.final_linf_bound, 0.5)
        check = check_corners(chain, cert.final_linf_bound)
        self.assertTrue(check.bound_holds)
        chain.undo()
        self.assertEqual(chain.input_lo, [-1.0, -1.0])

    def test_revocable_approximation_and_rollback(self):
        chain = make_amplification_chain()
        chain.schedule_truncate(2.0, [2, 4])
        self.assertEqual(set(chain.layers[0].dropped), {(0, 1)})
        chain.snapshot("s")
        chain.restore_entry(0, 0, 1)
        chain.edit_exact_entry(0, 0, 1, -0.09)
        self.assertEqual(chain.layers[0].exact_get(0, 1), -0.09)
        chain.rollback("s")
        self.assertEqual(chain.layers[0].dropped[(0, 1)], -0.01)
        self.assertNotIn((0, 1), chain.layers[0].entries)
        # undo() before the snapshot revokes the schedule itself.
        chain.undo()
        self.assertEqual(chain.layers[0].dropped, {})
        self.assertEqual(chain.layers[0].kept_nnz, 3)

    def test_local_matrix_edit_increments_and_undo_restores(self):
        chain = make_amplification_chain()
        chain.edit_exact_entry(0, 0, 0, 2.0)
        self.assertEqual(chain.layers[0].get(0, 0), 2.0)
        chain.undo()
        self.assertEqual(chain.layers[0].get(0, 0), 1.0)
        # Editing a currently dropped entry changes the hidden exact value.
        chain.schedule_truncate(2.0, [2, 4])
        chain.edit_exact_entry(0, 0, 1, -0.07)
        self.assertEqual(chain.layers[0].dropped[(0, 1)], -0.07)
        chain.undo()
        self.assertEqual(chain.layers[0].dropped[(0, 1)], -0.01)

    def test_unsatisfiable_keeps_exact_and_reports(self):
        mat = SparseMatrix(1, 1, [(0, 0, 5.0)])
        chain = TruncatedChain([mat], [1.0], [1.0])
        cert = chain.schedule_truncate(1.0, [0])
        self.assertFalse(cert.feasible)
        self.assertEqual(cert.unsatisfied_layers, [0])
        self.assertEqual(chain.layers[0].kept_nnz, 1)
        self.assertEqual(cert.final_linf_bound, 0.0)

    def test_certificate_is_conservative_under_error_coupling(self):
        # z1 = [x, x], z2 = x - x = 0 exactly.  Truncating both A1 entries
        # adds independent |x| terms that cannot cancel; the bound is 2 while
        # the observed corner error is 0.
        a0 = SparseMatrix(2, 1, [(0, 0, 1.0), (1, 0, 1.0)])
        a1 = SparseMatrix(1, 2, [(0, 0, 1.0), (0, 1, -1.0)])
        chain = TruncatedChain([a0, a1], [-1.0], [1.0])
        cert = chain.schedule_truncate(10.0, [None, 0])
        check = check_corners(chain, cert.final_linf_bound)
        self.assertTrue(check.bound_holds)
        self.assertAlmostEqual(cert.final_linf_bound, 2.0)
        self.assertLess(check.max_observed_linf, 1e-12)

    def test_dense_cross_check_of_exact_and_approx_outputs(self):
        chain = make_amplification_chain()
        cert = chain.schedule_truncate(2.0, [2, 4])
        point = [0.37, -0.61]
        exact_dense = point
        for mat in chain._exact_matrices():
            exact_dense = dense_matvec(sparse_to_dense(mat), exact_dense)
        approx_dense = point
        for mat in chain.layers:
            approx_dense = dense_matvec(sparse_to_dense(mat), approx_dense)
        exact_sparse = chain.exact_outputs([point])[0]
        approx_sparse = chain.approximate_outputs([point])[0]
        for a, b in zip(exact_dense, exact_sparse):
            self.assertAlmostEqual(a, b)
        for a, b in zip(approx_dense, approx_sparse):
            self.assertAlmostEqual(a, b)
        deviation = max(abs(a - b)
                        for a, b in zip(exact_sparse, approx_sparse))
        self.assertLessEqual(deviation, cert.final_linf_bound + 1e-12)

    def test_dense_matmul_helpers(self):
        a = [[1.0, 2.0], [3.0, 4.0]]
        b = [[2.0, 0.0], [1.0, 2.0]]
        self.assertEqual(dense_matmul(a, b),
                         [[4.0, 4.0], [10.0, 8.0]])
        self.assertEqual(dense_matvec(a, [1.0, 1.0]), [3.0, 7.0])

    def test_total_nnz_cap(self):
        m0 = SparseMatrix(1, 2, [(0, 0, 0.1), (0, 1, 0.2)])
        m1 = SparseMatrix(1, 1, [(0, 0, 0.3)])
        chain = TruncatedChain([m0, m1], [1.0, 1.0], [1.0, 1.0])
        cert = chain.schedule_truncate(10.0, [None, None], total_cap=1)
        self.assertTrue(cert.feasible)
        self.assertEqual(cert.total_kept_nnz, 1)

    def test_structural_zeros_are_not_candidates(self):
        mat = SparseMatrix(2, 2, [(0, 0, 1.0), (1, 1, 0.0)])
        self.assertEqual(mat.exact_nnz, 1)
        chain = TruncatedChain([mat], [1.0, 1.0], [1.0, 1.0])
        cert = chain.schedule_truncate(10.0, [0])
        self.assertTrue(cert.feasible)
        self.assertEqual(cert.layers[0].kept_nnz, 0)
        self.assertEqual(set(chain.layers[0].dropped), {(0, 0)})
        self.assertEqual(cert.layers[0].exact_nnz, 1)

    def test_multi_layer_corner_bound_holds(self):
        a0 = SparseMatrix(2, 2,
                          [(0, 0, 0.6), (0, 1, -0.3),
                           (1, 0, 0.2), (1, 1, 0.9)])
        a1 = SparseMatrix(2, 2,
                          [(0, 0, -0.5), (0, 1, 0.8),
                           (1, 0, 0.4), (1, 1, -0.2)])
        a2 = SparseMatrix(1, 2, [(0, 0, 1.2), (0, 1, -0.7)])
        chain = TruncatedChain([a0, a1, a2],
                               [-1.0, -1.0], [1.0, 1.0])
        cert = chain.schedule_truncate(0.3, [3, 3, 1])
        check = check_corners(chain, cert.final_linf_bound)
        self.assertTrue(check.bound_holds)
        self.assertEqual(check.points, 4)


if __name__ == "__main__":
    unittest.main(verbosity=2)
