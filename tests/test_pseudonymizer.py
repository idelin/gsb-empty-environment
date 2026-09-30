"""Unittest suite for the pseudonymizer package (synthetic data only)."""

from __future__ import annotations

import hashlib
import hmac
import threading
import unittest

from pseudonymizer import (
    AuditLog,
    ConsistencyError,
    DuplicateKeyVersion,
    KeyStore,
    OperationCancelled,
    PseudonymStore,
    UnknownPseudonym,
    derive_pseudonym,
)
from pseudonymizer.crypto import INFO

KEY_V1 = b"synthetic-key-version-1-0123456789ab"
KEY_V2 = b"synthetic-key-version-2-0123456789ab"
KEY_V3 = b"synthetic-key-version-3-0123456789ab"


def make_store(**kwargs):
    ks = KeyStore()
    ks.add("v1", KEY_V1)
    audit = kwargs.pop("audit", AuditLog())
    return PseudonymStore(ks, audit=audit, **kwargs), audit


class DerivationTests(unittest.TestCase):
    """Pseudonyms come from standard HMAC-SHA256, not a lookup table."""

    def test_matches_independent_hmac_computation(self):
        subject, domain = "subject-001", "domain-a"
        message = (
            INFO + b"\x00" + domain.encode() + b"\x00"
            + subject.encode() + b"\x00" + (0).to_bytes(4, "big")
        )
        expected = hmac.new(KEY_V1, message, hashlib.sha256).digest()[:16].hex()
        self.assertEqual(
            derive_pseudonym(KEY_V1, domain, subject), expected
        )

    def test_stable_for_same_domain_and_subject(self):
        a = derive_pseudonym(KEY_V1, "domain-a", "subject-001")
        b = derive_pseudonym(KEY_V1, "domain-a", "subject-001")
        self.assertEqual(a, b)

    def test_domain_separation(self):
        a = derive_pseudonym(KEY_V1, "domain-a", "subject-001")
        b = derive_pseudonym(KEY_V1, "domain-b", "subject-001")
        self.assertNotEqual(a, b)

    def test_subject_separation(self):
        a = derive_pseudonym(KEY_V1, "domain-a", "subject-001")
        b = derive_pseudonym(KEY_V1, "domain-a", "subject-002")
        self.assertNotEqual(a, b)

    def test_key_separation(self):
        a = derive_pseudonym(KEY_V1, "domain-a", "subject-001")
        b = derive_pseudonym(KEY_V2, "domain-a", "subject-001")
        self.assertNotEqual(a, b)

    def test_counter_changes_output(self):
        a = derive_pseudonym(KEY_V1, "d", "s", counter=0)
        b = derive_pseudonym(KEY_V1, "d", "s", counter=1)
        self.assertNotEqual(a, b)

    def test_parameter_validation(self):
        with self.assertRaises(ValueError):
            derive_pseudonym(b"", "d", "s")
        with self.assertRaises(ValueError):
            derive_pseudonym(KEY_V1, "d", "s", length=4)
        with self.assertRaises(ValueError):
            derive_pseudonym(KEY_V1, "d", "s", counter=-1)


class StoreBasicsTests(unittest.TestCase):
    def test_get_or_create_is_stable_and_idempotent(self):
        store, _ = make_store()
        pid1 = store.get_or_create("domain-a", "subject-001")
        pid2 = store.get_or_create("domain-a", "subject-001")
        self.assertEqual(pid1, pid2)
        self.assertEqual(len(store.state.assignments), 1)

    def test_same_subject_differs_across_domains(self):
        store, _ = make_store()
        a = store.get_or_create("domain-a", "subject-001")
        b = store.get_or_create("domain-b", "subject-001")
        self.assertNotEqual(a, b)

    def test_reference_lifecycle(self):
        store, _ = make_store()
        pid = store.get_or_create("domain-a", "subject-001")
        store.add_reference("domain-a", pid, "record-1")
        store.add_reference("domain-a", pid, "record-2")
        self.assertEqual(
            store.state.refs[("domain-a", pid)], {"record-1", "record-2"}
        )
        store.remove_reference("domain-a", pid, "record-1")
        self.assertEqual(store.state.refs[("domain-a", pid)], {"record-2"})
        with self.assertRaises(UnknownPseudonym):
            store.remove_reference("domain-a", pid, "record-1")

    def test_reference_to_unknown_pseudonym_rejected(self):
        store, _ = make_store()
        with self.assertRaises(UnknownPseudonym):
            store.add_reference("domain-a", "f" * 32, "record-1")

    def test_consistency_check_passes_on_healthy_store(self):
        store, _ = make_store()
        pid = store.get_or_create("domain-a", "subject-001")
        store.add_reference("domain-a", pid, "record-1")
        self.assertTrue(store.check_consistency())

    def test_consistency_check_detects_tampering(self):
        store, _ = make_store()
        pid = store.get_or_create("domain-a", "subject-001")
        store.add_reference("domain-a", pid, "record-1")
        # Simulate corruption discovered during recovery.
        store.state.owners.pop(("domain-a", pid))
        with self.assertRaises(ConsistencyError):
            store.check_consistency()


class CollisionTests(unittest.TestCase):
    """Collisions are injected via derive_fn; resolution is deterministic."""

    @staticmethod
    def colliding_derive(key, domain, subject, counter):
        if counter == 0:
            return "c" * 32  # every first attempt collides
        return derive_pseudonym(key, domain, subject, counter=counter)

    def make_colliding_store(self):
        return make_store(derive_fn=self.colliding_derive)

    def test_collision_is_detected_and_resolved_deterministically(self):
        store, audit = self.make_colliding_store()
        pid_a = store.get_or_create("domain-a", "subject-001")
        pid_b = store.get_or_create("domain-a", "subject-002")
        self.assertEqual(pid_a, "c" * 32)
        self.assertNotEqual(pid_a, pid_b)
        # Resolution is reproducible: same counter recorded, same result.
        self.assertEqual(store.state.counters[("domain-a", "subject-002")], 1)
        self.assertEqual(store.get_or_create("domain-a", "subject-002"), pid_b)
        events = [r["event"] for r in audit.records]
        self.assertIn("collision_resolved", events)
        self.assertTrue(store.check_consistency())

    def test_no_dangling_references_after_resolution(self):
        store, _ = self.make_colliding_store()
        pid_a = store.get_or_create("domain-a", "subject-001")
        pid_b = store.get_or_create("domain-a", "subject-002")
        store.add_reference("domain-a", pid_a, "record-1")
        store.add_reference("domain-a", pid_b, "record-2")
        self.assertTrue(store.check_consistency())
        self.assertEqual(store.state.refs[("domain-a", pid_b)], {"record-2"})

    def test_rotation_under_collision_injection_stays_consistent(self):
        store, _ = self.make_colliding_store()
        store.get_or_create("domain-a", "subject-001")
        old_b = store.get_or_create("domain-a", "subject-002")
        store.add_reference("domain-a", old_b, "record-2")
        store.rotate("v2", KEY_V2)
        self.assertTrue(store.check_consistency())
        new_b = store.get_or_create("domain-a", "subject-002")
        self.assertNotEqual(old_b, new_b)
        self.assertEqual(store.resolve_link("domain-a", old_b), new_b)
        self.assertEqual(store.state.refs[("domain-a", new_b)], {"record-2"})


class RotationTests(unittest.TestCase):
    def test_rotation_repseudonymizes_and_links(self):
        store, audit = make_store()
        old_pid = store.get_or_create("domain-a", "subject-001")
        store.add_reference("domain-a", old_pid, "record-1")
        store.rotate("v2", KEY_V2)
        new_pid = store.get_or_create("domain-a", "subject-001")
        self.assertNotEqual(old_pid, new_pid)
        # Explicitly linkable, never mixed: lookups return the new pid.
        self.assertEqual(store.resolve_link("domain-a", old_pid), new_pid)
        self.assertEqual(
            store.state.assignments[("domain-a", "subject-001")], new_pid
        )
        # References migrated, nothing dangling.
        self.assertEqual(store.state.refs[("domain-a", new_pid)], {"record-1"})
        self.assertTrue(store.check_consistency())
        events = [r["event"] for r in audit.records]
        self.assertIn("rotate", events)

    def test_link_chain_survives_multiple_rotations(self):
        store, _ = make_store()
        pid_v1 = store.get_or_create("domain-a", "subject-001")
        store.rotate("v2", KEY_V2)
        pid_v2 = store.get_or_create("domain-a", "subject-001")
        store.rotate("v3", KEY_V3)
        pid_v3 = store.get_or_create("domain-a", "subject-001")
        self.assertEqual(len({pid_v1, pid_v2, pid_v3}), 3)
        self.assertEqual(store.resolve_link("domain-a", pid_v1), pid_v3)
        self.assertEqual(store.resolve_link("domain-a", pid_v2), pid_v3)
        self.assertTrue(store.check_consistency())

    def test_rotation_is_atomic_under_cancellation(self):
        store, _ = make_store()
        old_pid = store.get_or_create("domain-a", "subject-001")
        cancel = threading.Event()
        cancel.set()
        with self.assertRaises(OperationCancelled):
            store.rotate("v2", KEY_V2, cancel_event=cancel)
        # Neither the store nor the key store moved.
        self.assertEqual(
            store.state.assignments[("domain-a", "subject-001")], old_pid
        )
        self.assertEqual(store.state.version, "v1")
        self.assertTrue(store.check_consistency())

    def test_cancelled_get_or_create_leaves_no_trace(self):
        store, _ = make_store()
        cancel = threading.Event()
        cancel.set()
        with self.assertRaises(OperationCancelled):
            store.get_or_create("domain-a", "subject-001", cancel_event=cancel)
        self.assertEqual(len(store.state.assignments), 0)
        self.assertTrue(store.check_consistency())

    def test_duplicate_key_version_rejected(self):
        ks = KeyStore()
        ks.add("v1", KEY_V1)
        with self.assertRaises(DuplicateKeyVersion):
            ks.rotate("v1", KEY_V2)


class ConcurrencyTests(unittest.TestCase):
    def test_concurrent_same_subject_requests_agree(self):
        store, _ = make_store()
        results = []
        barrier = threading.Barrier(16)

        def worker():
            barrier.wait()
            for _ in range(25):
                results.append(store.get_or_create("domain-a", "subject-001"))

        threads = [threading.Thread(target=worker) for _ in range(16)]
        for t in threads:
            t.start()
        for t in threads:
            t.join()
        self.assertEqual(len(set(results)), 1)
        self.assertEqual(len(store.state.assignments), 1)
        self.assertTrue(store.check_consistency())

    def test_concurrent_mixed_subjects_stay_consistent(self):
        store, _ = make_store()
        subjects = [f"subject-{i:03d}" for i in range(8)]
        barrier = threading.Barrier(32)

        def worker(subject):
            barrier.wait()
            for _ in range(10):
                pid = store.get_or_create("domain-a", subject)
                store.add_reference("domain-a", pid, f"ref-{subject}")

        threads = [
            threading.Thread(target=worker, args=(s,)) for s in subjects * 4
        ]
        for t in threads:
            t.start()
        for t in threads:
            t.join()
        self.assertEqual(len(store.state.assignments), 8)
        self.assertTrue(store.check_consistency())

    def test_concurrent_rotation_and_reads_end_consistent(self):
        store, _ = make_store()
        for i in range(20):
            store.get_or_create("domain-a", f"subject-{i:03d}")
        stop = threading.Event()
        failures = []

        def reader():
            while not stop.is_set():
                try:
                    store.get_or_create("domain-a", "subject-000")
                    store.check_consistency()
                except ConsistencyError as exc:  # pragma: no cover
                    failures.append(exc)

        readers = [threading.Thread(target=reader) for _ in range(4)]
        for t in readers:
            t.start()
        store.rotate("v2", KEY_V2)
        store.rotate("v3", KEY_V3)
        stop.set()
        for t in readers:
            t.join()
        self.assertEqual(failures, [])
        self.assertTrue(store.check_consistency())


class AuditAndKeyHygieneTests(unittest.TestCase):
    def test_audit_log_never_contains_raw_subject_or_key(self):
        store, audit = make_store()
        subjects = [f"subject-{i:03d}" for i in range(5)]
        # Reference ids are caller-controlled external ids; callers must
        # not embed raw subjects in them (the store cannot inspect them).
        for i, s in enumerate(subjects):
            pid = store.get_or_create("domain-a", s)
            store.add_reference("domain-a", pid, f"record-{i:04d}")
        store.rotate("v2", KEY_V2)
        blob = repr(audit.records) + audit.dump()
        for s in subjects:
            self.assertNotIn(s, blob)
        for key in (KEY_V1, KEY_V2):
            self.assertNotIn(key.decode(), blob)
            self.assertNotIn(key.hex(), blob)

    def test_audit_log_rejects_forbidden_fields(self):
        audit = AuditLog()
        with self.assertRaises(ValueError):
            audit.record("assign", subject="subject-001")
        with self.assertRaises(ValueError):
            audit.record("rotate", key="deadbeef")

    def test_key_material_never_appears_in_reprs(self):
        ks = KeyStore()
        ks.add("v1", KEY_V1)
        self.assertNotIn(KEY_V1.decode(), repr(ks))
        self.assertNotIn(KEY_V1.decode(), repr(ks.active()))
        self.assertIn("<redacted>", repr(ks.active()))


if __name__ == "__main__":
    unittest.main()
