import json
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent


class TestCli(unittest.TestCase):
    def run_cli(self, *args):
        return subprocess.run([sys.executable, str(ROOT / "main.py"), *args],
                              capture_output=True, text=True, cwd=ROOT)

    def test_minimize_sample(self):
        proc = self.run_cli("minimize", "examples/sample_dfa.json")
        self.assertEqual(proc.returncode, 0, proc.stderr)
        out = proc.stdout
        self.assertIn("Removed unreachable states: D", out)
        self.assertIn("dead state: __dead__", out)
        self.assertIn("A -> q0", out)
        self.assertIn("E -> q1", out)
        self.assertIn("B -> q2", out)
        self.assertIn("C -> q2", out)
        self.assertIn("__dead__ -> q3", out)
        self.assertIn("block q2: {B, C}", out)
        self.assertIn("q0 vs q1: '01'", out)
        self.assertIn("state pairs explored:", out)
        self.assertIn("result: EQUIVALENT", out)

    def test_verify_equivalent_and_counterexample(self):
        with tempfile.TemporaryDirectory() as tmp:
            qpath = Path(tmp) / "quotient.json"
            sys.path.insert(0, str(ROOT))
            from dfa_min.cli import dfa_from_json
            from dfa_min.core import minimize
            from dfa_min.verify import dfa_to_data
            data = json.loads((ROOT / "examples/sample_dfa.json").read_text())
            cert = minimize(dfa_from_json(data))
            qpath.write_text(json.dumps(dfa_to_data(cert.quotient)))
            proc = self.run_cli("verify", "examples/sample_dfa.json", str(qpath))
            self.assertEqual(proc.returncode, 0, proc.stderr)
            self.assertIn("result: EQUIVALENT", proc.stdout)

    def test_verify_inequivalent_writes_counterexample(self):
        with tempfile.TemporaryDirectory() as tmp:
            sys.path.insert(0, str(ROOT))
            from dfa_min.cli import dfa_from_json
            from dfa_min.core import build_dfa, minimize
            from dfa_min.verify import dfa_to_data
            data = json.loads((ROOT / "examples/sample_dfa.json").read_text())
            cert = minimize(dfa_from_json(data))
            good = cert.quotient
            bad_trans = {s: dict(row) for s, row in good.transitions.items()}
            bad_trans[good.start]["0"] = bad_trans[good.start]["1"]
            bad = build_dfa(good.alphabet, good.states, bad_trans,
                            good.start, good.accepting)
            p1 = Path(tmp) / "a.json"
            p2 = Path(tmp) / "b.json"
            cx = Path(tmp) / "cx.py"
            p1.write_text(json.dumps(dfa_to_data(cert.prepared)))
            p2.write_text(json.dumps(dfa_to_data(bad)))
            proc = self.run_cli("verify", str(p1), str(p2), "-o", str(cx))
            self.assertEqual(proc.returncode, 1)
            self.assertIn("NOT EQUIVALENT", proc.stdout)
            self.assertTrue(cx.exists())
            run = subprocess.run([sys.executable, str(cx)],
                                 capture_output=True, text=True)
            self.assertEqual(run.returncode, 0, run.stderr)
            self.assertIn("counterexample confirmed", run.stdout)


if __name__ == "__main__":
    unittest.main()
