"""Unit tests for the versioned time-interval authorization engine.

All data in these tests is synthetic.
"""

import json
import os
import subprocess
import sys
import tempfile
import unittest
from datetime import datetime, timezone

from authz import (
    BEGINNING_OF_TIME,
    EFFECT_ALLOW,
    EFFECT_DENY,
    Policy,
    Rule,
    earliest_difference,
    normalize_path,
    parse_time,
    prefix_matches,
)

T0 = datetime(2026, 1, 1, tzinfo=timezone.utc)
T1 = datetime(2026, 2, 1, tzinfo=timezone.utc)
T2 = datetime(2026, 3, 1, tzinfo=timezone.utc)
T3 = datetime(2026, 4, 1, tzinfo=timezone.utc)


def rule(rule_id, effect, prefix, subject="*", action="read", start=None, end=None):
    return Rule(rule_id=rule_id, effect=effect, subject=subject, action=action,
                prefix=normalize_path(prefix), start=start, end=end)


class NormalizePathTests(unittest.TestCase):
    def test_duplicate_separators_collapse(self):
        self.assertEqual(normalize_path("/root//a///b"), "/root/a/b")

    def test_dot_segments_removed(self):
        self.assertEqual(normalize_path("/root/./a/./b"), "/root/a/b")

    def test_dotdot_pops_parent(self):
        self.assertEqual(normalize_path("/root/a/../private/x"), "/root/private/x")

    def test_cannot_escape_root(self):
        self.assertEqual(normalize_path("/../../etc/passwd"), "/etc/passwd")
        self.assertEqual(normalize_path("/root/../../../x"), "/x")
        self.assertEqual(normalize_path(".."), "/")

    def test_trailing_slash_and_root(self):
        self.assertEqual(normalize_path("/root/"), "/root")
        self.assertEqual(normalize_path("/"), "/")
        self.assertEqual(normalize_path("//"), "/")

    def test_relative_path_treated_as_rooted(self):
        self.assertEqual(normalize_path("root/a"), "/root/a")

    def test_empty_path_rejected(self):
        with self.assertRaises(ValueError):
            normalize_path("")
        with self.assertRaises(ValueError):
            normalize_path("   ")


class PrefixMatchTests(unittest.TestCase):
    def test_segment_boundary(self):
        self.assertTrue(prefix_matches("/root/private", "/root/private"))
        self.assertTrue(prefix_matches("/root/private", "/root/private/x"))
        self.assertFalse(prefix_matches("/root/private", "/root/privatex"))
        self.assertFalse(prefix_matches("/root/private", "/root"))

    def test_root_prefix_matches_everything(self):
        self.assertTrue(prefix_matches("/", "/anything/at/all"))


class EvaluationTests(unittest.TestCase):
    def setUp(self):
        # Acceptance scenario: allow /root/* read, deny /root/private/* read.
        self.policy = Policy(version="v1", rules=(
            rule("allow-root", EFFECT_ALLOW, "/root"),
            rule("deny-private", EFFECT_DENY, "/root/private"),
        ))

    def test_allow_under_root(self):
        rec = self.policy.evaluate("alice", "/root/public/x", "read", T1)
        self.assertEqual(rec.decision, EFFECT_ALLOW)
        self.assertEqual(rec.matched_allow, ("allow-root",))

    def test_deny_overrides_allow_for_private(self):
        rec = self.policy.evaluate("alice", "/root/private/x", "read", T1)
        self.assertEqual(rec.decision, EFFECT_DENY)
        self.assertEqual(rec.matched_deny, ("deny-private",))
        self.assertEqual(rec.matched_allow, ("allow-root",))

    def test_traversal_cannot_bypass_deny(self):
        rec = self.policy.evaluate("alice", "/root/a/../private/x", "read", T1)
        self.assertEqual(rec.resource, "/root/private/x")
        self.assertEqual(rec.decision, EFFECT_DENY)

    def test_traversal_above_root_cannot_bypass(self):
        rec = self.policy.evaluate("alice", "/root/../../root/private/x", "read", T1)
        self.assertEqual(rec.resource, "/root/private/x")
        self.assertEqual(rec.decision, EFFECT_DENY)

    def test_default_deny_without_allow(self):
        rec = self.policy.evaluate("alice", "/elsewhere/x", "read", T1)
        self.assertEqual(rec.decision, EFFECT_DENY)
        self.assertEqual(rec.reason, "no matching allow rule (default deny)")

    def test_action_mismatch_default_deny(self):
        rec = self.policy.evaluate("alice", "/root/public/x", "write", T1)
        self.assertEqual(rec.decision, EFFECT_DENY)

    def test_audit_record_pins_version(self):
        rec = self.policy.evaluate("alice", "/root/public/x", "read", T1)
        self.assertEqual(rec.version, "v1")
        self.assertEqual(rec.to_dict()["version"], "v1")

    def test_time_interval_enforced(self):
        timed = Policy(version="v2", rules=(
            rule("window", EFFECT_ALLOW, "/root", start=T1, end=T2),
        ))
        self.assertEqual(timed.evaluate("a", "/root/x", "read", T0).decision, EFFECT_DENY)
        self.assertEqual(timed.evaluate("a", "/root/x", "read", T1).decision, EFFECT_ALLOW)
        self.assertEqual(timed.evaluate("a", "/root/x", "read", T2).decision, EFFECT_DENY)

    def test_subject_wildcard_and_exact(self):
        policy = Policy(version="v3", rules=(
            rule("alice-only", EFFECT_ALLOW, "/root", subject="alice"),
        ))
        self.assertEqual(policy.evaluate("alice", "/root/x", "read", T1).decision, EFFECT_ALLOW)
        self.assertEqual(policy.evaluate("bob", "/root/x", "read", T1).decision, EFFECT_DENY)

    def test_invalid_rule_rejected(self):
        with self.assertRaises(ValueError):
            Rule("bad", "maybe", "*", "*", "/root", None, None)
        with self.assertRaises(ValueError):
            Rule("empty", "allow", "*", "*", "/root", T2, T1)


class ParseTimeTests(unittest.TestCase):
    def test_iso8601_with_z(self):
        self.assertEqual(parse_time("2026-01-01T00:00:00Z"),
                         datetime(2026, 1, 1, tzinfo=timezone.utc))

    def test_naive_assumed_utc(self):
        self.assertEqual(parse_time("2026-01-01T00:00:00").tzinfo, timezone.utc)

    def test_none_is_unbounded(self):
        self.assertIsNone(parse_time(None))


class EarliestDifferenceTests(unittest.TestCase):
    def test_finds_earliest_divergence_and_witness(self):
        # v1 allows /root read from T1; v2 allows it only from T2.
        v1 = Policy(version="v1", rules=(rule("a", EFFECT_ALLOW, "/root", start=T1),))
        v2 = Policy(version="v2", rules=(rule("a", EFFECT_ALLOW, "/root", start=T2),))
        result = earliest_difference(v1, v2, resources=["/root/x", "/root/y"])
        self.assertIsNotNone(result)
        self.assertTrue(result["found"])
        self.assertEqual(result["time"], T1.isoformat())
        self.assertEqual(result["decision_a"], EFFECT_ALLOW)
        self.assertEqual(result["decision_b"], EFFECT_DENY)
        self.assertEqual(result["version_a"], "v1")
        self.assertEqual(result["version_b"], "v2")
        witness = result["request"]
        self.assertIn(witness["resource"], ("/root/x", "/root/y"))
        # The witness must actually reproduce the divergence.
        moment = parse_time(witness["time"])
        self.assertEqual(v1.evaluate(witness["subject"], witness["resource"],
                                     witness["action"], moment).decision, EFFECT_ALLOW)
        self.assertEqual(v2.evaluate(witness["subject"], witness["resource"],
                                     witness["action"], moment).decision, EFFECT_DENY)

    def test_difference_before_all_boundaries(self):
        # v1 has an unbounded allow, v2 nothing: they differ from the
        # beginning of time, reported via the BEGINNING_OF_TIME sentinel.
        v1 = Policy(version="v1", rules=(rule("a", EFFECT_ALLOW, "/root"),))
        v2 = Policy(version="v2", rules=())
        result = earliest_difference(v1, v2, resources=["/root/x"])
        self.assertTrue(result["found"])
        self.assertEqual(result["time"], BEGINNING_OF_TIME.isoformat())

    def test_equivalent_versions_report_no_difference(self):
        v1 = Policy(version="v1", rules=(rule("a", EFFECT_ALLOW, "/root"),))
        v2 = Policy(version="v2", rules=(rule("b", EFFECT_ALLOW, "/root"),))
        self.assertIsNone(earliest_difference(v1, v2, resources=["/root/x"]))

    def test_deny_expiry_creates_divergence_at_boundary(self):
        # v1 denies /root/private read during [T1, T2); v2 has no deny.
        allow = rule("allow", EFFECT_ALLOW, "/root")
        v1 = Policy(version="v1", rules=(allow, rule("deny", EFFECT_DENY, "/root/private",
                                                     start=T1, end=T2)))
        v2 = Policy(version="v2", rules=(allow,))
        result = earliest_difference(v1, v2, resources=["/root/private/x"])
        self.assertTrue(result["found"])
        self.assertEqual(result["time"], T1.isoformat())
        self.assertEqual(result["decision_a"], EFFECT_DENY)
        self.assertEqual(result["decision_b"], EFFECT_ALLOW)

    def test_resources_normalized_before_comparison(self):
        v1 = Policy(version="v1", rules=(rule("a", EFFECT_ALLOW, "/root"),))
        v2 = Policy(version="v2", rules=())
        result = earliest_difference(v1, v2, resources=["/root//a/../x"])
        self.assertTrue(result["found"])
        self.assertEqual(result["request"]["resource"], "/root/x")

    def test_empty_resource_set_rejected(self):
        v1 = Policy(version="v1", rules=())
        with self.assertRaises(ValueError):
            earliest_difference(v1, v1, resources=[])


class CliTests(unittest.TestCase):
    """End-to-end tests of the command line interface (synthetic data)."""

    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.policy_path = os.path.join(self.tmp.name, "policy.json")
        with open(self.policy_path, "w", encoding="utf-8") as fh:
            json.dump({
                "version": "v1",
                "rules": [
                    {"id": "allow-root", "effect": "allow", "subject": "*",
                     "action": "read", "prefix": "/root",
                     "start": None, "end": None},
                    {"id": "deny-private", "effect": "deny", "subject": "*",
                     "action": "read", "prefix": "/root/private",
                     "start": None, "end": None},
                ],
            }, fh)

    def run_cli(self, *argv):
        return subprocess.run(
            [sys.executable, "authz.py", *argv],
            capture_output=True, text=True,
            cwd=os.path.dirname(os.path.abspath(__file__)),
        )

    def test_check_allow_exit_0(self):
        proc = self.run_cli("check", "--policy", self.policy_path,
                            "--subject", "alice", "--resource", "/root/public/x",
                            "--action", "read", "--time", "2026-02-01T00:00:00Z")
        self.assertEqual(proc.returncode, 0, proc.stderr)
        out = json.loads(proc.stdout)
        self.assertEqual(out["decision"], "allow")
        self.assertEqual(out["version"], "v1")

    def test_check_deny_exit_1_and_traversal_blocked(self):
        proc = self.run_cli("check", "--policy", self.policy_path,
                            "--subject", "alice", "--resource", "/root/a/../private/x",
                            "--action", "read")
        self.assertEqual(proc.returncode, 1, proc.stderr)
        out = json.loads(proc.stdout)
        self.assertEqual(out["decision"], "deny")
        self.assertEqual(out["resource"], "/root/private/x")
        self.assertEqual(out["matched_deny"], ["deny-private"])

    def test_diff_found(self):
        v2_path = os.path.join(self.tmp.name, "v2.json")
        with open(v2_path, "w", encoding="utf-8") as fh:
            json.dump({"version": "v2", "rules": [
                {"id": "allow-root", "effect": "allow", "subject": "*",
                 "action": "read", "prefix": "/root",
                 "start": "2026-03-01T00:00:00Z", "end": None},
            ]}, fh)
        proc = self.run_cli("diff", "--policy-a", self.policy_path,
                            "--policy-b", v2_path, "--resources", "/root/public/x")
        self.assertEqual(proc.returncode, 0, proc.stderr)
        out = json.loads(proc.stdout)
        self.assertTrue(out["found"])
        # /root/public/x: v1 allows unbounded, v2 allows only from 2026-03-01,
        # so the earliest divergence is v1=allow vs v2=deny.
        self.assertEqual(out["decision_a"], "allow")
        self.assertEqual(out["decision_b"], "deny")

    def test_diff_not_found_exit_1(self):
        proc = self.run_cli("diff", "--policy-a", self.policy_path,
                            "--policy-b", self.policy_path,
                            "--resources", "/root/private/x")
        self.assertEqual(proc.returncode, 1)
        self.assertFalse(json.loads(proc.stdout)["found"])


if __name__ == "__main__":
    unittest.main()
