"""unittest suite for the sparse chain truncator (stdlib only)."""
import os
import random
import sys
import unittest

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

from sparse_truncator import ChainTruncator, Interval, check_certificate
from sparse_truncator import dense


def make_chain():
    m0 = (3, 3, [
        (0, 0, 0.5), (0, 1, -0.2), (0, 2, 0.01),
        (1, 0, 0.3), (1, 1, 0.4), (1, 2, -0.02),
        (2, 0, -0.1), (2, 1, 0.05), (2, 2, 0.6),
    ])
    m1 = (3, 3, [
        (0, 0, 1.2), (0, 1, 0.01), (0, 2, -0.3),
        (1, 0, 0.02), (1, 1, -0.8), (1, 2, 0.4),
        (2, 0, 0.5), (2, 1, 0.03), (2, 2, 0.9),
    ])
    m2 = (2, 3, [
        (0, 0, 4.0), (0, 1, -3.5), (0, 2, 2.0),
        (1, 0, 0.1), (1, 1, 0.05), (1, 2, -6.0),
    ])
    box = [(-1.0, 1.0), (0.5, 1.5), (-2.0, -1.0)]
    return ChainTruncator([m0, m1, m2], box)


def random_chain(rng, n_layers=3, dim=3):
    mats = []
    for _ in range(n_layers):
        entries = []
        for r in range(dim):
            for c in range(dim):
                if rng.random() < 0.8:
                    entries.append((r, c, rng.uniform(-2.0, 2.0)))
        mats.append((dim, dim, entries))
    box = [(rng.uniform(-2.0, -0.1), rng.uniform(0.1, 2.0))
           for _ in range(dim)]
    return ChainTruncator(mats, box)


class TestInterval(unittest.TestCase):
    def test_negative_scalar_flips_endpoints(self):
        iv = Interval(-2.0, 3.0)
        self.assertEqual(iv.scale(-1.5), Interval(-4.5, 3.0))
        self.assertEqual(iv.scale(2.0), Interval(-4.0, 6.0))

    def test_magnitude_and_width(self):
        self.assertEqual(Interval(-5.0, 2.0).magnitude(), 5.0)
        self.assertEqual(Interval(-5.0, 2.0).width(), 7.0)

    def test_empty_interval_rejected(self):
        with self.assertRaises(ValueError):
            Interval(1.0, -1.0)


class TestDense(unittest.TestCase):
    def test_matvec_matmul(self):
        A = [[1.0, -2.0], [0.5, 3.0]]
        B = [[2.0, 0.0], [-1.0, 4.0]]
        self.assertEqual(dense.matvec(A, [1.0, 1.0]), [-1.0, 3.5])
        self.assertEqual(dense.matmul(A, B), [[4.0, -8.0], [-2.0, 12.0]])

    def test_from_entries_explicit_shape(self):
        A = dense.from_entries(2, 3, {(0, 2): 5.0, (1, 0): -1.0})
        self.assertEqual(A, [[0.0, 0.0, 5.0], [-1.0, 0.0, 0.0]])
        with self.assertRaises(IndexError):
            dense.from_entries(2, 2, {(2, 0): 1.0})


class TestCertificate(unittest.TestCase):
    def test_exact_chain_has_zero_bound(self):
        chain = make_chain()
        cert = chain.certificate()
        self.assertEqual(cert.linf, 0.0)
        self.assertTrue(all(lc.dropped_nnz == 0 for lc in cert.layers))

    def test_certificate_covers_true_deviation(self):
        chain = make_chain()
        result = chain.allocate(error_budget=0.5)
        self.assertTrue(result.satisfied)
        self.assertLessEqual(result.certificate.linf, 0.5 + 1e-12)
        report = check_certificate(chain)
        self.assertTrue(report.ok)
        self.assertEqual(report.num_vertices, 2 ** 3)
        self.assertLessEqual(report.observed_linf,
                             report.certified_linf + 1e-9)

    def test_randomized_soundness(self):
        rng = random.Random(20260925)
        for trial in range(30):
            chain = random_chain(rng)
            budget = rng.uniform(0.05, 2.0)
            chain.allocate(error_budget=budget)
            report = check_certificate(chain)
            self.assertTrue(
                report.ok,
                f"trial {trial}: observed {report.observed_linf} exceeds "
                f"certified {report.certified_linf}")

    def test_amplification_is_propagated_not_reused(self):
        # One tiny dropped entry in layer 0, huge norm in the last layer.
        m0 = (2, 2, [(0, 0, 1.0), (0, 1, 0.01), (1, 1, 1.0)])
        m1 = (1, 2, [(0, 0, 100.0), (0, 1, 100.0)])
        chain = ChainTruncator([m0, m1], [(1.0, 1.0), (1.0, 1.0)])
        chain.drop(0, 0, 1)  # local error only 0.01
        cert = chain.certificate()
        local = 0.01
        self.assertGreater(cert.linf, 50 * local)  # amplified, not reused
        report = check_certificate(chain)
        self.assertTrue(report.ok)
        self.assertAlmostEqual(report.observed_linf, 1.0)  # 100 * 0.01

    def test_input_uncertainty_propagates(self):
        m = (1, 1, [(0, 0, 2.0)])
        wide = ChainTruncator([m], [(-3.0, 3.0)])
        narrow = ChainTruncator([m], [(0.0, 1.0)])
        wide.drop(0, 0, 0)
        narrow.drop(0, 0, 0)
        self.assertAlmostEqual(wide.certificate().linf, 6.0)
        self.assertAlmostEqual(narrow.certificate().linf, 2.0)

    def test_negative_coefficients_in_ranges(self):
        # z1 = -2 * x0 with x0 in [1, 2] -> z1 in [-4, -2]; dropping the
        # next layer's unit entry must see magnitude 4, not a signed value.
        m0 = (1, 1, [(0, 0, -2.0)])
        m1 = (1, 1, [(0, 0, 1.0)])
        chain = ChainTruncator([m0, m1], [(1.0, 2.0)])
        chain.drop(1, 0, 0)
        self.assertAlmostEqual(chain.certificate().linf, 4.0)
        self.assertTrue(check_certificate(chain).ok)


class TestGreedyAllocation(unittest.TestCase):
    def test_stable_coordinate_tie_break(self):
        # Equal magnitudes everywhere: order must be deterministic and
        # decided by stable coordinates (layer, row, col).
        entries = [(r, c, 1.0) for r in range(2) for c in range(2)]
        chain = ChainTruncator([(2, 2, entries)], [(1.0, 1.0), (1.0, 1.0)])
        deltas = chain.marginal_deltas()
        keys = [(i, r, c) for _d, i, r, c in deltas]
        self.assertEqual(keys, sorted(keys))
        self.assertEqual(len({d for d, _i, _r, _c in deltas}), 1)  # all tied

    def test_thresholds_and_nnz_reported_per_layer(self):
        chain = make_chain()
        result = chain.allocate(error_budget=0.5)
        self.assertTrue(result.satisfied)
        for lc in result.certificate.layers:
            if lc.dropped_nnz:
                kept = chain.kept_entries(lc.layer)
                self.assertAlmostEqual(
                    lc.threshold, min(abs(v) for v in kept.values()))
        self.assertEqual(result.total_nnz,
                         sum(lc.kept_nnz for lc in result.certificate.layers))

    def test_unsatisfiable_nnz_budget_keeps_exact_part(self):
        chain = make_chain()
        total = chain.total_nnz()
        result = chain.allocate(error_budget=0.05, max_total_nnz=2)
        self.assertFalse(result.satisfied)
        self.assertEqual(chain.total_nnz(), total)       # rolled back
        self.assertEqual(chain.certificate().linf, 0.0)  # exact part kept
        self.assertIn("nonzero budget", result.message)

    def test_negative_budget_unsatisfiable(self):
        chain = make_chain()
        result = chain.allocate(error_budget=-1.0)
        self.assertFalse(result.satisfied)


class TestUndoAndInputUpdate(unittest.TestCase):
    def test_undo_restores_matrix_and_ledger(self):
        chain = make_chain()
        before = chain.certificate().linf
        chain.drop(0, 0, 0)
        chain.drop(2, 1, 2)
        self.assertGreater(chain.certificate().linf, before)
        chain.undo()
        chain.undo()
        self.assertEqual(chain.certificate().linf, before)
        self.assertEqual(chain.total_nnz(),
                         sum(len(chain.original_entries(i)) for i in range(3)))

    def test_reset_restores_everything(self):
        chain = make_chain()
        chain.allocate(error_budget=0.3)
        self.assertLess(chain.total_nnz(), 25)
        chain.reset()
        self.assertEqual(chain.certificate().linf, 0.0)
        self.assertTrue(check_certificate(chain).ok)

    def test_local_input_update_incremental_and_revocable(self):
        chain = make_chain()
        chain.allocate(error_budget=0.5)
        before = chain.certificate()
        self.assertEqual(before.input_box[0], Interval(-1.0, 1.0))
        chain.set_input_interval(0, -0.5, 0.75)   # local modification
        updated = chain.certificate()
        self.assertEqual(updated.input_box[0], Interval(-0.5, 0.75))
        self.assertNotEqual(updated.linf, before.linf)
        self.assertTrue(check_certificate(chain).ok)  # still sound
        chain.undo()                                   # revoke the edit
        self.assertEqual(chain.certificate().input_box[0],
                         Interval(-1.0, 1.0))

    def test_undo_empty_raises(self):
        with self.assertRaises(IndexError):
            make_chain().undo()


class TestValidation(unittest.TestCase):
    def test_dimension_mismatch_rejected(self):
        with self.assertRaises(ValueError):
            ChainTruncator([(2, 3, []), (3, 3, [])], [(0.0, 1.0)] * 3)

    def test_box_dimension_mismatch_rejected(self):
        with self.assertRaises(ValueError):
            ChainTruncator([(2, 3, [])], [(0.0, 1.0)] * 2)

    def test_index_out_of_range_rejected(self):
        with self.assertRaises(IndexError):
            ChainTruncator([(2, 2, [(2, 0, 1.0)])], [(0.0, 1.0)] * 2)

    def test_double_drop_rejected(self):
        chain = make_chain()
        chain.drop(0, 0, 0)
        with self.assertRaises(KeyError):
            chain.drop(0, 0, 0)


if __name__ == "__main__":
    unittest.main()
