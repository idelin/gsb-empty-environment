"""Division by an interval containing zero is an explicit, handled error."""

import unittest

from affine_dag import DAG, build_certificate, verify_certificate


class TestDivisionByZeroInterval(unittest.TestCase):
    def test_divisor_crossing_zero(self):
        dag = DAG()
        a = dag.var(1.0, 2.0, "a")
        b = dag.var(-1.0, 1.0, "b")  # straddles 0
        q = dag.div(a, b, "q")
        res = dag.evaluate(q)
        self.assertFalse(res.complete)
        self.assertIsNone(res.interval)
        self.assertTrue(any("contains 0" in e for e in res.errors))
        # The downstream mul node cannot be evaluated either.
        self.assertTrue(any("upstream failure" in e for e in res.errors))

        cert = build_certificate(dag, [q], [res])
        self.assertFalse(cert["complete"])
        vr = verify_certificate(cert)
        self.assertFalse(vr.ok)
        self.assertTrue(any("contains 0" in r for r in vr.reasons))

    def test_safe_divisor_is_fine(self):
        dag = DAG()
        a = dag.var(1.0, 2.0)
        b = dag.var(2.0, 4.0)
        q = dag.div(a, b)
        res = dag.evaluate(q)
        self.assertTrue(res.complete)
        lo, hi = res.interval
        self.assertLessEqual(lo, 0.25 + 1e-12)
        self.assertGreaterEqual(hi, 1.0 - 1e-12)


if __name__ == "__main__":
    unittest.main()
