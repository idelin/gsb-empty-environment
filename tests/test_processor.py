import unittest

from cdc_verify import MISSING, Event, Processor


def ev(txn_id, table, pk, seq, before=None, after=None):
    return Event(txn_id=txn_id, table=table, pk=pk,
                 commit_seq=seq, before=before, after=after)


def seed_proc(rows):
    return Processor(seed=[{"table": t, "pk": p, "row": r} for (t, p), r in rows.items()])


class PartialBeforeTest(unittest.TestCase):
    def test_only_provided_fields_are_validated(self):
        proc = seed_proc({("accounts", "A"): {"balance": 100, "tier": "gold"}})
        status = proc.process_transaction([
            ev("t1", "accounts", "A", 1, before={"balance": 100}, after={"balance": 120}),
        ])
        self.assertEqual(status, "applied")
        self.assertEqual(proc.row("accounts", "A"),
                         {"balance": 120, "tier": "gold"})

    def test_unprovided_fields_keep_current_values(self):
        proc = seed_proc({("accounts", "A"): {"balance": 100, "note": "vip"}})
        proc.process_transaction([
            ev("t1", "accounts", "A", 1, before={"balance": 100}, after={"balance": 1}),
        ])
        self.assertEqual(proc.row("accounts", "A")["note"], "vip")

    def test_unvalidated_field_may_drift_without_conflict(self):
        proc = seed_proc({("accounts", "A"): {"balance": 100, "tier": "silver"}})
        status = proc.process_transaction([
            ev("t1", "accounts", "A", 1, before={"balance": 100}, after={"balance": 5}),
        ])
        self.assertEqual(status, "applied")


class SameTxnSameKeyTest(unittest.TestCase):
    def test_validated_in_declared_order_not_against_old_snapshot(self):
        proc = seed_proc({("accounts", "A"): {"balance": 100}})
        status = proc.process_transaction([
            ev("t1", "accounts", "A", 1, before={"balance": 100}, after={"balance": 150}),
            ev("t1", "accounts", "A", 2, before={"balance": 150}, after={"balance": 175}),
        ])
        self.assertEqual(status, "applied")
        self.assertEqual(proc.row("accounts", "A"), {"balance": 175})

    def test_second_event_against_stale_snapshot_conflicts(self):
        proc = seed_proc({("accounts", "A"): {"balance": 100}})
        status = proc.process_transaction([
            ev("t1", "accounts", "A", 1, before={"balance": 100}, after={"balance": 150}),
            ev("t1", "accounts", "A", 2, before={"balance": 100}, after={"balance": 175}),
        ])
        self.assertEqual(status, "rejected")
        conflict = proc.report.conflicts[0]
        self.assertEqual(conflict.kind, "before_mismatch")
        self.assertEqual(conflict.fields[0].actual, 150)
        self.assertEqual(proc.row("accounts", "A"), {"balance": 100})


class AtomicityBoundaryTest(unittest.TestCase):
    def test_conflict_rejects_whole_transaction(self):
        proc = seed_proc({("t", "A"): {"v": 1}, ("t", "B"): {"v": 2}})
        status = proc.process_transaction([
            ev("t1", "t", "A", 1, before={"v": 9}, after={"v": 10}),
            ev("t1", "t", "B", 2, before={"v": 2}, after={"v": 20}),
        ])
        self.assertEqual(status, "rejected")
        self.assertEqual(proc.row("t", "A"), {"v": 1})
        self.assertEqual(proc.row("t", "B"), {"v": 2})

    def test_independent_transaction_continues_after_conflict(self):
        proc = seed_proc({("t", "A"): {"v": 1}, ("t", "C"): {"v": 3}})
        proc.process_transaction([
            ev("t1", "t", "A", 1, before={"v": 9}, after={"v": 10}),
        ])
        status = proc.process_transaction([
            ev("t2", "t", "C", 2, before={"v": 3}, after={"v": 30}),
        ])
        self.assertEqual(status, "applied")
        self.assertEqual(proc.row("t", "C"), {"v": 30})

    def test_same_key_later_events_wait_for_resolution(self):
        proc = seed_proc({("t", "A"): {"v": 1}})
        proc.process_transaction([
            ev("t1", "t", "A", 1, before={"v": 9}, after={"v": 10}),
        ])
        status = proc.process_transaction([
            ev("t2", "t", "A", 2, before={"v": 1}, after={"v": 11}),
        ])
        self.assertEqual(status, "blocked")
        self.assertEqual(proc.row("t", "A"), {"v": 1})
        self.assertEqual(proc.report.blocked[0]["txn_id"], "t2")

    def test_blocked_txn_is_queued_wholly_even_its_clean_keys(self):
        proc = seed_proc({("t", "A"): {"v": 1}, ("t", "B"): {"v": 2}})
        proc.process_transaction([
            ev("t1", "t", "A", 1, before={"v": 9}, after={"v": 10}),
        ])
        status = proc.process_transaction([
            ev("t2", "t", "A", 2, before={"v": 1}, after={"v": 11}),
            ev("t2", "t", "B", 3, before={"v": 2}, after={"v": 22}),
        ])
        self.assertEqual(status, "blocked")
        self.assertEqual(proc.row("t", "B"), {"v": 2})


class ResolutionTest(unittest.TestCase):
    def test_discard_drops_waiting_txns_and_releases_key(self):
        proc = seed_proc({("t", "A"): {"v": 1}})
        proc.process_transaction([ev("t1", "t", "A", 1, before={"v": 9}, after={"v": 10})])
        proc.process_transaction([ev("t2", "t", "A", 2, before={"v": 1}, after={"v": 11})])
        self.assertEqual(proc.resolve("t", "A", "discard"), "discarded")
        self.assertEqual(proc.row("t", "A"), {"v": 1})
        status = proc.process_transaction([
            ev("t3", "t", "A", 3, before={"v": 1}, after={"v": 11}),
        ])
        self.assertEqual(status, "applied")
        self.assertEqual(proc.row("t", "A"), {"v": 11})

    def test_accept_current_rebases_and_replays_in_order(self):
        proc = seed_proc({("t", "A"): {"v": 1}})
        proc.process_transaction([ev("t1", "t", "A", 1, before={"v": 1}, after={"v": 2})])
        proc.process_transaction([ev("t2", "t", "A", 2, before={"v": 1}, after={"v": 5})])
        proc.process_transaction([ev("t3", "t", "A", 3, before={"v": 5}, after={"v": 6})])
        self.assertEqual(proc.resolve("t", "A", "accept_current"), "replayed")
        self.assertEqual(proc.row("t", "A"), {"v": 6})
        self.assertEqual(proc.report.resolutions[0]["replayed_txns"], ["t3"])

    def test_reconflict_during_replay_requarantines_key(self):
        proc = seed_proc({("t", "A"): {"v": 1}})
        proc.process_transaction([ev("t1", "t", "A", 1, before={"v": 9}, after={"v": 10})])
        proc.process_transaction([ev("t2", "t", "A", 2, before=None, after={"v": 5})])
        self.assertEqual(proc.resolve("t", "A", "accept_current"), "reconflicted")
        self.assertIn(("t", "A"), proc.quarantine)
        self.assertEqual(proc.quarantine[("t", "A")].kind, "insert_exists")
        self.assertEqual(proc.row("t", "A"), {"v": 1})

    def test_resolve_without_quarantine_is_noop(self):
        proc = seed_proc({("t", "A"): {"v": 1}})
        self.assertEqual(proc.resolve("t", "A", "discard"), "not_quarantined")


class SwapKeysTest(unittest.TestCase):
    def test_swap_values_between_two_keys_in_one_txn(self):
        proc = seed_proc({("seats", "L"): {"holder": "alice"},
                          ("seats", "R"): {"holder": "bob"}})
        status = proc.process_transaction([
            ev("t1", "seats", "L", 1, before={"holder": "alice"}, after={"holder": "bob"}),
            ev("t1", "seats", "R", 2, before={"holder": "bob"}, after={"holder": "alice"}),
        ])
        self.assertEqual(status, "applied")
        self.assertEqual(proc.row("seats", "L"), {"holder": "bob"})
        self.assertEqual(proc.row("seats", "R"), {"holder": "alice"})


class DuplicateCommitTest(unittest.TestCase):
    def test_duplicate_commit_is_skipped_idempotently(self):
        proc = seed_proc({("c", "A"): {"n": 1}})
        first = proc.process_transaction([
            ev("t1", "c", "A", 1, before={"n": 1}, after={"n": 2}),
        ])
        second = proc.process_transaction([
            ev("t1", "c", "A", 1, before={"n": 1}, after={"n": 2}),
        ])
        self.assertEqual((first, second), ("applied", "duplicate"))
        self.assertEqual(proc.row("c", "A"), {"n": 2})
        self.assertEqual(proc.report.duplicates, [("t1", "c", "A", 1)])

    def test_partial_duplicate_inside_txn_is_filtered(self):
        proc = seed_proc({("c", "A"): {"n": 1}, ("c", "B"): {"n": 1}})
        proc.process_transaction([ev("t1", "c", "A", 1, before={"n": 1}, after={"n": 2})])
        status = proc.process_transaction([
            ev("t1", "c", "A", 1, before={"n": 1}, after={"n": 2}),
            ev("t2", "c", "B", 2, before={"n": 1}, after={"n": 5}),
        ])
        self.assertEqual(status, "applied")
        self.assertEqual(proc.row("c", "A"), {"n": 2})
        self.assertEqual(proc.row("c", "B"), {"n": 5})


class LateVersionTest(unittest.TestCase):
    def test_late_old_version_rejected_not_overwritten(self):
        proc = seed_proc({("d", "A"): {"v": 1}})
        proc.process_transaction([ev("t1", "d", "A", 5, before={"v": 1}, after={"v": 2})])
        status = proc.process_transaction([
            ev("t2", "d", "A", 3, before={"v": 1}, after={"v": 9}),
        ])
        self.assertEqual(status, "rejected")
        conflict = proc.report.conflicts[0]
        self.assertEqual(conflict.kind, "late_version")
        self.assertIn("3 < last applied 5", conflict.detail)
        self.assertEqual(proc.row("d", "A"), {"v": 2})

    def test_seq_reuse_by_different_event_conflicts(self):
        proc = seed_proc({("d", "A"): {"v": 1}})
        proc.process_transaction([ev("t1", "d", "A", 5, before={"v": 1}, after={"v": 2})])
        status = proc.process_transaction([
            ev("t2", "d", "A", 5, before={"v": 2}, after={"v": 3}),
        ])
        self.assertEqual(status, "rejected")
        self.assertEqual(proc.report.conflicts[0].kind, "seq_reuse")


class MinimalEvidenceTest(unittest.TestCase):
    def test_only_mismatched_fields_are_reported(self):
        proc = seed_proc({("t", "A"): {"a": 1, "b": 2, "c": 3}})
        proc.process_transaction([
            ev("t1", "t", "A", 1,
               before={"a": 1, "b": 20, "c": 30}, after={"a": 7}),
        ])
        conflict = proc.report.conflicts[0]
        self.assertEqual(conflict.kind, "before_mismatch")
        evidence = {f.field: (f.expected, f.actual) for f in conflict.fields}
        self.assertEqual(evidence, {"b": (20, 2), "c": (30, 3)})


class MissingVsNullTest(unittest.TestCase):
    def test_missing_field_is_not_equal_to_null(self):
        proc = seed_proc({("u", "A"): {"name": "n1"}})
        status = proc.process_transaction([
            ev("t1", "u", "A", 1, before={"email": None}, after={"email": "a@x"}),
        ])
        self.assertEqual(status, "rejected")
        field = proc.report.conflicts[0].fields[0]
        self.assertEqual(field.field, "email")
        self.assertIsNone(field.expected)
        self.assertIs(field.actual, MISSING)

    def test_null_field_matches_null_before(self):
        proc = seed_proc({("u", "B"): {"name": "n2", "email": None}})
        status = proc.process_transaction([
            ev("t1", "u", "B", 1, before={"email": None}, after={"email": "b@x"}),
        ])
        self.assertEqual(status, "applied")
        self.assertEqual(proc.row("u", "B")["email"], "b@x")

    def test_missing_row_with_before_reports_missing_event(self):
        proc = Processor()
        status = proc.process_transaction([
            ev("t1", "u", "ghost", 1, before={"v": 1}, after={"v": 2}),
        ])
        self.assertEqual(status, "rejected")
        conflict = proc.report.conflicts[0]
        self.assertEqual(conflict.kind, "missing_row")
        self.assertIs(conflict.fields[0].actual, MISSING)


class InsertDeleteTest(unittest.TestCase):
    def test_insert_on_absent_row_applies(self):
        proc = Processor()
        status = proc.process_transaction([
            ev("t1", "u", "A", 1, before=None, after={"v": 1}),
        ])
        self.assertEqual(status, "applied")
        self.assertEqual(proc.row("u", "A"), {"v": 1})

    def test_insert_on_existing_row_conflicts(self):
        proc = seed_proc({("u", "A"): {"v": 1}})
        status = proc.process_transaction([
            ev("t1", "u", "A", 1, before=None, after={"v": 2}),
        ])
        self.assertEqual(status, "rejected")
        self.assertEqual(proc.report.conflicts[0].kind, "insert_exists")
        self.assertEqual(proc.row("u", "A"), {"v": 1})

    def test_delete_validates_before_then_removes_row(self):
        proc = seed_proc({("u", "A"): {"v": 1, "w": 2}})
        status = proc.process_transaction([
            ev("t1", "u", "A", 1, before={"v": 1}, after=None),
        ])
        self.assertEqual(status, "applied")
        self.assertIsNone(proc.row("u", "A"))

    def test_delete_of_missing_row_conflicts(self):
        proc = Processor()
        status = proc.process_transaction([
            ev("t1", "u", "A", 1, before=None, after=None),
        ])
        self.assertEqual(status, "rejected")
        self.assertEqual(proc.report.conflicts[0].kind, "delete_missing")


if __name__ == "__main__":
    unittest.main()
