"""Unit tests for lease_revoke. All data is synthetic."""
import json
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

import lease_revoke as lr

HERE = Path(__file__).resolve().parent


def make_tree(leases):
    return lr.build_tree(leases)


def acceptance_leases():
    """Parent quota 5 with two concurrent children of quota 3 and 2."""
    return [
        {"id": "root", "parent": None, "start": 0, "end": 100,
         "quota": 5, "weight": 0},
        {"id": "heavy", "parent": "root", "start": 0, "end": 100,
         "quota": 3, "weight": 10},
        {"id": "light", "parent": "root", "start": 0, "end": 100,
         "quota": 2, "weight": 1},
    ]


class AcceptanceBoundaryTest(unittest.TestCase):
    """The acceptance boundary from the specification."""

    def test_reduce_quota_revokes_two_quota_child(self):
        tree = make_tree(acceptance_leases())
        result = lr.apply_operation(tree, {"lease": "root", "quota": 3})
        # Weight 10 (quota 3) vs weight 1 (quota 2): minimum loss revokes
        # the quota-2 child; the remaining load is exactly 3 <= 3.
        self.assertEqual(result["revoked"], ["light"])
        self.assertEqual(result["loss"], 1)
        self.assertTrue(result["valid"])

    def test_result_never_exceeds_new_quota(self):
        tree = make_tree(acceptance_leases())
        result = lr.apply_operation(tree, {"lease": "root", "quota": 3})
        remaining = [
            l for l in tree.leases.values() if l.parent == "root"
        ]
        self.assertLessEqual(sum(l.quota for l in remaining), 3)
        self.assertEqual(sorted(l.id for l in remaining), ["heavy"])

    def test_reduce_to_zero_revokes_everything(self):
        tree = make_tree(acceptance_leases())
        result = lr.apply_operation(tree, {"lease": "root", "quota": 0})
        self.assertEqual(result["revoked"], ["heavy", "light"])
        self.assertEqual(result["loss"], 11)


class MinLossSelectionTest(unittest.TestCase):
    def test_prefers_single_light_loss_over_heavy_one(self):
        # Quota 5 -> 3: revoking the quota-3 child (weight 1) is cheaper
        # than revoking the quota-2 child (weight 2), even though the
        # revoked quota is larger.
        tree = make_tree([
            {"id": "root", "parent": None, "start": 0, "end": 10, "quota": 5},
            {"id": "a", "parent": "root", "start": 0, "end": 10,
             "quota": 3, "weight": 1},
            {"id": "b", "parent": "root", "start": 0, "end": 10,
             "quota": 2, "weight": 2},
        ])
        result = lr.apply_operation(tree, {"lease": "root", "quota": 3})
        self.assertEqual(result["revoked"], ["a"])
        self.assertEqual(result["loss"], 1)

    def test_tie_breaks_by_lexicographic_id_sequence(self):
        # Two symmetric children, equal quota and weight: both {"a"} and
        # {"b"} have loss 1; the lexicographically smaller sequence wins.
        tree = make_tree([
            {"id": "root", "parent": None, "start": 0, "end": 10, "quota": 4},
            {"id": "b", "parent": "root", "start": 0, "end": 10,
             "quota": 2, "weight": 1},
            {"id": "a", "parent": "root", "start": 0, "end": 10,
             "quota": 2, "weight": 1},
        ])
        result = lr.apply_operation(tree, {"lease": "root", "quota": 2})
        self.assertEqual(result["revoked"], ["a"])
        self.assertEqual(result["loss"], 1)

    def test_may_revoke_several_cheap_children(self):
        # Quota 6 -> 3 with children 3/2/1 (weights 5/1/1): revoking the
        # two cheap children (loss 2) beats revoking the heavy one (loss 5).
        tree = make_tree([
            {"id": "root", "parent": None, "start": 0, "end": 10, "quota": 6},
            {"id": "big", "parent": "root", "start": 0, "end": 10,
             "quota": 3, "weight": 5},
            {"id": "mid", "parent": "root", "start": 0, "end": 10,
             "quota": 2, "weight": 1},
            {"id": "small", "parent": "root", "start": 0, "end": 10,
             "quota": 1, "weight": 1},
        ])
        result = lr.apply_operation(tree, {"lease": "root", "quota": 3})
        self.assertEqual(result["revoked"], ["mid", "small"])
        self.assertEqual(result["loss"], 2)

    def test_no_revocation_when_still_feasible(self):
        tree = make_tree(acceptance_leases())
        result = lr.apply_operation(tree, {"lease": "root", "quota": 5})
        self.assertEqual(result["revoked"], [])
        self.assertEqual(result["loss"], 0)


class TimeIntervalTest(unittest.TestCase):
    def test_child_outside_shortened_interval_is_revoked(self):
        tree = make_tree([
            {"id": "root", "parent": None, "start": 0, "end": 100,
             "quota": 10},
            {"id": "inside", "parent": "root", "start": 0, "end": 50,
             "quota": 4, "weight": 1},
            {"id": "outside", "parent": "root", "start": 60, "end": 100,
             "quota": 4, "weight": 100},
        ])
        # Capacity is fine either way; containment forces the revocation.
        result = lr.apply_operation(
            tree, {"lease": "root", "start": 0, "end": 50}
        )
        self.assertEqual(result["revoked"], ["outside"])
        self.assertEqual(result["loss"], 100)

    def test_non_concurrent_children_may_exceed_quota_over_time(self):
        # 3 + 3 > 5, but the children never overlap in time: valid.
        tree = make_tree([
            {"id": "root", "parent": None, "start": 0, "end": 100,
             "quota": 5},
            {"id": "first", "parent": "root", "start": 0, "end": 50,
             "quota": 3, "weight": 1},
            {"id": "second", "parent": "root", "start": 50, "end": 100,
             "quota": 3, "weight": 1},
        ])
        result = lr.apply_operation(tree, {"lease": "root", "quota": 5})
        self.assertEqual(result["revoked"], [])

    def test_partial_overlap_only_conflicts_during_overlap(self):
        # a: [0, 60) q3, b: [40, 100) q3, parent quota 6 -> conflict on
        # [40, 60). Reducing quota to 3 forces one revocation; weights tie,
        # lexicographic order picks "a".
        tree = make_tree([
            {"id": "root", "parent": None, "start": 0, "end": 100,
             "quota": 6},
            {"id": "a", "parent": "root", "start": 0, "end": 60,
             "quota": 3, "weight": 1},
            {"id": "b", "parent": "root", "start": 40, "end": 100,
             "quota": 3, "weight": 1},
        ])
        result = lr.apply_operation(tree, {"lease": "root", "quota": 3})
        self.assertEqual(result["revoked"], ["a"])
        self.assertEqual(result["loss"], 1)


class CascadeTest(unittest.TestCase):
    def deep_tree(self):
        return make_tree([
            {"id": "root", "parent": None, "start": 0, "end": 100,
             "quota": 5},
            {"id": "child", "parent": "root", "start": 0, "end": 100,
             "quota": 3, "weight": 10},
            {"id": "grand", "parent": "child", "start": 0, "end": 100,
             "quota": 2, "weight": 7},
            {"id": "great", "parent": "grand", "start": 0, "end": 100,
             "quota": 1, "weight": 4},
            {"id": "sibling", "parent": "root", "start": 0, "end": 100,
             "quota": 2, "weight": 1},
        ])

    def test_revocation_cascades_to_all_descendants(self):
        tree = self.deep_tree()
        result = lr.apply_operation(tree, {"lease": "root", "quota": 3})
        # Revoking "child" (subtree loss 10+7+4=21) vs "sibling" (loss 1):
        # minimum loss revokes the sibling only.
        self.assertEqual(result["revoked"], ["sibling"])
        self.assertEqual(result["loss"], 1)

    def test_cascade_counts_each_descendant_exactly_once(self):
        tree = self.deep_tree()
        # Force the cascade: shrink root so only "child" (quota 3) fits.
        result = lr.apply_operation(tree, {"lease": "root", "quota": 3})
        self.assertEqual(result["revoked"], ["sibling"])
        # Now shrink "child" to quota 1: "grand" (q2) no longer fits and
        # its subtree is revoked with it. Loss = 7 + 4, counted once each.
        result = lr.apply_operation(tree, {"lease": "child", "quota": 1})
        self.assertEqual(result["revoked"], ["grand", "great"])
        self.assertEqual(result["loss"], 11)
        self.assertEqual(sorted(tree.leases), ["child", "root"])

    def test_cascade_loss_includes_descendants_once(self):
        # Revoking "child" cascades to "grand" and "great"; the loss is
        # 10 + 7 + 4 = 21 (each once), and it is still cheaper than any
        # alternative here because "sibling" alone cannot satisfy quota 1.
        tree = self.deep_tree()
        result = lr.apply_operation(tree, {"lease": "root", "quota": 1})
        # Options: revoke child (loss 21, sibling q2 remains > 1 -> also
        # needs sibling revoked) or revoke both. Minimum is revoking the
        # whole subtree plus sibling: 21 + 1 = 22... verify exact set.
        self.assertEqual(
            result["revoked"], ["child", "grand", "great", "sibling"]
        )
        self.assertEqual(result["loss"], 22)
        self.assertEqual(len(result["revoked"]), len(set(result["revoked"])))


class ValidationTest(unittest.TestCase):
    def test_rejects_child_outside_parent_interval(self):
        with self.assertRaises(lr.LeaseError):
            make_tree([
                {"id": "root", "parent": None, "start": 0, "end": 10,
                 "quota": 5},
                {"id": "bad", "parent": "root", "start": 5, "end": 15,
                 "quota": 1},
            ])

    def test_rejects_concurrent_over_quota(self):
        with self.assertRaises(lr.LeaseError):
            make_tree([
                {"id": "root", "parent": None, "start": 0, "end": 10,
                 "quota": 5},
                {"id": "a", "parent": "root", "start": 0, "end": 10,
                 "quota": 3},
                {"id": "b", "parent": "root", "start": 0, "end": 10,
                 "quota": 3},
            ])

    def test_rejects_unknown_parent_and_duplicates(self):
        with self.assertRaises(lr.LeaseError):
            make_tree([
                {"id": "root", "parent": None, "start": 0, "end": 10,
                 "quota": 5},
                {"id": "x", "parent": "ghost", "start": 0, "end": 5,
                 "quota": 1},
            ])
        with self.assertRaises(lr.LeaseError):
            make_tree([
                {"id": "root", "parent": None, "start": 0, "end": 10,
                 "quota": 5},
                {"id": "root", "parent": None, "start": 0, "end": 5,
                 "quota": 1},
            ])

    def test_rejects_operation_outside_parent(self):
        tree = make_tree([
            {"id": "root", "parent": None, "start": 0, "end": 10, "quota": 5},
            {"id": "kid", "parent": "root", "start": 0, "end": 10,
             "quota": 3},
        ])
        with self.assertRaises(lr.LeaseError):
            lr.apply_operation(tree, {"lease": "kid", "end": 20})

    def test_rejects_quota_increase_that_overloads_parent(self):
        tree = make_tree([
            {"id": "root", "parent": None, "start": 0, "end": 10, "quota": 5},
            {"id": "kid", "parent": "root", "start": 0, "end": 10,
             "quota": 3},
        ])
        with self.assertRaises(lr.LeaseError):
            lr.apply_operation(tree, {"lease": "kid", "quota": 6})


class CliTest(unittest.TestCase):
    def run_cli(self, payload):
        with tempfile.NamedTemporaryFile(
            "w", suffix=".json", delete=False
        ) as fh:
            json.dump(payload, fh)
            path = fh.name
        proc = subprocess.run(
            [sys.executable, str(HERE / "lease_revoke.py"), path],
            capture_output=True, text=True,
        )
        return proc

    def test_cli_acceptance_scenario(self):
        proc = self.run_cli({
            "leases": acceptance_leases(),
            "operation": {"lease": "root", "quota": 3},
        })
        self.assertEqual(proc.returncode, 0, proc.stderr)
        result = json.loads(proc.stdout)
        self.assertEqual(result["revoked"], ["light"])
        self.assertEqual(result["loss"], 1)
        self.assertTrue(result["valid"])

    def test_cli_reports_invalid_input(self):
        proc = self.run_cli({
            "leases": [
                {"id": "root", "parent": None, "start": 0, "end": 10,
                 "quota": 1},
                {"id": "a", "parent": "root", "start": 0, "end": 10,
                 "quota": 2},
            ],
            "operation": {"lease": "root", "quota": 1},
        })
        self.assertEqual(proc.returncode, 2)
        self.assertIn("error:", proc.stderr)


if __name__ == "__main__":
    unittest.main()
