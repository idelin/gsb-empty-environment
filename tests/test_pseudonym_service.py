"""Unit tests for the pseudonym service. Synthetic data only."""
from __future__ import annotations

import hashlib
import hmac
import json
import os
import tempfile
import threading
import unittest

from pseudonym_service import (
    AuditLog,
    CancelledError,
    KeyRing,
    PseudonymService,
    parse_pseudonym,
)

KEY_V1 = b"synthetic-key-material-v1-000000"
KEY_V2 = b"synthetic-key-material-v2-000000"
SUBJECTS = [f"subject-{i:03d}" for i in range(20)]
DOMAINS = ["billing", "analytics"]


def make_service(**kwargs) -> PseudonymService:
    return PseudonymService(KeyRing(KEY_V1), **kwargs)


class DerivationTests(unittest.TestCase):
    def test_matches_standard_hmac(self):
        """Pseudonyms are exactly HMAC-SHA256 over the documented encoding."""
        service = make_service()
        pseudonym = service.get_pseudonym("billing", "subject-000")
        version, digest = parse_pseudonym(pseudonym)
        self.assertEqual(version, 1)
        msg = b"pseudonym-service/v1"
        for part in ("billing", "subject-000", "0"):
            data = part.encode()
            msg += len(data).to_bytes(8, "big") + data
        expected = hmac.new(KEY_V1, msg, hashlib.sha256).hexdigest()
        self.assertEqual(digest, expected)

    def test_stable_within_domain(self):
        service = make_service()
        first = service.get_pseudonym("billing", "subject-000")
        for _ in range(5):
            self.assertEqual(first, service.get_pseudonym("billing", "subject-000"))

    def test_domain_isolation(self):
        service = make_service()
        per_domain = {d: service.get_pseudonym(d, "subject-000") for d in DOMAINS}
        self.assertNotEqual(per_domain["billing"], per_domain["analytics"])

    def test_no_hardcoded_mapping(self):
        """Two independent services with the same key derive identical results."""
        self.assertEqual(
            make_service().get_pseudonym("billing", "subject-000"),
            make_service().get_pseudonym("billing", "subject-000"),
        )


class CollisionTests(unittest.TestCase):
    def test_injected_collision_resolves_deterministically(self):
        hook = lambda domain, subject, epoch: subject == "subject-000" and epoch == 0
        first = make_service(collision_hook=hook).get_pseudonym("billing", "subject-000")
        second = make_service(collision_hook=hook).get_pseudonym("billing", "subject-000")
        self.assertEqual(first, second)  # deterministic resolution
        record = make_service(collision_hook=hook)
        pseudonym = record.get_pseudonym("billing", "subject-000")
        self.assertEqual(record.resolve(pseudonym).epoch, 1)

    def test_resolution_leaves_no_dangling_references(self):
        hook = lambda domain, subject, epoch: epoch == 0
        service = make_service(collision_hook=hook)
        pseudonyms = [service.get_pseudonym("billing", s) for s in SUBJECTS]
        service.rotate_key(KEY_V2)
        for pseudonym in pseudonyms:
            self.assertEqual(service.resolve(pseudonym).key_version, 1)
            self.assertEqual(service.resolve_current(pseudonym).key_version, 2)
        service.verify_consistency()

    def test_collision_events_are_audited_without_subjects(self):
        hook = lambda domain, subject, epoch: epoch == 0
        audit = AuditLog()
        service = PseudonymService(KeyRing(KEY_V1), audit=audit, collision_hook=hook)
        service.get_pseudonym("billing", "subject-000")
        events = [e.event for e in audit.entries]
        self.assertIn("collision_resolved", events)
        self.assertEqual(audit.scan_forbidden(SUBJECTS + [KEY_V1.decode()]), [])


class RotationTests(unittest.TestCase):
    def setUp(self):
        self.service = make_service()
        self.old = {
            s: {d: self.service.get_pseudonym(d, s) for d in DOMAINS}
            for s in SUBJECTS
        }

    def test_rotation_mapping_is_explicit_and_not_mixed(self):
        self.service.rotate_key(KEY_V2)
        mapping = self.service.rotation_map()
        self.assertEqual(len(mapping), len(SUBJECTS) * len(DOMAINS))
        for subject in SUBJECTS:
            for domain in DOMAINS:
                old = self.old[subject][domain]
                new = self.service.get_pseudonym(domain, subject)
                self.assertEqual(mapping[old], new)  # explicit old->new link
                self.assertNotEqual(old, new)
                self.assertEqual(parse_pseudonym(old)[0], 1)
                self.assertEqual(parse_pseudonym(new)[0], 2)

    def test_old_references_resolve_after_rotation(self):
        self.service.rotate_key(KEY_V2)
        for subject in SUBJECTS:
            for domain in DOMAINS:
                old = self.old[subject][domain]
                self.assertEqual(self.service.resolve(old).subject, subject)
                current = self.service.resolve_current(old)
                self.assertEqual(current.key_version, 2)
                self.assertEqual(current.subject, subject)

    def test_rotation_commit_is_atomic_on_cancellation(self):
        calls = {"n": 0}

        def cancel_check():
            calls["n"] += 1
            if calls["n"] > 3:
                raise CancelledError("simulated cancellation")

        before = self.service.get_pseudonym("billing", "subject-000")
        with self.assertRaises(CancelledError):
            self.service.rotate_key(KEY_V2, cancel_check=cancel_check)
        self.assertEqual(self.service.active_version, 1)
        self.assertEqual(self.service.rotation_map(), {})
        self.assertEqual(self.service.get_pseudonym("billing", "subject-000"), before)
        # No mapping was committed: the old pseudonym resolves to itself (v1).
        self.assertEqual(self.service.resolve_current(before).key_version, 1)
        self.service.verify_consistency()

    def test_rotation_then_recovery_keeps_mapping(self):
        self.service.rotate_key(KEY_V2)
        with tempfile.TemporaryDirectory() as tmp:
            path = os.path.join(tmp, "state.json")
            self.service.save(path)
            keyring = KeyRing(KEY_V1)
            keyring.add_version(KEY_V2, 2)
            keyring.activate(2)
            restored = PseudonymService.load(path, keyring)
        for subject in SUBJECTS:
            old = self.old[subject]["billing"]
            self.assertEqual(
                restored.resolve_current(old).pseudonym,
                self.service.resolve_current(old).pseudonym,
            )


class ConcurrencyTests(unittest.TestCase):
    def test_concurrent_same_subject_requests_are_consistent(self):
        service = make_service()
        results: list[list[str]] = [[] for _ in range(8)]

        def worker(slot):
            for _ in range(50):
                results[slot].append(service.get_pseudonym("billing", "subject-000"))

        threads = [threading.Thread(target=worker, args=(i,)) for i in range(8)]
        for t in threads:
            t.start()
        for t in threads:
            t.join()
        all_results = [p for slot in results for p in slot]
        self.assertEqual(len(set(all_results)), 1)
        service.verify_consistency()

    def test_concurrent_mixed_subjects_stay_unique(self):
        service = make_service()
        output: list[str] = []
        lock = threading.Lock()

        def worker(subject):
            pseudonym = service.get_pseudonym("billing", subject)
            with lock:
                output.append(pseudonym)

        threads = [
            threading.Thread(target=worker, args=(f"subject-{i:03d}",))
            for i in range(40)
            for _ in range(2)
        ]
        for t in threads:
            t.start()
        for t in threads:
            t.join()
        self.assertEqual(len(output), 80)
        self.assertEqual(len(set(output)), 40)


class HygieneTests(unittest.TestCase):
    def test_audit_log_has_no_subjects_or_keys(self):
        audit = AuditLog()
        service = PseudonymService(KeyRing(KEY_V1), audit=audit)
        for subject in SUBJECTS:
            for domain in DOMAINS:
                service.get_pseudonym(domain, subject)
        service.rotate_key(KEY_V2)
        forbidden = SUBJECTS + [KEY_V1.decode(), KEY_V2.decode()]
        self.assertEqual(audit.scan_forbidden(forbidden), [])

    def test_keyring_repr_redacts_keys(self):
        self.assertNotIn(KEY_V1.decode(), repr(KeyRing(KEY_V1)))

    def test_saved_state_contains_no_keys(self):
        service = make_service()
        service.get_pseudonym("billing", "subject-000")
        service.rotate_key(KEY_V2)
        with tempfile.TemporaryDirectory() as tmp:
            path = os.path.join(tmp, "state.json")
            service.save(path)
            with open(path, encoding="utf-8") as fh:
                blob = fh.read()
        for key in (KEY_V1, KEY_V2):
            self.assertNotIn(key.decode(), blob)
        json.loads(blob)  # well-formed


if __name__ == "__main__":
    unittest.main()
