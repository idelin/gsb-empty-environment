import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

from dfa_min.core import build_dfa, minimize
from dfa_min.verify import counterexample_script, product_verify, states_equivalent
from tests.test_core import sample_dfa


class TestProductVerify(unittest.TestCase):
    def test_minimized_quotient_verified_by_product_search(self):
        cert = minimize(sample_dfa())
        ver = product_verify(cert.prepared, cert.quotient)
        self.assertTrue(ver.equivalent)
        self.assertIsNone(ver.witness)
        self.assertGreater(ver.pairs_explored, 0)

    def test_inequivalent_dfa_yields_shortest_witness(self):
        cert = minimize(sample_dfa())
        good = cert.quotient
        bad_trans = {s: dict(row) for s, row in good.transitions.items()}
        bad_trans[good.start]["0"] = bad_trans[good.start]["1"]
        bad = build_dfa(good.alphabet, good.states, bad_trans,
                        good.start, good.accepting)
        ver = product_verify(cert.prepared, bad)
        self.assertFalse(ver.equivalent)
        self.assertEqual(ver.witness, "0")

    def test_alphabet_mismatch_rejected(self):
        cert = minimize(sample_dfa())
        other = build_dfa(["a"], ["S"], {"S": {"a": "S"}}, "S", [])
        with self.assertRaises(ValueError):
            product_verify(cert.prepared, other)


class TestStatesEquivalent(unittest.TestCase):
    def test_equivalent_states(self):
        cert = minimize(sample_dfa())
        equiv, witness, pairs = states_equivalent(cert.prepared, "B", "C")
        self.assertTrue(equiv)
        self.assertIsNone(witness)
        self.assertGreater(pairs, 0)

    def test_inequivalent_states_give_witness(self):
        cert = minimize(sample_dfa())
        equiv, witness, pairs = states_equivalent(cert.prepared, "A", "B")
        self.assertFalse(equiv)
        self.assertEqual(witness, "")


class TestCounterexampleScript(unittest.TestCase):
    def test_script_is_independently_executable(self):
        cert = minimize(sample_dfa())
        good = cert.quotient
        bad_trans = {s: dict(row) for s, row in good.transitions.items()}
        bad_trans[good.start]["0"] = bad_trans[good.start]["1"]
        bad = build_dfa(good.alphabet, good.states, bad_trans,
                        good.start, good.accepting)
        ver = product_verify(cert.prepared, bad)
        self.assertFalse(ver.equivalent)
        script = counterexample_script(cert.prepared, bad, ver.witness,
                                       name1="original", name2="broken")
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / "counterexample.py"
            path.write_text(script)
            proc = subprocess.run([sys.executable, str(path)],
                                  capture_output=True, text=True)
        self.assertEqual(proc.returncode, 0, proc.stderr)
        self.assertIn("counterexample confirmed", proc.stdout)
        self.assertIn("'0'", proc.stdout)

    def test_script_fails_when_witness_invalid(self):
        cert = minimize(sample_dfa())
        script = counterexample_script(cert.prepared, cert.quotient, "0")
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / "not_a_counterexample.py"
            path.write_text(script)
            proc = subprocess.run([sys.executable, str(path)],
                                  capture_output=True, text=True)
        self.assertNotEqual(proc.returncode, 0)
        self.assertIn("NOT a counterexample", proc.stdout + proc.stderr)


if __name__ == "__main__":
    unittest.main()
