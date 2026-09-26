"""Tests for the model checker and the independent verifier."""

from __future__ import annotations

import copy
import io
import json
import os
import subprocess
import sys
import tempfile
import unittest
from contextlib import redirect_stderr, redirect_stdout
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "src"))

from modelchecker.checker import CheckOutcome, check_safety  # noqa: E402
from modelchecker.cli import main  # noqa: E402
from modelchecker.model import FiniteModel  # noqa: E402
from modelchecker.models import (  # noqa: E402
    long_chain,
    trap_unsafe,
    two_bit_counter,
    unreachable_bad,
)
from modelchecker.verifier import (  # noqa: E402
    VerificationVerdict,
    verify_certificate,
    verify_counterexample,
    verify_reachability,
)


def safe_model() -> FiniteModel:
    return FiniteModel.build(
        name="m",
        states=[0, 1, 2],
        initial=[0],
        transitions={0: [1], 1: [2], 2: []},
        bad_states=[],
    )


class TestChecker(unittest.TestCase):
    def test_safe_model_emits_complete_closure(self):
        model = safe_model()
        result = check_safety(model)
        self.assertIs(result.outcome, CheckOutcome.SAFE)
        cert = result.certificate
        self.assertEqual(cert["kind"], "reachability_closure")
        self.assertEqual(cert["reachable_states"], [0, 1, 2])
        self.assertEqual(cert["initial"], [0])

    def test_unsafe_model_emits_replayable_path(self):
        result = check_safety(trap_unsafe())
        self.assertIs(result.outcome, CheckOutcome.UNSAFE)
        self.assertEqual(result.counterexample, (0, 1, 2))
        self.assertEqual(result.certificate["bad_state"], 2)

    def test_budget_exhaustion_stays_unknown(self):
        result = check_safety(long_chain(), budget=3)
        self.assertIs(result.outcome, CheckOutcome.UNKNOWN)
        self.assertIsNone(result.certificate)
        self.assertGreater(len(result.explored), 3)
        self.assertIn("budget", result.reason)

    def test_budget_greater_than_reachable_decides(self):
        result = check_safety(long_chain(), budget=100)
        self.assertIs(result.outcome, CheckOutcome.SAFE)

    def test_budget_zero_multiple_initial_states(self):
        model = FiniteModel.build(
            name="two_init",
            states=[0, 1, 2],
            initial=[0, 1],
            transitions={0: [2], 1: [2], 2: []},
            bad_states=[],
        )
        result = check_safety(model, budget=0)
        self.assertIs(result.outcome, CheckOutcome.UNKNOWN)

    def test_unreachable_bad_state_is_safe(self):
        result = check_safety(unreachable_bad())
        self.assertIs(result.outcome, CheckOutcome.SAFE)
        self.assertNotIn(2, result.explored)


class TestVerifierHappyPath(unittest.TestCase):
    def test_valid_safe_closure(self):
        model = two_bit_counter()
        cert = check_safety(model).certificate
        verdict, findings = verify_reachability(model, cert)
        self.assertIs(verdict, VerificationVerdict.VALID)
        self.assertEqual(findings, [])

    def test_valid_counterexample_replayed(self):
        model = trap_unsafe()
        cert = check_safety(model).certificate
        verdict, findings = verify_counterexample(model, cert)
        self.assertIs(verdict, VerificationVerdict.VALID)
        self.assertEqual(findings, [])

    def test_dispatch_by_kind(self):
        model = trap_unsafe()
        cert = check_safety(model).certificate
        verdict, _ = verify_certificate(model, cert)
        self.assertIs(verdict, VerificationVerdict.VALID)


class TestVerifierTampering(unittest.TestCase):
    def test_deleting_required_successor_fails_closure(self):
        model = two_bit_counter()
        cert = copy.deepcopy(check_safety(model).certificate)
        # State 1's successor 2 is a required successor of a reachable state.
        cert["successors"]["1"].remove(2)
        verdict, findings = verify_reachability(model, cert)
        self.assertIs(verdict, VerificationVerdict.INVALID)
        codes = {(f.code, f.detail.get("state"), f.detail.get("missing_successor"))
                 for f in findings}
        self.assertIn(("SUCCESSOR_NOT_CLOSED", 1, 2), codes)

    def test_deleting_successor_state_also_detected_independently(self):
        model = two_bit_counter()
        cert = copy.deepcopy(check_safety(model).certificate)
        # Remove state 2 from the closure outright.
        cert["reachable_states"].remove(2)
        del cert["successors"]["2"]
        verdict, findings = verify_reachability(model, cert)
        self.assertIs(verdict, VerificationVerdict.INVALID)
        self.assertTrue(
            any(f.code == "REACHABILITY_INCOMPLETE" and 2 in f.detail["states"]
                for f in findings)
        )

    def test_missing_initial_state_rejected(self):
        model = two_bit_counter()
        cert = copy.deepcopy(check_safety(model).certificate)
        cert["initial"] = []
        verdict, findings = verify_reachability(model, cert)
        self.assertIs(verdict, VerificationVerdict.INVALID)
        self.assertTrue(any(f.code == "INITIAL_NOT_COVERED" for f in findings))

    def test_bad_state_injected_into_safe_closure(self):
        model = FiniteModel.build(
            name="m",
            states=[0, 1, 2],
            initial=[0],
            transitions={0: [1], 1: [1], 2: []},
            bad_states=[2],
        )
        cert = check_safety(model).certificate
        # Forge a closure that silently includes the bad, unreachable state.
        cert["reachable_states"].append(2)
        cert["successors"]["2"] = []
        verdict, findings = verify_reachability(model, cert)
        self.assertIs(verdict, VerificationVerdict.INVALID)
        self.assertTrue(
            any(f.code == "PROPERTY_VIOLATED" and f.detail["state"] == 2
                for f in findings)
        )

    def test_phantom_successor_rejected(self):
        model = two_bit_counter()
        cert = copy.deepcopy(check_safety(model).certificate)
        cert["successors"]["0"].append(2)
        verdict, findings = verify_reachability(model, cert)
        self.assertIs(verdict, VerificationVerdict.INVALID)
        self.assertTrue(any(f.code == "PHANTOM_SUCCESSOR" for f in findings))

    def test_bad_counterexample_transition_is_localized(self):
        model = trap_unsafe()
        cert = copy.deepcopy(check_safety(model).certificate)
        # Rewrite the path so step 1 jumps from 1 to a state it cannot reach.
        cert["path"] = [0, 1, 3]
        cert["transitions"][1]["target"] = 3
        cert["bad_state"] = 3
        verdict, findings = verify_counterexample(model, cert)
        self.assertIs(verdict, VerificationVerdict.INVALID)
        bad = [f for f in findings if f.code == "BAD_TRANSITION"]
        self.assertEqual(len(bad), 1)
        self.assertEqual(bad[0].detail["transition_index"], 1)
        self.assertEqual(bad[0].detail["source"], 1)
        self.assertEqual(bad[0].detail["target"], 3)
        self.assertEqual(bad[0].detail["available"], [2])
        self.assertTrue(any(f.code == "END_NOT_BAD" for f in findings))

    def test_non_initial_counterexample_start_rejected(self):
        model = trap_unsafe()
        cert = copy.deepcopy(check_safety(model).certificate)
        cert["path"] = [1, 2]
        # Keep both original records (path now needs exactly one).
        verdict, findings = verify_counterexample(model, cert)
        self.assertIs(verdict, VerificationVerdict.INVALID)
        self.assertTrue(any(f.code == "NOT_INITIAL" for f in findings))
        self.assertTrue(any(f.code == "TRANSITION_LENGTH_MISMATCH" for f in findings))


class TestPartialAndMismatch(unittest.TestCase):
    def test_budget_partial_graph_rejected_as_certificate(self):
        # A partial BFS frontier is not a certificate kind at all.
        partial = {"kind": "partial_exploration", "reachable_states": [0, 1]}
        model = two_bit_counter()
        verdict, findings = verify_certificate(model, partial)
        self.assertIs(verdict, VerificationVerdict.INVALID)
        self.assertEqual(findings[0].code, "UNSUPPORTED_KIND")

    def test_partial_closure_missing_frontier_state_rejected(self):
        model = long_chain()
        cert = copy.deepcopy(check_safety(model).certificate)
        # Chop off states 3..9 while pretending the result is complete:
        # both successor closure and independent re-derivation must catch it.
        cert["reachable_states"] = [s for s in cert["reachable_states"] if s <= 2]
        cert["successors"] = {
            k: v for k, v in cert["successors"].items() if int(k) <= 2
        }
        # Also erase the dangling edge so only independent re-derivation can
        # prove the certificate incomplete.
        cert["successors"]["2"] = []
        verdict, findings = verify_reachability(model, cert)
        self.assertIs(verdict, VerificationVerdict.INVALID)
        self.assertTrue(any(f.code == "REACHABILITY_INCOMPLETE" for f in findings))

    def test_fingerprint_mismatch_rejected(self):
        other = FiniteModel.build(
            name="other",
            states=[0, 1],
            initial=[0],
            transitions={0: [1], 1: [1]},
            bad_states=[],
        )
        cert = check_safety(two_bit_counter()).certificate
        verdict, findings = verify_reachability(other, cert)
        self.assertIs(verdict, VerificationVerdict.INVALID)
        self.assertEqual(findings[0].code, "FINGERPRINT_MISMATCH")

    def test_generator_stats_are_not_trusted(self):
        model = two_bit_counter()
        cert = copy.deepcopy(check_safety(model).certificate)
        # Lie in the advisory stats; the verdict must be based on structure.
        cert["generator_stats"]["reachable_count"] = 9999
        verdict, findings = verify_reachability(model, cert)
        self.assertIs(verdict, VerificationVerdict.VALID)

    def test_model_validation_rejects_unknown_state(self):
        with self.assertRaises(ValueError):
            FiniteModel.build(
                name="bad",
                states=[0, 1],
                initial=[0],
                transitions={0: [1], 1: [7]},
            )


class TestCli(unittest.TestCase):
    def _run_cli(self, *argv: str) -> tuple[int, str, str]:
        out, err = io.StringIO(), io.StringIO()
        with redirect_stdout(out), redirect_stderr(err):
            code = main(list(argv))
        return code, out.getvalue(), err.getvalue()

    def test_cli_check_and_verify_safe_roundtrip(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = os.path.join(tmp, "cert.json")
            code, out, _ = self._run_cli(
                "check", "two_bit_counter", "-o", path
            )
            self.assertEqual(code, 0)
            self.assertEqual(json.loads(out)["outcome"], "safe")
            code, out, _ = self._run_cli(
                "verify", "two_bit_counter", path
            )
            self.assertEqual(code, 0)
            self.assertEqual(json.loads(out)["verdict"], "valid")

    def test_cli_unsafe_exit_code(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = os.path.join(tmp, "cex.json")
            code, _, _ = self._run_cli("check", "trap_unsafe", "-o", path)
            self.assertEqual(code, 2)
            code, out, _ = self._run_cli("verify", "trap_unsafe", path)
            self.assertEqual(code, 0)
            self.assertEqual(json.loads(out)["certificate_kind"], "counterexample")

    def test_cli_budget_unknown_exit_code(self):
        code, out, _ = self._run_cli(
            "check", "long_chain", "--budget", "2"
        )
        self.assertEqual(code, 3)
        self.assertEqual(json.loads(out)["outcome"], "unknown")

    def test_cli_verifier_catches_tampering(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = os.path.join(tmp, "cert.json")
            code, _, _ = self._run_cli(
                "check", "two_bit_counter", "-o", path
            )
            self.assertEqual(code, 0)
            data = json.loads(Path(path).read_text())
            data["successors"]["0"] = []  # delete required successor
            Path(path).write_text(json.dumps(data))
            code, out, _ = self._run_cli(
                "verify", "two_bit_counter", path
            )
            self.assertEqual(code, 1)
            report = json.loads(out)
            self.assertEqual(report["verdict"], "invalid")
            self.assertIn("SUCCESSOR_NOT_CLOSED",
                          [f["code"] for f in report["findings"]])

    def test_cli_custom_json_model(self):
        with tempfile.TemporaryDirectory() as tmp:
            model_path = os.path.join(tmp, "model.json")
            cert_path = os.path.join(tmp, "cert.json")
            Path(model_path).write_text(json.dumps({
                "name": "custom_loop",
                "states": ["a", "b"],
                "initial": ["a"],
                "transitions": {'"a"': ["b"], '"b"': ["a"]},
                "bad_states": [],
            }))
            code, _, _ = self._run_cli(
                "check", model_path, "-o", cert_path
            )
            self.assertEqual(code, 0)
            code, out, _ = self._run_cli("verify", model_path, cert_path)
            self.assertEqual(code, 0)
            self.assertEqual(json.loads(out)["verdict"], "valid")

    def test_module_invocation(self):
        env = dict(os.environ)
        repo = Path(__file__).resolve().parents[1]
        env["PYTHONPATH"] = str(repo / "src")
        proc = subprocess.run(
            [sys.executable, "-m", "modelchecker.cli", "list"],
            capture_output=True,
            text=True,
            env=env,
            check=False,
        )
        self.assertEqual(proc.returncode, 0)
        self.assertIn("two_bit_counter", proc.stdout)


if __name__ == "__main__":
    unittest.main(verbosity=2)
