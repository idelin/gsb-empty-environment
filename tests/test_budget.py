"""Symbol budget: exceeding it yields an incomplete certificate."""

import unittest

from affine_dag import DAG, build_certificate, verify_certificate


class TestBudget(unittest.TestCase):
    def test_budget_exceeded_returns_incomplete(self):
        dag = DAG(max_terms=1)
        x = dag.var(0.0, 2.0, "x")
        y = dag.var(0.0, 2.0, "y")
        m = dag.mul(x, y, "m")  # form has 3 noise symbols > budget 1
        res = dag.evaluate(m)
        self.assertFalse(res.complete)
        self.assertTrue(any("budget exceeded" in e for e in res.errors))

        cert = build_certificate(dag, [m], [res])
        self.assertFalse(cert["complete"])
        vr = verify_certificate(cert)
        self.assertFalse(vr.ok)
        self.assertFalse(vr.complete)
        self.assertTrue(any("incomplete" in r for r in vr.reasons))
        self.assertTrue(any("budget" in r for r in vr.reasons))

    def test_within_budget_is_complete(self):
        dag = DAG(max_terms=3)
        x = dag.var(-1.0, 1.0)
        y = dag.var(-1.0, 1.0)
        m = dag.mul(x, y)
        res = dag.evaluate(m)
        self.assertTrue(res.complete)
        cert = build_certificate(dag, [m], [res])
        self.assertTrue(verify_certificate(cert).ok)


if __name__ == "__main__":
    unittest.main()
