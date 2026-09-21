import os
import random
import tempfile
import unittest
from fractions import Fraction

from coverage_ledger import (
    AddMembership,
    AddReport,
    CoverageLedger,
    Membership,
    Report,
    RetractMembership,
    RetractReport,
    load_checkpoint,
    save_checkpoint,
)


class LedgerScenarioTest(unittest.TestCase):
    def build_initial_ledger(self):
        ledger = CoverageLedger()
        self.assertEqual(
            ledger.add_membership(Membership("A", 0, None, "v1", "mA")).impact.revised_scope,
            ((0, float("inf")),),
        )
        self.assertEqual(
            ledger.add_membership(Membership("B", 0, None, "v1", "mB")).impact.revised_scope,
            ((0, float("inf")),),
        )
        ledger.add_report(Report("A", 0, 6, "rA"))
        ledger.add_report(Report("B", 4, 8, "rB"))
        return ledger

    def test_union_and_at_least_two_do_not_sum_device_hours(self):
        ledger = self.build_initial_ledger()
        union = ledger.observe(
            kind="union", window_start=0, window_end=10
        )
        at_least_two = ledger.observe(
            kind="at_least_k", window_start=0, window_end=10, k=2
        )
        intersection = ledger.observe(
            kind="intersection", window_start=0, window_end=10
        )

        self.assertEqual(union.covered_length, 8)
        self.assertEqual(union.coverage_rate, Fraction(4, 5))
        self.assertEqual(at_least_two.covered_length, 2)
        self.assertEqual(at_least_two.coverage_rate, Fraction(1, 5))
        self.assertEqual(intersection.covered_length, 2)
        self.assertEqual(intersection.coverage_rate, Fraction(1, 5))
        self.assertEqual(
            [(fragment.start, fragment.end) for fragment in union.evidence],
            [(0, 4), (4, 6), (6, 8), (8, 10)],
        )

    def test_member_exit_old_retraction_and_correction_cut_by_time(self):
        ledger = self.build_initial_ledger()
        published = {
            "union": ledger.observe(kind="union", window_start=0, window_end=10),
            "at_least_two": ledger.observe(
                kind="at_least_k", window_start=0, window_end=10, k=2
            ),
        }

        exit_result = ledger.close_membership(
            "mB",
            5,
            reason="B exits",
            query_windows={"main": ("union", None, 0, 10)},
        )
        self.assertEqual(
            exit_result.impact.event_scope,
            ((0, float("inf")),),
        )
        self.assertEqual(
            exit_result.impact.revised_scope,
            ((5, float("inf")),),
        )
        self.assertEqual(
            exit_result.impact.windowed_revised_scope["main"],
            ((5, 10),),
        )

        replace_result = ledger.replace_report(
            "rB",
            Report("B", 3, 10, "rB-v2"),
            reason="correct B source interval",
        )
        self.assertEqual(
            replace_result.impact.event_scope,
            ((3, 10),),
        )
        self.assertEqual(
            replace_result.impact.revised_scope,
            ((3, 10),),
        )

        current_union = ledger.observe(kind="union", window_start=0, window_end=10)
        current_two = ledger.observe(
            kind="at_least_k", window_start=0, window_end=10, k=2
        )
        self.assertEqual(current_union.covered_length, 6)
        self.assertEqual(current_union.coverage_rate, Fraction(3, 5))
        self.assertEqual(current_two.covered_length, 2)
        self.assertEqual(current_two.coverage_rate, Fraction(1, 5))
        self.assertEqual(
            [
                (fragment.start, fragment.end, fragment.matched, fragment.eligible_devices)
                for fragment in current_two.evidence
            ],
            [
                (0, 3, False, ("A",)),
                (3, 5, True, ("A", "B")),
                (5, 6, False, ("A",)),
                (6, 10, False, ()),
            ],
        )

        old_union = ledger.observe_at_revision(
            4, kind="union", window_start=0, window_end=10
        )
        old_two = ledger.observe_at_revision(
            4, kind="at_least_k", window_start=0, window_end=10, k=2
        )
        self.assertEqual(old_union.covered_length, published["union"].covered_length)
        self.assertEqual(old_two.covered_length, published["at_least_two"].covered_length)
        self.assertEqual(old_union.rule_versions, ("v1",))
        self.assertEqual(old_two.rule_versions, ("v1",))

        union_delta = ledger.delta_for_observation(published["union"])
        two_delta = ledger.delta_for_observation(published["at_least_two"])
        self.assertEqual(union_delta.covered_delta, -2)
        self.assertEqual(two_delta.covered_delta, 0)
        self.assertTrue(two_delta.evidence_changed)

        rebuilt_union = union_delta.apply(published["union"])
        rebuilt_two = two_delta.apply(published["at_least_two"])
        self.assertEqual(rebuilt_union.covered_length, current_union.covered_length)
        self.assertEqual(rebuilt_two.covered_length, current_two.covered_length)
        self.assertEqual(
            [fragment.signature() for fragment in rebuilt_two.evidence],
            [fragment.signature() for fragment in current_two.evidence],
        )
        self.assertEqual(
            union_delta.inverse().apply(rebuilt_union).covered_length,
            published["union"].covered_length,
        )
        self.assertEqual(
            two_delta.inverse().apply(rebuilt_two).covered_length,
            published["at_least_two"].covered_length,
        )

    def test_duplicate_devices_do_not_double_count(self):
        ledger = CoverageLedger()
        ledger.add_membership(Membership("A", 0, None, "v1", "mA"))
        ledger.add_report(Report("A", 0, 8, "r1"))
        ledger.add_report(Report("A", 3, 6, "r2"))
        observation = ledger.observe(
            kind="at_least_k", window_start=0, window_end=10, k=2
        )
        self.assertEqual(observation.covered_length, 0)
        self.assertEqual(observation.evidence[1].active_count, 1)
        self.assertEqual(observation.evidence[1].online_devices, ("A",))
        self.assertEqual(observation.evidence[1].reports, (("A", "r1"), ("A", "r2")))


    def test_membership_rule_versions_are_revision_safe(self):
        ledger = CoverageLedger()
        ledger.add_membership(Membership("A", 0, None, "v1", "mA"))
        ledger.add_report(Report("A", 0, 10, "rA"))
        first = ledger.observe(kind="union", window_start=0, window_end=10)

        ledger.commit(
            [
                RetractMembership("mA"),
                AddMembership(
                    Membership("A", 0, 4, "v1", "mA")
                ),
                AddMembership(Membership("A", 4, None, "v2", "mA-v2")),
            ],
            reason="membership rule changes",
            query_windows={"main": ("union", None, 0, 10)},
        )
        current = ledger.observe(kind="union", window_start=0, window_end=10)
        old = ledger.observe_at_revision(
            first.revision, kind="union", window_start=0, window_end=10
        )
        self.assertEqual(old.rule_versions, ("v1",))
        self.assertEqual(current.rule_versions, ("v1", "v2"))
        self.assertEqual(
            [
                (fragment.start, fragment.end, fragment.memberships)
                for fragment in current.evidence
            ],
            [
                (
                    0,
                    4,
                    (("A", "mA", "v1"),),
                ),
                (
                    4,
                    10,
                    (("A", "mA-v2", "v2"),),
                ),
            ],
        )
        self.assertEqual(
            ledger.entries[-1].reason,
            "membership rule changes",
        )


    def test_batch_retraction_does_not_mismerge_new_endpoint(self):
        ledger = CoverageLedger()
        ledger.add_membership(Membership("A", 0, None, "v1", "mA"))
        ledger.add_report(Report("A", 0, 10, "old"))
        result = ledger.commit(
            [
                RetractReport("old"),
                AddReport(Report("A", 10, 20, "new")),
            ]
        )
        self.assertEqual(result.impact.revised_scope, ((0, 20),))
        observation = ledger.observe(kind="union", window_start=0, window_end=20)
        self.assertEqual(observation.covered_length, 10)
        self.assertEqual(
            [(fragment.start, fragment.end, fragment.matched) for fragment in observation.evidence],
            [(0, 10, False), (10, 20, True)],
        )


    def test_idempotent_duplicate_commit_and_overlapping_membership_rejection(self):
        ledger = CoverageLedger()
        membership = Membership("A", 0, None, "v1", "mA")
        ledger.add_membership(membership)
        self.assertIsNone(ledger.add_membership(membership))
        self.assertEqual(ledger.revision, 1)

        with self.assertRaises(ValueError):
            ledger.add_membership(
                Membership("A", 1, 2, "v2", "m-overlap")
            )

    def test_zero_length_or_invalid_window_is_parameter_error(self):
        ledger = CoverageLedger()
        with self.assertRaises(ValueError):
            ledger.observe(kind="union", window_start=5, window_end=5)
        with self.assertRaises(ValueError):
            ledger.observe(kind="union", window_start=8, window_end=4)
        with self.assertRaises(ValueError):
            ledger.observe(
                kind="at_least_k", window_start=0, window_end=10, k=0
            )

    def test_adjacent_endpoints_merge_after_retraction(self):
        ledger = CoverageLedger()
        ledger.add_membership(Membership("A", 0, None, "v1", "mA"))
        ledger.add_report(Report("A", 0, 2, "r1"))
        ledger.add_report(Report("A", 2, 4, "r2"))
        self.assertEqual(ledger.index.finite_endpoint_count(), 3)
        ledger.retract_report("r1")
        ledger.retract_report("r2")
        self.assertEqual(ledger.index.finite_endpoint_count(), 1)
        self.assertEqual(ledger.index.segment_count(), 2)

        ledger.add_report(Report("A", 0, 5, "r3"))
        ledger.add_report(Report("A", 5, 10, "r4"))
        observation = ledger.observe(kind="union", window_start=0, window_end=10)
        self.assertEqual(observation.covered_length, 10)
        self.assertEqual(
            [(fragment.start, fragment.end) for fragment in observation.evidence],
            [(0, 5), (5, 10)],
        )

    def test_checkpoint_restore_and_continue_commits(self):
        ledger = self.build_initial_ledger()
        ledger.close_membership("mB", 5)
        with tempfile.TemporaryDirectory() as directory:
            path = os.path.join(directory, "ledger.json")
            save_checkpoint(ledger, path)
            restored = load_checkpoint(path)

        before = restored.observe(
            kind="union", window_start=0, window_end=10
        )
        self.assertEqual(before.covered_length, 6)
        restored.replace_report("rB", Report("B", 3, 10, "rB-v2"))
        after = restored.observe(
            kind="at_least_k", window_start=0, window_end=10, k=2
        )
        self.assertEqual(after.covered_length, 2)
        self.assertEqual(restored.revision, 6)

    def test_delta_conservation_forward_and_inverse(self):
        ledger = self.build_initial_ledger()
        before = ledger.observe(kind="union", window_start=0, window_end=10)
        ledger.close_membership("mB", 5)
        ledger.replace_report("rB", Report("B", 3, 10, "rB-v2"))
        after = ledger.observe(kind="union", window_start=0, window_end=10)
        delta = ledger.delta_for_observation(before)

        added = sum(item.length for item in delta.added)
        removed = sum(item.length for item in delta.removed)
        self.assertEqual(
            before.covered_length + added - removed,
            after.covered_length,
        )
        reverse = delta.inverse()
        self.assertEqual(
            after.covered_length + sum(item.length for item in reverse.added)
            - sum(item.length for item in reverse.removed),
            before.covered_length,
        )


class BruteForceReferenceTest(unittest.TestCase):
    def reference_coverage(self, reports, memberships, kind, k, start, end):
        covered = 0
        for point in range(start, end):
            online = {
                report.device
                for report in reports
                if report.start <= point < report.end
            }
            members = {
                membership.device
                for membership in memberships
                if membership.start <= point < membership.end
            }
            active = online & members
            if kind == "union":
                matched = bool(active)
            elif kind == "intersection":
                matched = bool(members) and active == members
            else:
                matched = len(active) >= k
            covered += matched
        return covered

    def test_random_operations_against_full_reference(self):
        rng = random.Random(20260921)
        ledger = CoverageLedger()
        active_reports = []
        active_memberships = []
        devices = ["A", "B", "C"]
        report_seq = 0
        membership_seq = 0
        for device in devices:
            membership_seq += 1
            membership = Membership(
                device, 0, None, "v1", f"m{membership_seq}"
            )
            ledger.add_membership(membership)
            active_memberships.append(membership)

        for step in range(180):
            if step % 3 == 0 and active_reports:
                report = rng.choice(active_reports)
                ledger.retract_report(report.report_id)
                active_reports.remove(report)
            else:
                report_seq += 1
                device = rng.choice(devices)
                start = rng.randrange(0, 9)
                end = rng.randrange(start + 1, 13)
                report = Report(device, start, end, f"r{report_seq}")
                ledger.add_report(report)
                active_reports.append(report)

            if step % 17 == 16 and len(active_memberships) > 1:
                membership = rng.choice(active_memberships[1:])
                end = rng.randrange(membership.start + 1, 13)
                ledger.close_membership(membership.membership_id, end)
                memberships_index = ledger.index.memberships
                active_memberships = [
                    memberships_index[item.membership_id]
                    if item.membership_id in memberships_index
                    else item
                    for item in active_memberships
                ]
                active_memberships = list(memberships_index.values())

            for kind, k in (("union", None), ("intersection", None), ("at_least_k", 2)):
                observed = ledger.observe(
                    kind=kind, window_start=0, window_end=12, k=k
                )
                expected = self.reference_coverage(
                    active_reports,
                    active_memberships,
                    kind,
                    k if k is not None else 1,
                    0,
                    12,
                )
                self.assertEqual(observed.covered_length, expected)
                self.assertEqual(
                    observed.coverage_rate,
                    Fraction(expected, 12),
                )


if __name__ == "__main__":
    unittest.main()
