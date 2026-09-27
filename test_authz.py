"""Unittest suite for authz. All data is synthetic."""

import json
import os
import subprocess
import sys
import tempfile
import unittest
from datetime import datetime, timezone

import authz

T0 = datetime(2026, 1, 1, tzinfo=timezone.utc)
T1 = datetime(2026, 2, 1, tzinfo=timezone.utc)
T2 = datetime(2026, 3, 1, tzinfo=timezone.utc)

ACCEPTANCE_POLICY = {
    "version": "v1",
    "rules": [
        {
            "id": "r-allow-root",
            "effect": "allow",
            "subject": "*",
            "resource_prefix": "/root/*",
            "action": "read",
            "valid_from": None,
            "valid_to": None,
        },
        {
            "id": "r-deny-private",
            "effect": "deny",
            "subject": "*",
            "resource_prefix": "/root/private/*",
            "action": "read",
            "valid_from": None,
            "valid_to": None,
        },
    ],
}


def make_policy(data=None):
    return authz.Policy.from_dict(data if data is not None else ACCEPTANCE_POLICY)


class NormalizePathTests(unittest.TestCase):
    def test_duplicate_separators_and_dot_segments(self):
        self.assertEqual(authz.normalize_path("//root//a/./b//"), "/root/a/b")

    def test_dotdot_resolution(self):
        self.assertEqual(authz.normalize_path("/root/a/../private/x"),
                         "/root/private/x")

    def test_escape_above_root_rejected(self):
        with self.assertRaises(authz.PathEscapesRoot):
            authz.normalize_path("/../etc/passwd")
        with self.assertRaises(authz.PathEscapesRoot):
            authz.normalize_path("/root/../../etc")

    def test_relative_path_rejected(self):
        with self.assertRaises(ValueError):
            authz.normalize_path("root/a")

    def test_root_itself(self):
        self.assertEqual(authz.normalize_path("/"), "/")
        self.assertEqual(authz.normalize_path("/./"), "/")


class AcceptanceTests(unittest.TestCase):
    def setUp(self):
        self.policy = make_policy()

    def test_private_denied(self):
        record = authz.decide(self.policy, "alice", "/root/private/secret",
                              "read", T0)
        self.assertEqual(record["decision"], "deny")
        self.assertEqual(record["reason"], "explicit_deny")
        self.assertEqual(record["matched_rules"][0]["id"], "r-deny-private")

    def test_dotdot_cannot_bypass_deny(self):
        record = authz.decide(self.policy, "alice", "/root/a/../private/x",
                              "read", T0)
        self.assertEqual(record["decision"], "deny")
        self.assertEqual(record["request"]["normalized_resource"],
                         "/root/private/x")

    def test_public_read_allowed(self):
        record = authz.decide(self.policy, "alice", "/root/public/doc",
                              "read", T0)
        self.assertEqual(record["decision"], "allow")

    def test_outside_root_implicitly_denied(self):
        record = authz.decide(self.policy, "alice", "/other/doc", "read", T0)
        self.assertEqual(record["decision"], "deny")
        self.assertEqual(record["reason"], "implicit_deny")

    def test_write_implicitly_denied(self):
        record = authz.decide(self.policy, "alice", "/root/public/doc",
                              "write", T0)
        self.assertEqual(record["decision"], "deny")

    def test_audit_pins_rule_version(self):
        record = authz.decide(self.policy, "alice", "/root/private/x",
                              "read", T0)
        self.assertEqual(record["policy_version"], "v1")
        for entry in record["matched_rules"]:
            self.assertEqual(entry["version"], "v1")


class IntervalTests(unittest.TestCase):
    def setUp(self):
        self.policy = make_policy({
            "version": "v-window",
            "rules": [
                {
                    "id": "r-window",
                    "effect": "allow",
                    "subject": "bob",
                    "resource_prefix": "/data/*",
                    "action": "read",
                    "valid_from": "2026-01-01T00:00:00Z",
                    "valid_to": "2026-02-01T00:00:00Z",
                }
            ],
        })

    def test_inside_interval_allowed(self):
        self.assertEqual(
            authz.decide(self.policy, "bob", "/data/x", "read", T0)["decision"],
            "allow")

    def test_before_interval_denied(self):
        before = datetime(2025, 12, 31, 23, 59, 59, tzinfo=timezone.utc)
        self.assertEqual(
            authz.decide(self.policy, "bob", "/data/x", "read", before)["decision"],
            "deny")

    def test_end_is_exclusive(self):
        self.assertEqual(
            authz.decide(self.policy, "bob", "/data/x", "read", T1)["decision"],
            "deny")


class DenyPriorityTests(unittest.TestCase):
    def test_deny_overrides_allow_same_scope(self):
        policy = make_policy({
            "version": "v-prio",
            "rules": [
                {"id": "a", "effect": "allow", "subject": "*",
                 "resource_prefix": "/x/*", "action": "read",
                 "valid_from": None, "valid_to": None},
                {"id": "d", "effect": "deny", "subject": "*",
                 "resource_prefix": "/x/*", "action": "read",
                 "valid_from": None, "valid_to": None},
            ],
        })
        record = authz.decide(policy, "anyone", "/x/y", "read", T0)
        self.assertEqual(record["decision"], "deny")
        self.assertEqual([m["id"] for m in record["matched_rules"]], ["d"])


class DiffTests(unittest.TestCase):
    def _policy(self, version, valid_to):
        return make_policy({
            "version": version,
            "rules": [
                {"id": "r1", "effect": "allow", "subject": "alice",
                 "resource_prefix": "/root/*", "action": "read",
                 "valid_from": "2026-01-01T00:00:00Z", "valid_to": valid_to},
            ],
        })

    def test_earliest_difference_and_witness(self):
        policy_a = self._policy("vA", "2026-02-01T00:00:00Z")
        policy_b = self._policy("vB", "2026-03-01T00:00:00Z")
        outcome = authz.earliest_difference(policy_a, policy_b, ["/root/doc"])
        self.assertIsNotNone(outcome)
        self.assertEqual(outcome["time"], "2026-02-01T00:00:00Z")
        self.assertEqual(outcome["witness"],
                         {"subject": "alice", "resource": "/root/doc",
                          "action": "read"})
        self.assertEqual(outcome["decisions"], {"vA": "deny", "vB": "allow"})

    def test_equivalent_versions(self):
        policy_a = self._policy("vA", "2026-02-01T00:00:00Z")
        policy_b = self._policy("vB", "2026-02-01T00:00:00Z")
        self.assertIsNone(
            authz.earliest_difference(policy_a, policy_b, ["/root/doc"]))

    def test_difference_from_added_rule(self):
        policy_a = make_policy({"version": "vA", "rules": []})
        policy_b = make_policy({
            "version": "vB",
            "rules": [
                {"id": "r1", "effect": "allow", "subject": "*",
                 "resource_prefix": "/root/*", "action": "read",
                 "valid_from": None, "valid_to": None},
            ],
        })
        outcome = authz.earliest_difference(policy_a, policy_b, ["/root/a"])
        self.assertIsNotNone(outcome)
        self.assertEqual(outcome["decisions"], {"vA": "deny", "vB": "allow"})

    def test_unbounded_past_difference_marked(self):
        policy_a = make_policy({
            "version": "vA",
            "rules": [
                {"id": "d", "effect": "deny", "subject": "*",
                 "resource_prefix": "/root/*", "action": "read",
                 "valid_from": None, "valid_to": None},
            ],
        })
        policy_b = make_policy({
            "version": "vB",
            "rules": [
                {"id": "a", "effect": "allow", "subject": "*",
                 "resource_prefix": "/root/*", "action": "read",
                 "valid_from": None, "valid_to": None},
                {"id": "d", "effect": "deny", "subject": "*",
                 "resource_prefix": "/root/*", "action": "read",
                 "valid_from": "2026-06-01T00:00:00Z", "valid_to": None},
            ],
        })
        outcome = authz.earliest_difference(policy_a, policy_b, ["/root/x"])
        self.assertIsNotNone(outcome)
        self.assertIn("note", outcome)
        self.assertEqual(outcome["decisions"], {"vA": "deny", "vB": "allow"})

    def test_witness_resource_normalized(self):
        policy_a = self._policy("vA", "2026-02-01T00:00:00Z")
        policy_b = self._policy("vB", "2026-03-01T00:00:00Z")
        outcome = authz.earliest_difference(policy_a, policy_b,
                                            ["/root//sub/../doc"])
        self.assertEqual(outcome["witness"]["resource"], "/root/doc")


class CliTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.policy_path = os.path.join(self.tmp.name, "policy.json")
        with open(self.policy_path, "w", encoding="utf-8") as handle:
            json.dump(ACCEPTANCE_POLICY, handle)

    def tearDown(self):
        self.tmp.cleanup()

    def _run(self, *argv):
        return subprocess.run(
            [sys.executable, "authz.py", *argv],
            capture_output=True, text=True,
            cwd=os.path.dirname(os.path.abspath(__file__)),
        )

    def test_authorize_allow_exit_0(self):
        proc = self._run("authorize", "--policy", self.policy_path,
                         "--subject", "alice", "--resource", "/root/pub/x",
                         "--action", "read", "--time", "2026-01-01T00:00:00Z")
        self.assertEqual(proc.returncode, 0, proc.stderr)
        self.assertEqual(json.loads(proc.stdout)["decision"], "allow")

    def test_authorize_deny_exit_1(self):
        proc = self._run("authorize", "--policy",
                         self.policy_path, "--subject", "alice",
                         "--resource", "/root/a/../private/x",
                         "--action", "read", "--time", "2026-01-01T00:00:00Z")
        self.assertEqual(proc.returncode, 1, proc.stderr)
        self.assertEqual(json.loads(proc.stdout)["decision"], "deny")

    def test_diff_exit_2_with_witness(self):
        policy_b = dict(ACCEPTANCE_POLICY)
        policy_b["version"] = "v2"
        policy_b["rules"] = [dict(rule) for rule in ACCEPTANCE_POLICY["rules"]]
        policy_b["rules"][1]["valid_to"] = "2026-06-01T00:00:00Z"
        path_b = os.path.join(self.tmp.name, "policy_b.json")
        with open(path_b, "w", encoding="utf-8") as handle:
            json.dump(policy_b, handle)
        resources = os.path.join(self.tmp.name, "resources.json")
        with open(resources, "w", encoding="utf-8") as handle:
            json.dump(["/root/private/x", "/root/pub/y"], handle)
        proc = self._run("diff", "--policy-a", self.policy_path,
                         "--policy-b", path_b, "--resources", resources)
        self.assertEqual(proc.returncode, 2, proc.stderr)
        payload = json.loads(proc.stdout)
        self.assertEqual(payload["result"], "different")
        diff = payload["earliest_difference"]
        self.assertEqual(diff["time"], "2026-06-01T00:00:00Z")
        self.assertEqual(diff["witness"]["resource"], "/root/private/x")
        self.assertEqual(diff["decisions"], {"v1": "deny", "v2": "allow"})


if __name__ == "__main__":
    unittest.main()
