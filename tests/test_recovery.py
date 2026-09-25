import unittest

from evidence.receiver import Receiver
from evidence.sample import build_sample_manifest, feed_out_of_order
from evidence.signoff import (PublishCrash, confirm_delivery, pending_outbox,
                              recover, sign_off)
from evidence.store import Store


class RecoveryTest(unittest.TestCase):
    def setUp(self):
        self.store = Store(":memory:")
        self.receiver = Receiver(self.store)
        manifest, files = build_sample_manifest()
        self.receiver.receive_manifest(manifest)
        feed_out_of_order(self.receiver, manifest, files)
        self.manifest = manifest

    def test_crash_after_validation_before_publish_persists_nothing(self):
        def crash():
            raise PublishCrash("process killed after validation")

        with self.assertRaises(PublishCrash):
            sign_off(self.store, self.manifest.manifest_id,
                     before_commit=crash)

        # Nothing committed: no receipt, no notification.
        self.assertEqual(self.store.conn.execute(
            "SELECT COUNT(*) AS n FROM signoffs").fetchone()["n"], 0)
        self.assertEqual(self.store.conn.execute(
            "SELECT COUNT(*) AS n FROM outbox").fetchone()["n"], 0)

        # Recovery sees a clean state and the decision can be retried.
        report = recover(self.store)
        self.assertEqual(report.anomalies, [])
        self.assertEqual(report.pending, 0)

        retried = sign_off(self.store, self.manifest.manifest_id)
        self.assertTrue(retried.ok)
        self.assertEqual(recover(self.store).anomalies, [])

    def test_duplicate_notification_confirmation_is_idempotent(self):
        result = sign_off(self.store, self.manifest.manifest_id)
        self.assertTrue(result.ok)
        pending = pending_outbox(self.store)
        self.assertEqual(len(pending), 1)
        outbox_id = pending[0]["id"]

        self.assertTrue(confirm_delivery(self.store, outbox_id))
        self.assertFalse(confirm_delivery(self.store, outbox_id))
        self.assertFalse(confirm_delivery(self.store, outbox_id))

        delivered = self.store.conn.execute(
            "SELECT COUNT(*) AS n FROM outbox WHERE delivered=1"
        ).fetchone()["n"]
        self.assertEqual(delivered, 1)
        self.assertEqual(pending_outbox(self.store), [])

    def test_no_notification_without_valid_signoff_invariant(self):
        # Crash scenario repeated, plus a delivered notification for a sign-off
        # later superseded by a manifest correction.
        sign_off(self.store, self.manifest.manifest_id)
        outbox_id = pending_outbox(self.store)[0]["id"]
        confirm_delivery(self.store, outbox_id)

        v2, _ = build_sample_manifest(version=2)
        self.receiver.receive_manifest(v2)

        report = recover(self.store)
        # Delivered notifications reference real receipt rows even when the
        # receipt is now superseded; orphans (notice without receipt) never do.
        self.assertEqual(report.anomalies, [])

        # Defensive check: forge an orphan row as if storage had been corrupted,
        # recovery must surface it instead of silently treating it as healthy.
        self.store.conn.execute(
            "INSERT INTO outbox (signoff_id, kind, payload, delivered, created_at)"
            " VALUES (NULL, 'signoff_notification', '{}', 1, 0)")
        report = recover(self.store)
        self.assertEqual(len(report.anomalies), 1)


if __name__ == "__main__":
    unittest.main()
