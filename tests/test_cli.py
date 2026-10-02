import contextlib
import io
import json
import os
import tempfile
import unittest
from pathlib import Path

from invcheck.cli import main

ROOT = Path(__file__).resolve().parent.parent
EXAMPLES = ROOT / "examples"


def run_cli(argv):
    out = io.StringIO()
    with contextlib.redirect_stdout(out):
        code = main(argv)
    return code, out.getvalue()


class TestCLI(unittest.TestCase):
    def test_check_inductive_failure_exit_1(self):
        code, out = run_cli([
            "check", str(EXAMPLES / "mod4_counter.json"),
            "--inv", str(EXAMPLES / "inv_no3.txt"),
        ])
        self.assertEqual(code, 1)
        self.assertIn("inductive_failed", out)
        self.assertIn("premise reachable from init: yes", out)

    def test_check_verified_exit_0_with_reference(self):
        code, out = run_cli([
            "check", str(EXAMPLES / "mod3_counter.json"),
            "--inv", str(EXAMPLES / "inv_mod3.txt"),
            "--reference",
        ])
        self.assertEqual(code, 0)
        self.assertIn("verified", out)
        self.assertIn("reference enumeration: agrees", out)

    def test_incremental_refinement_stages(self):
        code, out = run_cli([
            "check", str(EXAMPLES / "latch.json"),
            "--inv", str(EXAMPLES / "inv_latch.txt"),
        ])
        self.assertEqual(code, 0)
        self.assertIn("== stage 1", out)
        self.assertIn("== stage 2", out)
        self.assertIn("inductive_failed", out)
        self.assertIn("verified", out)
        self.assertIn("unreachable", out.lower())

    def test_empty_init_reported(self):
        code, out = run_cli([
            "check", str(EXAMPLES / "empty_init.json"),
            "--inv", str(EXAMPLES / "inv_mod3.txt"),
        ])
        self.assertEqual(code, 0)
        self.assertIn("empty_init", out)

    def test_node_limit_exit_2(self):
        code, out = run_cli([
            "check", str(EXAMPLES / "mod4_counter.json"),
            "--inv", str(EXAMPLES / "inv_no3.txt"),
            "--node-limit", "5",
        ])
        self.assertEqual(code, 2)
        self.assertIn("undecided", out)

    def test_permutation_keeps_verdict(self):
        for permute in ("reverse", "random:3"):
            code, out = run_cli([
                "check", str(EXAMPLES / "mod4_counter.json"),
                "--inv", str(EXAMPLES / "inv_no3.txt"),
                "--order", "block", "--permute", permute,
            ])
            self.assertEqual(code, 1)
            self.assertIn("inductive_failed", out)

    def test_certificate_written_and_replayed(self):
        with tempfile.TemporaryDirectory() as tmp:
            cert = os.path.join(tmp, "cert.json")
            code, _ = run_cli([
                "check", str(EXAMPLES / "mod4_counter.json"),
                "--inv", str(EXAMPLES / "inv_no3.txt"),
                "--cert", cert,
            ])
            self.assertEqual(code, 1)
            self.assertTrue(os.path.exists(cert))
            code, out = run_cli(["replay", cert])
            self.assertEqual(code, 0)
            self.assertIn("replay succeeded", out)

    def test_enumerate_subcommand(self):
        code, out = run_cli([
            "enumerate", str(EXAMPLES / "mod3_counter.json"),
            "--inv", str(EXAMPLES / "inv_mod3.txt"),
        ])
        self.assertEqual(code, 0)
        self.assertEqual(json.loads(out)["verdict"], "verified")


if __name__ == "__main__":
    unittest.main()
