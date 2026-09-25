"""Incremental updates: changing one noise bound touches only successors."""

import unittest

from affine_dag import DAG


def build_graph():
    dag = DAG()
    x = dag.var(-1.0, 1.0, "x")      # node 0
    y = dag.var(-1.0, 1.0, "y")      # node 1
    z = dag.var(2.0, 3.0, "z")       # node 2
    a = dag.add(x, y, "a")           # node 3, deps {x, y}
    b = dag.mul(a, z, "b")           # node 4, deps {x, y, z}
    w = dag.mul(z, z, "w")           # node 5, deps {z} only
    out = dag.add(b, w, "out")       # node 6
    return dag, dict(x=x, y=y, z=z, a=a, b=b, w=w, out=out)


class TestIncremental(unittest.TestCase):
    def test_only_affected_successors_recomputed(self):
        dag, n = build_graph()
        first = dag.evaluate_all()
        self.assertTrue(first.complete)
        self.assertEqual(set(first.recomputed), set(range(7)))

        w_form_before = dag.nodes[n["w"]].form
        x_sid = dag.nodes[n["x"]].params["sid"]
        dirtied = dag.set_symbol_radius(x_sid, 0.25)  # x now in [-0.25, 0.25]
        self.assertEqual(set(dirtied), {n["x"], n["a"], n["b"], n["out"]})

        second = dag.evaluate_all()
        self.assertEqual(set(second.recomputed), {n["x"], n["a"], n["b"], n["out"]})
        # Untouched branch keeps its cached form object (not recomputed).
        self.assertIs(dag.nodes[n["w"]].form, w_form_before)
        self.assertNotIn(n["y"], second.recomputed)
        self.assertNotIn(n["z"], second.recomputed)

    def test_intervals_update_downstream(self):
        dag, n = build_graph()
        dag.evaluate_all()
        x_sid = dag.nodes[n["x"]].params["sid"]
        dag.set_symbol_radius(x_sid, 0.5)
        dag.evaluate_all()
        # a = x + y now has radius 0.5 + 1 = 1.5
        self.assertEqual(dag.interval_of(n["a"]), (-1.5, 1.5))
        # w = z*z unaffected by x; AA gives 6.25 +/- (2.5 + 0.25)
        self.assertEqual(dag.interval_of(n["w"]), (3.5, 9.0))
        # ... which conservatively contains the true range [4, 9]

    def test_certificate_stable_after_incremental_change(self):
        from affine_dag import build_certificate, verify_certificate

        dag, n = build_graph()
        dag.evaluate_all()
        x_sid = dag.nodes[n["x"]].params["sid"]
        dag.set_symbol_radius(x_sid, 0.5)
        res = dag.evaluate_all()
        cert = build_certificate(dag, [n["out"]], [res])
        vr = verify_certificate(cert)
        self.assertTrue(vr.ok, vr.reasons)


if __name__ == "__main__":
    unittest.main()
