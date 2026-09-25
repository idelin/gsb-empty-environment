import random
import unittest

from sparse_trunc import SparseMatrix, TruncatedChain, check_corners


class FuzzCertificateTests(unittest.TestCase):

    def test_random_small_chains_certificate_always_covers_deviation(self):
        rng = random.Random(20260925)
        cases = 0
        for _ in range(60):
            depth = rng.randint(1, 3)
            dims = [rng.randint(1, 3)]
            for _ in range(depth):
                dims.append(rng.randint(1, 3))
            layers = []
            for l in range(depth):
                rows, cols = dims[l + 1], dims[l]
                entries = []
                for r in range(rows):
                    for c in range(cols):
                        if rng.random() < 0.7:
                            v = rng.uniform(-2.0, 2.0)
                            entries.append((r, c, v))
                layers.append(SparseMatrix(rows, cols, entries, f"A{l}"))
            dim0 = dims[0]
            lo = [rng.uniform(-1.5, 0.0) for _ in range(dim0)]
            hi = [x + rng.uniform(0.0, 1.5) for x in lo]
            chain = TruncatedChain(layers, lo, hi)
            caps = []
            for mat in chain.layers:
                if rng.random() < 0.5:
                    caps.append(rng.randint(0, mat.exact_nnz))
                else:
                    caps.append(None)
            budget = rng.choice([0.0, 0.05, 0.2, 1.0, 10.0])
            cert = chain.schedule_truncate(budget, caps)
            check = check_corners(chain, cert.final_linf_bound)
            self.assertTrue(
                check.bound_holds,
                msg=f"bound violated case {cases}: "
                    f"bound={cert.final_linf_bound} "
                    f"observed={check.max_observed_linf}")
            if cert.feasible:
                for l, cap in enumerate(caps):
                    if cap is not None:
                        self.assertLessEqual(
                            cert.layers[l].kept_nnz, cap)
            cases += 1
        self.assertEqual(cases, 60)


if __name__ == "__main__":
    unittest.main(verbosity=2)
