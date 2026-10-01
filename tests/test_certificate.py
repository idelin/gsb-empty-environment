import copy
import os
import unittest

from invcheck import (InvariantChecker, build_certificate, load_model,
                      replay_certificate, verify_certificate)

EXAMPLES = os.path.join(os.path.dirname(os.path.dirname(
    os.path.abspath(__file__))), "examples")


def make_cert(example):
    model = load_model(os.path.join(EXAMPLES, example))
    result = InvariantChecker(model).check()
    return build_certificate(model, result)


class TestCertificate(unittest.TestCase):
    def test_pass_certificate_verifies(self):
        cert = make_cert("mod3_counter.json")
        report = verify_certificate(cert)
        self.assertTrue(report["ok"], report)

    def test_step_fail_certificate_verifies(self):
        cert = make_cert("mod5_unreachable_step.json")
        report = verify_certificate(cert)
        self.assertTrue(report["ok"], report)
        names = {c["check"] for c in report["checks"]}
        self.assertIn("step_cex_valid", names)
        self.assertIn("step_cex_minimal", names)
        self.assertIn("replay", names)

    def test_base_fail_certificate_verifies(self):
        cert = make_cert("base_fail.json")
        report = verify_certificate(cert)
        self.assertTrue(report["ok"], report)

    def test_tampered_verdict_detected(self):
        cert = make_cert("mod5_unreachable_step.json")
        cert["verdict"] = "PASS"
        report = verify_certificate(cert)
        self.assertFalse(report["ok"])

    def test_tampered_cex_detected(self):
        cert = make_cert("mod5_unreachable_step.json")
        # flip a literal in the source cube: 6 (b2 b1 ~b0-ish) -> wrong state
        cert["step_cex"]["state"] = {"b2": False, "b1": False, "b0": False}
        report = verify_certificate(cert)
        self.assertFalse(report["ok"])

    def test_tampered_digest_detected(self):
        cert = make_cert("mod3_counter.json")
        cert["model"]["init"] = "b0"
        report = verify_certificate(cert)
        self.assertFalse(report["ok"])
        digest_check = [c for c in report["checks"] if c["check"] == "model_digest"]
        self.assertFalse(digest_check[0]["ok"])

    def test_replay_lines(self):
        cert = make_cert("mod5_unreachable_step.json")
        lines = replay_certificate(cert)
        self.assertEqual(len(lines), 1)
        self.assertIn("invariant(s)=True", lines[0])
        self.assertIn("transition=True", lines[0])
        self.assertIn("invariant(s')=False", lines[0])

    def test_replay_base_cex(self):
        cert = make_cert("base_fail.json")
        lines = replay_certificate(cert)
        self.assertIn("init=True", lines[0])
        self.assertIn("invariant=False", lines[0])

    def test_verifier_uses_different_order(self):
        cert = make_cert("mod3_counter.json")
        report = verify_certificate(cert)
        order_check = [c for c in report["checks"]
                       if c["check"] == "fresh_variable_order"]
        self.assertTrue(order_check[0]["ok"])

    def test_certificate_roundtrip_json(self):
        import json
        cert = make_cert("mod5_unreachable_step.json")
        blob = json.dumps(cert, sort_keys=True)
        report = verify_certificate(json.loads(blob))
        self.assertTrue(report["ok"])


if __name__ == "__main__":
    unittest.main()
