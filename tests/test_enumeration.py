"""Small-domain enumeration: certified intervals contain the full chain."""

import unittest

from affine_dag import DAG, abs_pl, build_certificate, verify_certificate
from affine_dag.dense import matvec


def grid(lo, hi, n):
    return [lo + (hi - lo) * k / (n - 1) for k in range(n)]


class TestEnumeration(unittest.TestCase):
    def test_full_chain_containment(self):
        dag = DAG()
        x = dag.var(0.5, 2.0, "x")
        y = dag.var(-1.5, 1.0, "y")
        t = dag.mul(x, y)
        u = dag.add(t, x)                       # u = x*(y+1), crosses 0
        den = dag.add(x, dag.const(3.0))
        w = dag.div(u, den)                     # w = u / (x + 3)
        p = dag.piecewise(u, abs_pl())          # kink at 0 inside u's range
        out = dag.sub(p, dag.scale(x, 0.3))     # out = |u| - 0.3 x
        res_w = dag.evaluate(w)
        res_out = dag.evaluate(out)
        self.assertTrue(res_w.complete and res_out.complete)

        lo_w, hi_w = res_w.interval
        lo_o, hi_o = res_out.interval
        tol = 1e-12
        samples = 0
        for xv in grid(0.5, 2.0, 26):
            for yv in grid(-1.5, 1.0, 26):
                uv = xv * yv + xv
                wv = uv / (xv + 3.0)
                ov = abs(uv) - 0.3 * xv
                self.assertLessEqual(lo_w - tol, wv)
                self.assertLessEqual(wv, hi_w + tol)
                self.assertLessEqual(lo_o - tol, ov)
                self.assertLessEqual(ov, hi_o + tol)
                samples += 1
        self.assertEqual(samples, 26 * 26)

        # The certificate's own independent sample check agrees.
        cert = build_certificate(dag, [w, out], [res_w, res_out])
        vr = verify_certificate(cert)
        self.assertTrue(vr.ok, vr.reasons)

    def test_dense_matvec_containment(self):
        # y = W x with explicit sizes (m=2, n=2), cross-checked by a
        # self-written dense matvec on an enumeration grid.
        W = [[2.0, -1.0], [0.5, 3.0]]
        m, n = 2, 2
        dag = DAG()
        x1 = dag.var(-1.0, 1.0, "x1")
        x2 = dag.var(0.0, 2.0, "x2")
        # y0 = 2 x1 - x2 ; y1 = 0.5 x1 + 3 x2
        y0 = dag.add(dag.scale(x1, W[0][0]), dag.scale(x2, W[0][1]))
        y1 = dag.add(dag.scale(x1, W[1][0]), dag.scale(x2, W[1][1]))
        r0 = dag.evaluate(y0)
        r1 = dag.evaluate(y1)
        self.assertTrue(r0.complete and r1.complete)
        tol = 1e-12
        for a in grid(-1.0, 1.0, 21):
            for b in grid(0.0, 2.0, 21):
                out = matvec(W, [a, b], m, n)
                self.assertLessEqual(r0.interval[0] - tol, out[0])
                self.assertLessEqual(out[0], r0.interval[1] + tol)
                self.assertLessEqual(r1.interval[0] - tol, out[1])
                self.assertLessEqual(out[1], r1.interval[1] + tol)

    def test_dense_explicit_size_checks(self):
        with self.assertRaises(ValueError):
            matvec([[1.0, 2.0]], [1.0], 1, 2)  # vector too short
        with self.assertRaises(ValueError):
            matvec([[1.0, 2.0]], [1.0, 2.0], 2, 2)  # wrong row count
        with self.assertRaises(ValueError):
            matvec([[1.0], [2.0]], [1.0, 2.0], 2, 1)  # wrong col count


if __name__ == "__main__":
    unittest.main()
