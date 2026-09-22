import json
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
MAIN = ROOT / "main.py"
SAMPLES = ROOT / "samples"


def run_cli(*args):
    return subprocess.run(
        [sys.executable, str(MAIN), *args],
        capture_output=True, text=True, cwd=ROOT,
    )


class TestCli(unittest.TestCase):
    def test_solve_three_package(self):
        proc = run_cli("solve", str(SAMPLES / "three_package_global.json"))
        self.assertEqual(proc.returncode, 0, proc.stderr)
        out = json.loads(proc.stdout)
        self.assertEqual(out["status"], "optimal")
        self.assertEqual(out["objective"], {"exceptions": 0, "disclosures": 2})

    def test_enumerate_agrees_with_solve(self):
        proc = run_cli("enumerate", str(SAMPLES / "three_package_global.json"))
        out = json.loads(proc.stdout)
        self.assertEqual(out["objective"], {"exceptions": 0, "disclosures": 2})

    def test_budget_never_claims_full_pass(self):
        proc = run_cli("solve", str(SAMPLES / "budget.json"), "--budget", "12")
        out = json.loads(proc.stdout)
        self.assertEqual(out["status"], "feasible_upper_bound")
        self.assertFalse(out["complete"])
        self.assertTrue(out["pending"])
        self.assertIn("upper bound", out["note"])

    def test_repair_flow(self):
        with tempfile.TemporaryDirectory() as tmp:
            prev = Path(tmp) / "prev.json"
            proc = run_cli("solve", str(SAMPLES / "repair_v1.json"))
            prev.write_text(proc.stdout)
            proc = run_cli(
                "repair", str(SAMPLES / "repair_v2.json"), "--prev", str(prev)
            )
            self.assertEqual(proc.returncode, 0, proc.stderr)
            out = json.loads(proc.stdout)
            self.assertEqual(out["status"], "optimal")
            self.assertEqual(out["mode"], "pinned")
            self.assertIn("app@1.0.0", out["kept"])

    def test_infeasible_reports_core(self):
        proc = run_cli("solve", str(SAMPLES / "conflict_core.json"))
        out = json.loads(proc.stdout)
        self.assertEqual(out["status"], "infeasible")
        self.assertIn("conflict_core", out)
        triggers = {i["trigger"] for i in out["conflict_core"]["instances"]}
        self.assertEqual(triggers, {"libP@1.0.0"})


if __name__ == "__main__":
    unittest.main()
