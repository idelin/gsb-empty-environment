import json
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent


def write(tmp, name, rows):
    path = Path(tmp) / name
    path.write_text(json.dumps(rows), encoding="utf-8")
    return str(path)


class CliTest(unittest.TestCase):
    def run_cli(self, *args):
        return subprocess.run(
            [sys.executable, "-m", "recon", *args],
            cwd=ROOT, capture_output=True, text=True, check=True)

    def test_onetoone_cli_acceptance_boundary(self):
        with tempfile.TemporaryDirectory() as tmp:
            left = write(tmp, "left.json", [
                {"id": "a", "keys": ["k1", "k2"], "amount": "10.00",
                 "time": 1000},
                {"id": "b", "keys": ["k1"], "amount": "10.00", "time": 1000},
            ])
            right = write(tmp, "right.json", [
                {"id": "x", "keys": ["k1"], "amount": "10.00", "time": 1000},
                {"id": "y", "keys": ["k2"], "amount": "10.00", "time": 1000},
            ])
            out = self.run_cli("onetoone", "--left", left, "--right", right,
                               "--window", "0")
            report = json.loads(out.stdout)
            pairs = {(p["left"], p["right"]) for p in report["pairs"]}
            self.assertEqual(pairs, {("a", "y"), ("b", "x")})
            self.assertTrue(report["verification"]["optimal"])

    def test_splitmerge_cli_with_exhaustive_verification(self):
        with tempfile.TemporaryDirectory() as tmp:
            left = write(tmp, "left.json", [
                {"id": "L1", "keys": ["inv-1"], "amount": "10.00",
                 "time": 1000},
            ])
            right = write(tmp, "right.json", [
                {"id": "R1", "keys": ["inv-1"], "amount": "6.00",
                 "time": 1005},
                {"id": "R2", "keys": ["inv-1"], "amount": "4.00",
                 "time": 1010},
            ])
            out = self.run_cli("splitmerge", "--left", left, "--right", right,
                               "--window", "60", "--verify-exhaustive")
            report = json.loads(out.stdout)
            self.assertEqual([g["gid"] for g in report["groups"]],
                             ["L1=R1+R2"])
            self.assertTrue(report["search"]["exhaustive_check"]
                            ["objectives_match"])
            self.assertTrue(report["optimal"])

    def test_splitmerge_cli_budget_interruption(self):
        with tempfile.TemporaryDirectory() as tmp:
            left = write(tmp, "left.json", [
                {"id": f"L{i}", "keys": ["k"], "amount": 10, "time": 0}
                for i in range(4)])
            right = write(tmp, "right.json", [
                {"id": f"R{i}", "keys": ["k"], "amount": 10, "time": 0}
                for i in range(4)])
            out = self.run_cli("splitmerge", "--left", left, "--right", right,
                               "--window", "0", "--budget", "0")
            report = json.loads(out.stdout)
            self.assertTrue(report["search"]["interrupted"])
            self.assertFalse(report["optimal"])


if __name__ == "__main__":
    unittest.main()
