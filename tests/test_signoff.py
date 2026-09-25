import unittest

from evidence.receiver import Receiver
from evidence.sample import build_sample_manifest, feed_out_of_order
from evidence.signoff import sign_off
from evidence.store import Store


class SignoffTest(unittest.TestCase):
    def setUp(self):
        self.store = Store(":memory:")
        self.receiver = Receiver(self.store)

    def _deliver_all_chunks(self, manifest, files):
        for status in feed_out_of_order(self.receiver, manifest, files, seed=5):
            self.assertIn(status, ("stored",))

    def test_all_files_present_but_reference_target_missing_blocks_signoff(self):
        manifest, files = build_sample_manifest(ghost_reference=True)
        self.receiver.receive_manifest(manifest)
        self._deliver_all_chunks(manifest, files)

        result = sign_off(self.store, manifest.manifest_id)
        self.assertFalse(result.ok)
        codes = {error.code for error in result.errors}
        self.assertIn("REFERENCE_TARGET_MISSING", codes)
        self.assertEqual(self.store.conn.execute(
            "SELECT COUNT(*) AS n FROM signoffs").fetchone()["n"], 0)
        self.assertEqual(self.store.conn.execute(
            "SELECT COUNT(*) AS n FROM outbox "
            "WHERE kind='signoff_notification'").fetchone()["n"], 0)

    def test_manifest_update_invalidates_old_signoff_pending_reverification(self):
        v1, files_v1 = build_sample_manifest(version=1)
        self.receiver.receive_manifest(v1)
        feed_out_of_order(self.receiver, v1, files_v1)
        first = sign_off(self.store, v1.manifest_id)
        self.assertTrue(first.ok)

        # Repeated sign-off for the same accepted version is idempotent.
        again = sign_off(self.store, v1.manifest_id)
        self.assertTrue(again.ok)
        self.assertTrue(again.idempotent_replay)
        self.assertEqual(again.signoff_id, first.signoff_id)

        # Corrected manifest arrives: old receipt invalidated, v2 pending.
        v2, _ = build_sample_manifest(version=2)
        self.assertEqual(self.receiver.receive_manifest(v2), "updated")
        old_status = self.store.conn.execute(
            "SELECT status FROM signoffs WHERE id=?",
            (first.signoff_id,)).fetchone()["status"]
        self.assertEqual(old_status, "superseded")
        new_status = self.store.conn.execute(
            "SELECT status FROM manifests WHERE version=2").fetchone()["status"]
        self.assertEqual(new_status, "pending_reverification")
        notice = self.store.conn.execute(
            "SELECT kind FROM outbox WHERE kind='reverification_required'"
        ).fetchone()
        self.assertIsNotNone(notice)

        # Signing the stale v1 receipt again is impossible: current is v2,
        # and v1 sign-off must not return the superseded row.
        from evidence.validator import validate_current
        report = validate_current(self.store, v1.manifest_id)
        self.assertFalse(report.ok)
        ids = self.store.conn.execute(
            "SELECT id FROM signoffs WHERE status='valid'").fetchall()
        self.assertEqual(ids, [])


if __name__ == "__main__":
    unittest.main()
