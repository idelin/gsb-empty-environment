import json
import os
import tempfile
import unittest

from invcheck import expr as exprmod
from invcheck.certificate import (
    build_certificate,
    save_certificate,
    load_certificate,
    replay_certificate,
)
from invcheck.checker import InvariantChecker
from invcheck.model import parse_model

from tests.helpers import MOD3, MOD4, LATCH


def make_cert(model_dict, inv_text):
    model = parse_model(model_dict)
    inv_ast = exprmod.parse(inv_text)
    res = InvariantChecker(model).check(inv_ast, inv_text)
    return build_certificate(model, inv_text, res)


class TestCertificate(unittest.TestCase):
    def test_replay_inductive_failure(self):
        cert = make_cert(MOD4, "!(a & b)")
        report = replay_certificate(cert)
        self.assertTrue(report["ok"], report)
        names = [c["name"] for c in report["checks"]]
        self.assertTrue(any(n.startswith("inductive") for n in names))

    def test_replay_base_failure(self):
        cert = make_cert(MOD3, "a")
        report = replay_certificate(cert)
        self.assertTrue(report["ok"], report)

    def test_replay_verified(self):
        cert = make_cert(MOD3, "!(a & b)")
        report = replay_certificate(cert)
        self.assertTrue(report["ok"], report)

    def test_replay_unreachable_premise_case(self):
        cert = make_cert(LATCH, "!a")
        self.assertFalse(cert["inductive_counterexample_premise_reachable"])
        report = replay_certificate(cert)
        self.assertTrue(report["ok"], report)

    def test_roundtrip_through_disk(self):
        cert = make_cert(MOD4, "!(a & b)")
        with tempfile.TemporaryDirectory() as tmp:
            path = os.path.join(tmp, "cert.json")
            save_certificate(cert, path)
            loaded = load_certificate(path)
        self.assertEqual(loaded, cert)
        self.assertTrue(replay_certificate(loaded)["ok"])

    def test_tampered_counterexample_fails_replay(self):
        cert = make_cert(MOD4, "!(a & b)")
        cex = cert["inductive_step"]["counterexample"]
        cex["successor"]["a"] = not cex["successor"]["a"]
        report = replay_certificate(cert)
        self.assertFalse(report["ok"])

    def test_tampered_model_hash_fails_replay(self):
        cert = make_cert(MOD4, "!(a & b)")
        cert["model"]["name"] = "forged"
        report = replay_certificate(cert)
        self.assertFalse(report["ok"])

    def test_certificate_is_json_serializable(self):
        cert = make_cert(MOD4, "!(a & b)")
        json.dumps(cert)


if __name__ == "__main__":
    unittest.main()
