"""Unittest suite for replica_placement (synthetic data only)."""

import itertools
import json
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

import replica_placement as rp

PYTHON = sys.executable  # tests must be run with Python 3.11+
CLI = str(Path(__file__).with_name("replica_placement.py"))


def acceptance_spec():
    """Acceptance boundary: r=2, q=1; s1 and s2 share failure set fs1,
    s3 is outside every failure set. Placing {s1, s2} must be rejected."""
    return {
        "r": 2,
        "q": 1,
        "servers": [
            {"id": "s1", "rack": "r1", "zone": "z1", "capacity": 1, "cost": 1},
            {"id": "s2", "rack": "r1", "zone": "z1", "capacity": 1, "cost": 1},
            {"id": "s3", "rack": "r2", "zone": "z2", "capacity": 1, "cost": 5},
        ],
        "failure_sets": [
            {"name": "fs1", "servers": ["s1", "s2"]},
        ],
    }


class AcceptanceBoundaryTest(unittest.TestCase):
    def test_same_failure_set_pair_rejected(self):
        cert = rp.solve(acceptance_spec())
        self.assertEqual(cert["status"], "optimal")
        placement = set(cert["placement"])
        # Both replicas inside fs1 would leave 0 < q=1 after fs1 fails.
        self.assertFalse(placement <= {"s1", "s2"},
                         "placement must not lie entirely inside fs1")
        # At least one replica must be on a server outside the failure set.
        self.assertTrue(placement - {"s1", "s2"},
                        "at least one server outside the failure set is required")
        self.assertEqual(placement, {"s1", "s3"})  # cheapest valid choice
        self.assertEqual(cert["total_cost"], 6)

    def test_witness_records_survivors(self):
        cert = rp.solve(acceptance_spec())
        (w,) = cert["failure_witnesses"]
        self.assertEqual(w["failure_set"], "fs1")
        self.assertEqual(w["replicas_lost"], 1)
        self.assertEqual(w["replicas_remaining"], 1)
        self.assertEqual(w["required_remaining"], 1)
        self.assertTrue(w["holds"])

    def test_only_failure_set_members_infeasible(self):
        spec = acceptance_spec()
        spec["servers"] = spec["servers"][:2]  # drop s3
        cert = rp.solve(spec)
        self.assertEqual(cert["status"], "infeasible")
        self.assertIsNone(cert["placement"])
        st = cert["statistics"]
        self.assertEqual(st["combinations_evaluated"], 1)
        self.assertEqual(st["rejected_fault"], 1)
        self.assertEqual(st["rejected_fault_by_set"], {"fs1": 1})
        self.assertEqual(st["feasible"], 0)


class OptimalityTest(unittest.TestCase):
    def test_picks_cheapest_feasible_combo(self):
        spec = {
            "r": 2, "q": 1,
            "servers": [
                {"id": "a", "rack": "r1", "zone": "z1", "capacity": 1, "cost": 1},
                {"id": "b", "rack": "r2", "zone": "z1", "capacity": 1, "cost": 2},
                {"id": "c", "rack": "r3", "zone": "z2", "capacity": 1, "cost": 3},
            ],
            "failure_sets": [{"name": "rack-r1", "servers": ["a"]}],
        }
        cert = rp.solve(spec)
        self.assertEqual(cert["status"], "optimal")
        self.assertEqual(cert["placement"], ["a", "b"])
        self.assertEqual(cert["total_cost"], 3)

    def test_matches_bruteforce_minimum(self):
        # Cross-check solver cost against an independent brute force.
        spec = {
            "r": 3, "q": 1,
            "servers": [
                {"id": f"s{i}", "rack": f"r{i % 2}", "zone": f"z{i % 3}",
                 "capacity": 1, "cost": (i * 7) % 5 + 1}
                for i in range(7)
            ],
            "failure_sets": [
                {"name": "rack-r0", "servers": [f"s{i}" for i in range(7) if i % 2 == 0]},
                {"name": "zone-z0", "servers": [f"s{i}" for i in range(7) if i % 3 == 0]},
            ],
        }
        cert = rp.solve(spec)
        self.assertEqual(cert["status"], "optimal")
        by_id = {s["id"]: s for s in spec["servers"]}
        fs = [set(f["servers"]) for f in spec["failure_sets"]]
        best = None
        for combo in itertools.combinations(spec["servers"], 3):
            ids = {s["id"] for s in combo}
            if all(3 - len(ids & m) >= 1 for m in fs):
                cost = sum(s["cost"] for s in combo)
                best = cost if best is None else min(best, cost)
        self.assertEqual(cert["total_cost"], best)
        self.assertEqual(sum(by_id[s]["cost"] for s in cert["placement"]), best)

    def test_deterministic_tie_break(self):
        spec = {
            "r": 2, "q": 1,
            "servers": [
                {"id": "b", "rack": "r1", "zone": "z1", "capacity": 1, "cost": 1},
                {"id": "a", "rack": "r2", "zone": "z1", "capacity": 1, "cost": 1},
                {"id": "c", "rack": "r3", "zone": "z2", "capacity": 1, "cost": 1},
            ],
            "failure_sets": [],
        }
        cert = rp.solve(spec)
        self.assertEqual(cert["placement"], ["a", "b"])


class CapacityTest(unittest.TestCase):
    def test_zero_capacity_server_never_used(self):
        spec = {
            "r": 2, "q": 1,
            "servers": [
                {"id": "cheap-but-full", "rack": "r1", "zone": "z1",
                 "capacity": 0, "cost": 0},
                {"id": "x", "rack": "r2", "zone": "z1", "capacity": 1, "cost": 9},
                {"id": "y", "rack": "r3", "zone": "z2", "capacity": 1, "cost": 9},
            ],
            "failure_sets": [],
        }
        cert = rp.solve(spec)
        self.assertEqual(cert["placement"], ["x", "y"])
        self.assertEqual(cert["statistics"]["rejected_capacity"], 2)


class StatisticsTest(unittest.TestCase):
    def test_counters_partition_all_combinations(self):
        spec = {
            "r": 2, "q": 2,
            "servers": [
                {"id": "a", "rack": "r1", "zone": "z1", "capacity": 0, "cost": 1},
                {"id": "b", "rack": "r1", "zone": "z1", "capacity": 1, "cost": 1},
                {"id": "c", "rack": "r2", "zone": "z2", "capacity": 1, "cost": 1},
                {"id": "d", "rack": "r3", "zone": "z3", "capacity": 1, "cost": 1},
            ],
            "failure_sets": [{"name": "fs", "servers": ["b", "c"]}],
        }
        cert = rp.solve(spec)
        st = cert["statistics"]
        self.assertEqual(st["combinations_total"], 6)
        self.assertEqual(st["combinations_evaluated"], 6)
        self.assertEqual(
            st["rejected_capacity"] + st["rejected_fault"] + st["feasible"],
            st["combinations_evaluated"])
        self.assertEqual(sum(st["rejected_fault_by_set"].values()),
                         st["rejected_fault"])

    def test_infeasible_stats_are_auditable_not_a_certificate(self):
        spec = {
            "r": 3, "q": 1,
            "servers": [
                {"id": "a", "rack": "r1", "zone": "z1", "capacity": 1, "cost": 1},
                {"id": "b", "rack": "r1", "zone": "z1", "capacity": 1, "cost": 1},
            ],
            "failure_sets": [],
        }
        cert = rp.solve(spec)
        self.assertEqual(cert["status"], "infeasible")
        self.assertIsNone(cert["placement"])
        self.assertIsNone(cert["total_cost"])
        self.assertNotIn("failure_witnesses", cert)
        st = cert["statistics"]
        self.assertEqual(st["combinations_total"], 0)  # C(2,3) = 0
        self.assertEqual(st["combinations_evaluated"], 0)
        self.assertEqual(st["feasible"], 0)


class ValidationTest(unittest.TestCase):
    def test_rejects_q_greater_than_r(self):
        spec = acceptance_spec()
        spec["q"] = 3
        with self.assertRaises(rp.SpecError):
            rp.solve(spec)

    def test_rejects_duplicate_server_ids(self):
        spec = acceptance_spec()
        spec["servers"].append(dict(spec["servers"][0]))
        with self.assertRaises(rp.SpecError):
            rp.solve(spec)

    def test_rejects_unknown_failure_set_member(self):
        spec = acceptance_spec()
        spec["failure_sets"][0]["servers"].append("ghost")
        with self.assertRaises(rp.SpecError):
            rp.solve(spec)

    def test_rejects_negative_cost(self):
        spec = acceptance_spec()
        spec["servers"][0]["cost"] = -1
        with self.assertRaises(rp.SpecError):
            rp.solve(spec)


class VerifyTest(unittest.TestCase):
    def test_valid_certificate_verifies(self):
        spec = acceptance_spec()
        cert = rp.solve(spec)
        self.assertEqual(rp.verify(spec, cert), [])

    def test_valid_infeasible_verdict_verifies(self):
        spec = acceptance_spec()
        spec["servers"] = spec["servers"][:2]
        cert = rp.solve(spec)
        self.assertEqual(cert["status"], "infeasible")
        self.assertEqual(rp.verify(spec, cert), [])

    def test_tampered_cost_detected(self):
        spec = acceptance_spec()
        cert = rp.solve(spec)
        cert["total_cost"] = 1
        errors = rp.verify(spec, cert)
        self.assertTrue(any("cost" in e for e in errors), errors)

    def test_tampered_placement_detected(self):
        spec = acceptance_spec()
        cert = rp.solve(spec)
        cert["placement"] = ["s1", "s2"]  # entirely inside fs1
        errors = rp.verify(spec, cert)
        self.assertTrue(any("fs1" in e for e in errors), errors)

    def test_forged_infeasible_detected(self):
        spec = acceptance_spec()
        cert = rp.solve(spec)
        cert["status"] = "infeasible"
        cert["placement"] = None
        cert["total_cost"] = None
        cert.pop("failure_witnesses", None)
        errors = rp.verify(spec, cert)
        self.assertTrue(any("infeasible" in e for e in errors), errors)

    def test_tampered_statistics_detected(self):
        spec = acceptance_spec()
        cert = rp.solve(spec)
        cert["statistics"]["feasible"] = 999
        errors = rp.verify(spec, cert)
        self.assertTrue(any("statistics" in e for e in errors), errors)


class CliTest(unittest.TestCase):
    def _run(self, *args, stdin=None):
        return subprocess.run(
            [PYTHON, CLI, *args],
            input=stdin, capture_output=True, text=True, timeout=60)

    def test_solve_and_verify_roundtrip(self):
        spec = acceptance_spec()
        with tempfile.TemporaryDirectory() as td:
            spec_p = Path(td, "spec.json")
            cert_p = Path(td, "cert.json")
            spec_p.write_text(json.dumps(spec), encoding="utf-8")

            proc = self._run("solve", str(spec_p), "-o", str(cert_p))
            self.assertEqual(proc.returncode, 0, proc.stderr)
            cert = json.loads(cert_p.read_text(encoding="utf-8"))
            self.assertEqual(cert["status"], "optimal")

            proc = self._run("verify", str(spec_p), str(cert_p))
            self.assertEqual(proc.returncode, 0, proc.stderr)
            self.assertIn("verified", proc.stdout)

    def test_solve_infeasible_exit_code_1_with_stats(self):
        spec = acceptance_spec()
        spec["servers"] = spec["servers"][:2]
        proc = self._run("solve", "-", stdin=json.dumps(spec))
        self.assertEqual(proc.returncode, 1, proc.stderr)
        cert = json.loads(proc.stdout)
        self.assertEqual(cert["status"], "infeasible")
        self.assertEqual(cert["statistics"]["feasible"], 0)

    def test_invalid_spec_exit_code_2(self):
        proc = self._run("solve", "-", stdin=json.dumps({"r": 1, "q": 2,
                                                         "servers": []}))
        self.assertEqual(proc.returncode, 2)
        self.assertIn("invalid spec", proc.stderr)

    def test_verify_rejects_tampered_file(self):
        spec = acceptance_spec()
        cert = rp.solve(spec)
        cert["total_cost"] = 0
        with tempfile.TemporaryDirectory() as td:
            spec_p = Path(td, "spec.json")
            cert_p = Path(td, "cert.json")
            spec_p.write_text(json.dumps(spec), encoding="utf-8")
            cert_p.write_text(json.dumps(cert), encoding="utf-8")
            proc = self._run("verify", str(spec_p), str(cert_p))
            self.assertEqual(proc.returncode, 1)
            self.assertIn("CERTIFICATE INVALID", proc.stderr)


if __name__ == "__main__":
    unittest.main()
