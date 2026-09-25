"""Runnable tests for the error-propagation DAG (stdlib unittest only)."""

import copy
import unittest

from errordag import (
    BudgetExceeded,
    CertStatus,
    DAG,
    DenseAffineForm,
    DivisionAcrossZero,
    verify,
)
from errordag import bounds


def concrete_run(dag, assignment):
    """Evaluate the DAG with plain floats (independent concrete interpreter)."""
    vals = {}
    for node in dag.nodes:
        p = node.params
        if node.op == "var":
            vals[node.nid] = assignment[node.nid]
        elif node.op == "const":
            vals[node.nid] = p["value"]
        elif node.op == "add":
            vals[node.nid] = vals[node.inputs[0]] + vals[node.inputs[1]]
        elif node.op == "sub":
            vals[node.nid] = vals[node.inputs[0]] - vals[node.inputs[1]]
        elif node.op == "scale":
            vals[node.nid] = vals[node.inputs[0]] * p["factor"]
        elif node.op == "mul":
            vals[node.nid] = vals[node.inputs[0]] * vals[node.inputs[1]]
        elif node.op == "recip":
            vals[node.nid] = 1.0 / vals[node.inputs[0]]
        elif node.op == "div":
            vals[node.nid] = vals[node.inputs[0]] / vals[node.inputs[1]]
        elif node.op == "piecewise":
            vals[node.nid] = bounds.piecewise_linear_eval(
                p["breakpoints"], p["values"], p["left_slope"],
                p["right_slope"], vals[node.inputs[0]])
        else:
            raise AssertionError(node.op)
    return vals


def grid(lo, hi, n):
    if n == 1:
        return [(lo + hi) / 2.0]
    return [lo + (hi - lo) * i / (n - 1) for i in range(n)]


class CorrelationTests(unittest.TestCase):
    def test_x_minus_x_is_exact_zero(self):
        dag = DAG()
        x = dag.var(-1.0, 1.0, "x")
        d = dag.sub(x, x)
        self.assertTrue(dag.evaluate())
        self.assertTrue(d.form.is_exact_zero())
        self.assertEqual(d.interval(), (0.0, 0.0))  # not [-2, 2]

    def test_independent_vars_same_interval_do_not_cancel(self):
        dag = DAG()
        x = dag.var(-1.0, 1.0, "x")
        y = dag.var(-1.0, 1.0, "y")
        d = dag.sub(x, y)
        self.assertTrue(dag.evaluate())
        self.assertEqual(d.interval(), (-2.0, 2.0))
        self.assertEqual(len(d.form.coeffs), 2)  # two independent symbols


class PiecewiseTests(unittest.TestCase):
    def test_abs_crossing_kink_is_not_endpoint_only(self):
        dag = DAG()
        x = dag.var(-2.0, 1.0, "x")
        y = dag.abs(x)
        self.assertTrue(dag.evaluate())
        lo, hi = y.interval()
        # Endpoint-only reasoning would give [1, 2]; the kink at 0 forces
        # the certified range to include 0 and reach 2.
        self.assertLessEqual(lo, 0.0)
        self.assertGreaterEqual(hi, 2.0)
        # Sanity: the true range [0, 2] must be contained.
        self.assertLessEqual(lo, 0.0 + 1e-12)

    def test_piecewise_without_interior_kink_is_exact(self):
        dag = DAG()
        x = dag.var(1.0, 2.0, "x")
        y = dag.abs(x)  # no breakpoint inside [1, 2] -> plain linear map
        self.assertTrue(dag.evaluate())
        self.assertEqual(y.interval(), (1.0, 2.0))
        self.assertEqual(len(dag.space.names), 1)  # no remainder symbol

    def test_piecewise_envelope_sound_by_enumeration(self):
        dag = DAG()
        x = dag.var(-2.0, 1.0, "x")
        y = dag.abs(x)
        self.assertTrue(dag.evaluate())
        lo, hi = y.interval()
        for xv in grid(-2.0, 1.0, 41):
            fv = abs(xv)
            self.assertLessEqual(lo, fv)
            self.assertLessEqual(fv, hi)


class IncrementalTests(unittest.TestCase):
    def _build(self):
        dag = DAG()
        x = dag.var(-1.0, 1.0, "x")
        y = dag.var(0.0, 2.0, "y")
        sx = dag.scale(x, 2.0)                           # depends on x
        a = dag.add(sx, dag.const(1.0))                  # depends on x
        b = dag.scale(y, 3.0)                            # depends on y only
        c = dag.add(a, b)                                # depends on both
        self.assertTrue(dag.evaluate())
        return dag, x, y, sx, a, b, c

    def test_incremental_update_touches_only_affected_successors(self):
        dag, x, y, sx, a, b, c = self._build()
        affected = dag.update_var(x, -0.5, 1.5)
        self.assertEqual(affected, {x.nid, sx.nid, a.nid, c.nid})
        self.assertTrue(dag.evaluate())
        self.assertEqual(set(dag.recomputed), {x.nid, sx.nid, a.nid, c.nid})
        self.assertNotIn(b.nid, dag.recomputed)  # untouched branch
        self.assertEqual(b.interval(), (0.0, 6.0))  # stale-free, unchanged
        self.assertEqual(a.interval(), (0.0, 4.0))   # 2*[-0.5,1.5]+1

    def test_incremental_update_invalidates_only_affected_certificates(self):
        dag, x, y, sx, a, b, c = self._build()
        cert_c = dag.certificate([c])
        cert_b = dag.certificate([b])
        self.assertIs(cert_c.status, CertStatus.COMPLETE)
        dag.update_var(x, -0.5, 1.5)
        # Certificate covering the changed subgraph is downgraded...
        self.assertIs(cert_c.status, CertStatus.INCOMPLETE)
        self.assertIn("incremental update", cert_c.reason)
        # ...while the disjoint branch's certificate stays complete.
        self.assertIs(cert_b.status, CertStatus.COMPLETE)
        self.assertTrue(verify(cert_b).ok)
        self.assertTrue(dag.evaluate())
        self.assertFalse(verify(cert_c, dag).ok)  # stale version
        fresh = dag.certificate([c])
        self.assertTrue(verify(fresh, dag).ok)


class CertificateTests(unittest.TestCase):
    def _chain(self):
        dag = DAG()
        x = dag.var(-1.0, 1.0, "x")
        y = dag.var(-0.5, 0.5, "y")
        num = dag.add(dag.mul(x, y), x)
        den = dag.add(x, dag.const(3.0))
        w = dag.div(num, den)
        z = dag.abs(w)
        self.assertTrue(dag.evaluate())
        return dag, x, y, z

    def test_valid_certificate_verifies(self):
        dag, x, y, z = self._chain()
        cert = dag.certificate([z])
        self.assertIs(cert.status, CertStatus.COMPLETE)
        result = verify(cert, dag)
        self.assertTrue(result.ok, result.errors)

    def test_enumeration_full_chain_containment(self):
        dag, x, y, z = self._chain()
        cert = dag.certificate([z])
        self.assertTrue(verify(cert, dag).ok)
        recorded = {r.nid: (r.lo, r.hi) for r in cert.records}
        for xv in grid(-1.0, 1.0, 11):
            for yv in grid(-0.5, 0.5, 11):
                vals = concrete_run(dag, {x.nid: xv, y.nid: yv})
                for nid, value in vals.items():
                    lo, hi = recorded[nid]
                    self.assertLessEqual(lo - 1e-12, value,
                                         f"node {nid} below range at {value}")
                    self.assertLessEqual(value, hi + 1e-12,
                                         f"node {nid} above range at {value}")

    def test_bad_certificate_rejected(self):
        dag, x, y, z = self._chain()
        cert = dag.certificate([z])
        # Tamper: shrink the output interval so it no longer contains truth.
        bad = copy.deepcopy(cert)
        rec = next(r for r in bad.records if r.nid == z.nid)
        rec.lo, rec.hi = rec.lo + 0.05, rec.hi - 0.05
        result = verify(bad, dag)
        self.assertFalse(result.ok)
        self.assertTrue(any("interval" in e for e in result.errors))
        # Tamper: corrupt a coefficient.
        bad2 = copy.deepcopy(cert)
        bad2.records[-1].center += 0.1
        result2 = verify(bad2, dag)
        self.assertFalse(result2.ok)
        self.assertTrue(any("center" in e for e in result2.errors))

    def test_incomplete_certificate_not_verifiable(self):
        dag = DAG()
        x = dag.var(-1.0, 1.0, "x")
        y = dag.var(-1.0, 1.0, "y")
        w = dag.div(x, y)  # divisor interval contains 0
        self.assertFalse(dag.evaluate())
        cert = dag.certificate([w])
        self.assertIs(cert.status, CertStatus.INCOMPLETE)
        result = verify(cert, dag)
        self.assertFalse(result.ok)
        self.assertIn("incomplete", result.errors[0])


class DivisionTests(unittest.TestCase):
    def test_divisor_crossing_zero_explicitly_handled(self):
        dag = DAG()
        x = dag.var(1.0, 2.0, "x")
        y = dag.var(-1.0, 1.0, "y")  # crosses 0
        w = dag.div(x, y)
        self.assertFalse(dag.evaluate())
        self.assertIsInstance(dag.error, DivisionAcrossZero)
        cert = dag.certificate([w])
        self.assertIs(cert.status, CertStatus.INCOMPLETE)
        self.assertIn("contains 0", cert.reason)

    def test_divisor_away_from_zero_is_sound(self):
        dag = DAG()
        x = dag.var(1.0, 2.0, "x")
        y = dag.var(0.5, 1.5, "y")
        w = dag.div(x, y)
        self.assertTrue(dag.evaluate())
        lo, hi = w.interval()
        for xv in grid(1.0, 2.0, 9):
            for yv in grid(0.5, 1.5, 9):
                self.assertLessEqual(lo, xv / yv)
                self.assertLessEqual(xv / yv, hi)


class BudgetTests(unittest.TestCase):
    def test_symbol_budget_exceeded_returns_incomplete_certificate(self):
        dag = DAG(max_symbols=2)
        x = dag.var(-1.0, 1.0, "x")   # symbol 0
        y = dag.var(-1.0, 1.0, "y")   # symbol 1
        m = dag.mul(x, y)             # needs a remainder symbol -> over budget
        self.assertFalse(dag.evaluate())
        self.assertIsInstance(dag.error, BudgetExceeded)
        cert = dag.certificate([m])
        self.assertIs(cert.status, CertStatus.INCOMPLETE)
        self.assertIn("budget", cert.reason)
        self.assertFalse(verify(cert).ok)

    def test_budget_sufficient_completes(self):
        dag = DAG(max_symbols=3)
        x = dag.var(-1.0, 1.0, "x")
        y = dag.var(-1.0, 1.0, "y")
        m = dag.mul(x, y)
        self.assertTrue(dag.evaluate())
        cert = dag.certificate([m])
        self.assertIs(cert.status, CertStatus.COMPLETE)
        self.assertTrue(verify(cert, dag).ok)


class DenseCrossCheckTests(unittest.TestCase):
    def test_dense_matches_sparse_on_small_chain(self):
        dag = DAG()
        x = dag.var(-1.0, 1.0, "x")
        y = dag.var(-0.5, 0.5, "y")
        m = dag.mul(x, y)
        s = dag.sub(m, x)
        self.assertTrue(dag.evaluate())
        size = len(dag.space.names)
        for node in (x, y, m, s):
            dense = DenseAffineForm.from_sparse(size, node.form)
            self.assertEqual(dense.size, size)
            back = dense.to_sparse()
            self.assertEqual(back.center, node.form.center)
            self.assertEqual(back.coeffs, node.form.coeffs)
            self.assertEqual(dense.interval(), node.form.interval())

    def test_dense_explicit_size_enforced(self):
        with self.assertRaises(ValueError):
            DenseAffineForm(2, 0.0, [1.0, 2.0, 3.0])
        with self.assertRaises(IndexError):
            DenseAffineForm.from_sparse(1, DAG().var(0, 1).form
                                        if False else _one_symbol_form(5))


def _one_symbol_form(idx):
    from errordag import AffineForm
    return AffineForm(0.0, {idx: 1.0})


if __name__ == "__main__":
    unittest.main()
