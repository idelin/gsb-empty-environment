"""Unit tests for replica_placement. All data is synthetic."""

import json
import os
import subprocess
import sys
import tempfile
import unittest

import replica_placement as rp

PYTHON = sys.executable
HERE = os.path.dirname(os.path.abspath(__file__))
CLI = os.path.join(HERE, "replica_placement.py")


def make_instance(**overrides):
    base = {
        "servers": [
            {"id": "s1", "rack": "rk1", "zone": "z1", "capacity": 2, "cost": 1},
            {"id": "s2", "rack": "rk1", "zone": "z1", "capacity": 2, "cost": 1},
            {"id": "s3", "rack": "rk2", "zone": "z2", "capacity": 2, "cost": 10},
        ],
        "fault_sets": [{"id": "fs1", "servers": ["s1", "s2"]}],
        "r": 2,
        "q": 1,
        "shards": ["shard-a"],
    }
    base.update(overrides)
    return rp.load_instance(base)


class AcceptanceBoundaryTest(unittest.TestCase):
    """r=2, q=1: two servers in the same specified fault set."""

    def test_same_fault_set_pair_is_rejected(self):
        inst = make_instance()
        valid, stats = rp.enumerate_valid_placements(inst)
        combos = {combo for combo, _ in valid}
        # Placing both replicas on s1+s2 (same fault set) must NOT be valid.
        self.assertNotIn(("s1", "s2"), combos)
        self.assertEqual(stats["combinations_examined"], 3)
        self.assertEqual(stats["rejected_by_fault_rule"], 1)
        self.assertEqual(stats["accepted"], 2)

    def test_solution_must_use_server_outside_fault_set(self):
        inst = make_instance()
        cert = rp.solve(inst)
        self.assertEqual(cert["status"], "OPTIMAL")
        placement = set(cert["assignment"]["shard-a"])
        # At least one replica must sit outside the fault set {s1, s2}.
        self.assertTrue(placement - {"s1", "s2"})
        # Cheapest valid choice: one cheap server inside + the outside server s3.
        self.assertEqual(cert["total_cost"], 11)
        # Fault witness: s3 survives fs1, and 1 >= q.
        self.assertEqual(cert["witnesses"]["shard-a"]["fs1"], ["s3"])

    def test_witnesses_cover_every_fault_set(self):
        inst = make_instance(
            fault_sets=[
                {"id": "fs1", "servers": ["s1", "s2"]},
                {"id": "fs2", "servers": ["s3"]},
            ]
        )
        cert = rp.solve(inst)
        self.assertEqual(cert["status"], "OPTIMAL")
        for fid in ("fs1", "fs2"):
            survivors = cert["witnesses"]["shard-a"][fid]
            self.assertGreaterEqual(len(survivors), inst["q"])
            for sid in survivors:
                self.assertNotIn(sid, dict((f["id"], f["servers"]) for f in
                                           [{"id": "fs1", "servers": ["s1", "s2"]},
                                            {"id": "fs2", "servers": ["s3"]}])[fid])


class OptimalityTest(unittest.TestCase):
    def test_picks_cheapest_valid_combination(self):
        inst = make_instance()
        cert = rp.solve(inst)
        # Valid combos: {s1,s3}=11 and {s2,s3}=11; both optimal, cost 11.
        self.assertEqual(cert["total_cost"], 11)

    def test_tie_broken_deterministically(self):
        inst = make_instance()
        cert1 = rp.solve(inst)
        cert2 = rp.solve(inst)
        self.assertEqual(cert1["assignment"], cert2["assignment"])

    def test_optimality_matches_brute_force_over_all_combos(self):
        import itertools
        inst = make_instance(
            servers=[
                {"id": f"s{i}", "rack": f"rk{i % 2}", "zone": f"z{i % 3}",
                 "capacity": 3, "cost": c}
                for i, c in enumerate([4, 1, 7, 2, 9])
            ],
            fault_sets=[{"id": "fs1", "servers": ["s0", "s3"]}],
            r=3,
            q=2,
        )
        cert = rp.solve(inst)
        self.assertEqual(cert["status"], "OPTIMAL")
        best = None
        for combo in itertools.combinations(sorted(inst["servers"]), 3):
            if all(len(rp.surviving_replicas(combo, fs)) >= 2
                   for fs in inst["fault_sets"].values()):
                cost = sum(inst["servers"][s]["cost"] for s in combo)
                best = cost if best is None else min(best, cost)
        self.assertEqual(cert["total_cost"], best)


class CapacityTest(unittest.TestCase):
    def test_capacity_shared_across_shards(self):
        inst = make_instance(
            servers=[
                {"id": "s1", "rack": "rk1", "zone": "z1", "capacity": 1, "cost": 1},
                {"id": "s2", "rack": "rk2", "zone": "z1", "capacity": 1, "cost": 2},
                {"id": "s3", "rack": "rk3", "zone": "z2", "capacity": 1, "cost": 5},
                {"id": "s4", "rack": "rk4", "zone": "z2", "capacity": 1, "cost": 9},
            ],
            fault_sets=[{"id": "fs1", "servers": ["s1", "s3"]}],
            shards=["shard-a", "shard-b"],
        )
        cert = rp.solve(inst)
        self.assertEqual(cert["status"], "OPTIMAL")
        for sid, used in cert["capacity_usage"].items():
            self.assertLessEqual(used, inst["servers"][sid]["capacity"])
        # Cheapest joint assignment: {s1,s2} + {s2,s4}? capacity forbids reuse;
        # verified independently below via brute force.
        import itertools
        shards = inst["shards"]
        valid, _ = rp.enumerate_valid_placements(inst)
        best = None
        for combo_a in valid:
            for combo_b in valid:
                usage = {}
                for s in combo_a[0] + combo_b[0]:
                    usage[s] = usage.get(s, 0) + 1
                if all(u <= inst["servers"][s]["capacity"] for s, u in usage.items()):
                    total = combo_a[1] + combo_b[1]
                    best = total if best is None else min(best, total)
        self.assertEqual(cert["total_cost"], best)

    def test_infeasible_due_to_capacity_only(self):
        inst = make_instance(
            servers=[
                {"id": "s1", "rack": "rk1", "zone": "z1", "capacity": 1, "cost": 1},
                {"id": "s2", "rack": "rk2", "zone": "z1", "capacity": 1, "cost": 1},
            ],
            fault_sets=[],
            r=2,
            q=1,
            shards=["a", "b"],
        )
        cert = rp.solve(inst)
        self.assertEqual(cert["status"], "INFEASIBLE")
        self.assertIn("capacity", cert["reason"])
        # Each shard individually has a valid placement; joint capacity kills it.
        self.assertEqual(cert["exhaustive_stats"]["per_shard"]["a"]["accepted"], 1)
        self.assertGreater(cert["exhaustive_stats"]["joint"]["branches_pruned_by_capacity"], 0)


class InfeasibilityTest(unittest.TestCase):
    def test_all_servers_in_one_fault_set(self):
        inst = make_instance(
            fault_sets=[{"id": "fs-all", "servers": ["s1", "s2", "s3"]}],
        )
        cert = rp.solve(inst)
        self.assertEqual(cert["status"], "INFEASIBLE")
        self.assertNotIn("assignment", cert)
        stats = cert["exhaustive_stats"]["per_shard"]["shard-a"]
        self.assertEqual(stats["combinations_examined"], 3)
        self.assertEqual(stats["accepted"], 0)
        self.assertEqual(stats["rejected_by_fault_rule"], 3)

    def test_r_exceeds_server_count(self):
        inst = make_instance(r=4, q=1)
        cert = rp.solve(inst)
        self.assertEqual(cert["status"], "INFEASIBLE")
        self.assertEqual(
            cert["exhaustive_stats"]["per_shard"]["shard-a"]["combinations_examined"], 0
        )

    def test_infeasible_stats_are_reproducible(self):
        inst = make_instance(
            fault_sets=[{"id": "fs-all", "servers": ["s1", "s2", "s3"]}],
        )
        cert1 = rp.solve(inst)
        cert2 = rp.solve(inst)
        self.assertEqual(cert1["exhaustive_stats"], cert2["exhaustive_stats"])


class VerificationTest(unittest.TestCase):
    def test_valid_certificate_passes(self):
        inst = make_instance()
        cert = rp.solve(inst)
        errors = rp.verify_certificate(inst, cert, check_optimality=True)
        self.assertEqual(errors, [])

    def test_infeasible_certificate_passes(self):
        inst = make_instance(
            fault_sets=[{"id": "fs-all", "servers": ["s1", "s2", "s3"]}],
        )
        cert = rp.solve(inst)
        errors = rp.verify_certificate(inst, cert, check_optimality=True)
        self.assertEqual(errors, [])

    def test_forged_certificate_rejected(self):
        inst = make_instance()
        forged = {
            "status": "OPTIMAL",
            "total_cost": 5,
            "assignment": {"shard-a": ["s1", "s2"]},
            "witnesses": {"shard-a": {"fs1": ["s1", "s2"]}},
        }
        errors = rp.verify_certificate(inst, forged)
        self.assertTrue(any("fs1" in e for e in errors))
        self.assertTrue(any("total_cost" in e for e in errors))

    def test_suboptimal_certificate_detected_with_flag(self):
        inst = make_instance()
        cert = rp.solve(inst)
        cert["total_cost"] += 100  # lie about the cost
        errors = rp.verify_certificate(inst, cert, check_optimality=True)
        self.assertTrue(errors)

    def test_capacity_violation_detected(self):
        inst = make_instance(
            servers=[
                {"id": "s1", "rack": "rk1", "zone": "z1", "capacity": 1, "cost": 1},
                {"id": "s2", "rack": "rk2", "zone": "z1", "capacity": 1, "cost": 1},
            ],
            fault_sets=[],
            r=1,
            q=0,
            shards=["a", "b"],
        )
        forged = {
            "status": "OPTIMAL",
            "total_cost": 2,
            "assignment": {"a": ["s1"], "b": ["s1"]},
        }
        errors = rp.verify_certificate(inst, forged)
        self.assertTrue(any("capacity" in e for e in errors))


class DerivedFaultSetTest(unittest.TestCase):
    def test_derive_from_rack_and_zone(self):
        inst = make_instance(derive_fault_sets=["rack", "zone"])
        self.assertIn("rack:rk1", inst["fault_sets"])
        self.assertIn("zone:z1", inst["fault_sets"])
        self.assertEqual(inst["fault_sets"]["rack:rk1"], frozenset({"s1", "s2"}))

    def test_derived_zone_constraint_enforced(self):
        # With zone-derived fault sets, replicas must span zones.
        inst = make_instance(
            fault_sets=[],
            derive_fault_sets=["zone"],
        )
        cert = rp.solve(inst)
        self.assertEqual(cert["status"], "OPTIMAL")
        zones = {inst["servers"][s]["zone"] for s in cert["assignment"]["shard-a"]}
        self.assertEqual(len(zones), 2)


class InputValidationTest(unittest.TestCase):
    def test_rejects_duplicate_server_ids(self):
        with self.assertRaises(rp.InputError):
            rp.load_instance({
                "servers": [
                    {"id": "s1", "capacity": 1, "cost": 1},
                    {"id": "s1", "capacity": 1, "cost": 1},
                ],
                "r": 1, "q": 0,
            })

    def test_rejects_unknown_fault_set_member(self):
        with self.assertRaises(rp.InputError):
            rp.load_instance({
                "servers": [{"id": "s1", "capacity": 1, "cost": 1}],
                "fault_sets": [{"id": "fs", "servers": ["ghost"]}],
                "r": 1, "q": 0,
            })

    def test_rejects_q_greater_than_r(self):
        with self.assertRaises(rp.InputError):
            rp.load_instance({
                "servers": [{"id": "s1", "capacity": 1, "cost": 1}],
                "r": 1, "q": 2,
            })


class CliTest(unittest.TestCase):
    def _run_cli(self, *args):
        return subprocess.run(
            [PYTHON, CLI, *args],
            capture_output=True, text=True, cwd=HERE,
        )

    def test_solve_and_verify_roundtrip(self):
        with tempfile.TemporaryDirectory() as tmp:
            inst_path = os.path.join(tmp, "inst.json")
            cert_path = os.path.join(tmp, "cert.json")
            with open(inst_path, "w", encoding="utf-8") as fh:
                json.dump({
                    "servers": [
                        {"id": "s1", "rack": "rk1", "zone": "z1", "capacity": 2, "cost": 1},
                        {"id": "s2", "rack": "rk1", "zone": "z1", "capacity": 2, "cost": 1},
                        {"id": "s3", "rack": "rk2", "zone": "z2", "capacity": 2, "cost": 10},
                    ],
                    "fault_sets": [{"id": "fs1", "servers": ["s1", "s2"]}],
                    "r": 2, "q": 1, "shards": ["shard-a"],
                }, fh)
            proc = self._run_cli("solve", inst_path, "-o", cert_path)
            self.assertEqual(proc.returncode, 0, proc.stderr)
            with open(cert_path, encoding="utf-8") as fh:
                cert = json.load(fh)
            self.assertEqual(cert["status"], "OPTIMAL")
            self.assertEqual(cert["total_cost"], 11)

            proc = self._run_cli("verify", inst_path, cert_path, "--check-optimality")
            self.assertEqual(proc.returncode, 0, proc.stderr)
            self.assertIn("VALID", proc.stdout)

    def test_cli_exit_code_1_on_infeasible(self):
        with tempfile.TemporaryDirectory() as tmp:
            inst_path = os.path.join(tmp, "inst.json")
            with open(inst_path, "w", encoding="utf-8") as fh:
                json.dump({
                    "servers": [{"id": "s1", "capacity": 1, "cost": 1}],
                    "fault_sets": [{"id": "fs", "servers": ["s1"]}],
                    "r": 1, "q": 1,
                }, fh)
            proc = self._run_cli("solve", inst_path)
            self.assertEqual(proc.returncode, 1)
            cert = json.loads(proc.stdout)
            self.assertEqual(cert["status"], "INFEASIBLE")
            self.assertIn("exhaustive_stats", cert)

    def test_cli_rejects_malformed_input(self):
        with tempfile.TemporaryDirectory() as tmp:
            inst_path = os.path.join(tmp, "bad.json")
            with open(inst_path, "w", encoding="utf-8") as fh:
                fh.write("{not json")
            proc = self._run_cli("solve", inst_path)
            self.assertEqual(proc.returncode, 2)

    def test_cli_verify_detects_forgery(self):
        with tempfile.TemporaryDirectory() as tmp:
            inst_path = os.path.join(tmp, "inst.json")
            cert_path = os.path.join(tmp, "cert.json")
            with open(inst_path, "w", encoding="utf-8") as fh:
                json.dump({
                    "servers": [
                        {"id": "s1", "capacity": 2, "cost": 1},
                        {"id": "s2", "capacity": 2, "cost": 1},
                    ],
                    "fault_sets": [{"id": "fs1", "servers": ["s1", "s2"]}],
                    "r": 2, "q": 1,
                }, fh)
            with open(cert_path, "w", encoding="utf-8") as fh:
                json.dump({
                    "status": "OPTIMAL",
                    "total_cost": 2,
                    "assignment": {"shard-0": ["s1", "s2"]},
                }, fh)
            proc = self._run_cli("verify", inst_path, cert_path)
            self.assertEqual(proc.returncode, 1)
            self.assertIn("INVALID", proc.stderr)


if __name__ == "__main__":
    unittest.main()
