"""Certificates: valid ones verify, tampered/incomplete ones are rejected."""

import copy
import unittest

from affine_dag import (
    DAG,
    abs_pl,
    build_certificate,
    certificate_from_json,
    certificate_to_json,
    verify_certificate,
)


def build_chain():
    dag = DAG()
    x = dag.var(0.5, 2.0, "x")
    y = dag.var(-1.0, 1.0, "y")
    t = dag.mul(x, y, "t")
    u = dag.add(t, x, "u")
    den = dag.add(x, dag.const(3.0), "den")
    w = dag.div(u, den, "w")
    p = dag.piecewise(u, abs_pl(), "p")
    out = dag.sub(p, dag.scale(x, 0.3), "out")
    results = {nid: dag.evaluate(nid) for nid in (w, out)}
    return dag, dict(x=x, y=y, t=t, u=u, den=den, w=w, p=p, out=out), results


class TestCertificate(unittest.TestCase):
    def test_valid_certificate_verifies(self):
        dag, n, results = build_chain()
        cert = build_certificate(dag, [n["w"], n["out"]], list(results.values()))
        vr = verify_certificate(cert)
        self.assertTrue(vr.ok, vr.reasons)
        self.assertTrue(vr.complete)

    def test_json_round_trip(self):
        dag, n, results = build_chain()
        cert = build_certificate(dag, [n["out"]], [results[n["out"]]])
        restored = certificate_from_json(certificate_to_json(cert))
        vr = verify_certificate(restored)
        self.assertTrue(vr.ok, vr.reasons)

    def test_tampered_interval_rejected(self):
        dag, n, results = build_chain()
        cert = build_certificate(dag, [n["out"]], [results[n["out"]]])
        bad = copy.deepcopy(cert)
        bad["nodes"][n["out"]]["interval"][0] += 0.5  # shrink claimed interval
        vr = verify_certificate(bad)
        self.assertFalse(vr.ok)
        self.assertTrue(any("interval" in r for r in vr.reasons))

    def test_tampered_form_rejected(self):
        dag, n, results = build_chain()
        cert = build_certificate(dag, [n["out"]], [results[n["out"]]])
        bad = copy.deepcopy(cert)
        bad["nodes"][n["u"]]["form"]["center"] += 1.0
        vr = verify_certificate(bad)
        self.assertFalse(vr.ok)
        self.assertTrue(any("affine form" in r for r in vr.reasons))

    def test_tampered_symbol_radius_rejected(self):
        dag, n, results = build_chain()
        cert = build_certificate(dag, [n["out"]], [results[n["out"]]])
        bad = copy.deepcopy(cert)
        some_sid = next(iter(bad["symbols"]))
        bad["symbols"][some_sid] *= 2.0
        vr = verify_certificate(bad)
        self.assertFalse(vr.ok)

    def test_tampered_structure_rejected(self):
        dag, n, results = build_chain()
        cert = build_certificate(dag, [n["out"]], [results[n["out"]]])
        bad = copy.deepcopy(cert)
        bad["nodes"][n["t"]]["op"] = "add"  # lie about the operation
        vr = verify_certificate(bad)
        self.assertFalse(vr.ok)

    def test_wrong_format_rejected(self):
        vr = verify_certificate({"format": "something-else"})
        self.assertFalse(vr.ok)


if __name__ == "__main__":
    unittest.main()
