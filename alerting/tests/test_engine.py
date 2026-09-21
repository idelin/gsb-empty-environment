import os
import shutil
import tempfile
import unittest

from alerting.engine import Engine
from alerting.outbox import CrashAt, Pump, Transport


class EngineTest(unittest.TestCase):
    def setUp(self):
        self.dir = tempfile.mkdtemp()
        self.path = os.path.join(self.dir, "test.db")
        self.e = Engine(self.path)

    def tearDown(self):
        shutil.rmtree(self.dir, ignore_errors=True)

    def drain(self, engine=None):
        engine = engine or self.e
        pump = Pump(engine)
        return pump.run(), pump

    # --- the headline scenario --------------------------------------
    def test_enter10_duration5_backfill3_retracts(self):
        self.e.add_threshold("cpu", 0, "cpu", 10, 10, 5)
        self.e.ingest_sample(0, "cpu", 11)
        self.e.ingest_sample(6, "cpu", 12)

        alert = self.e.alert_snapshot()[0]
        self.assertEqual(alert[7], "firing")
        self.assertEqual(self.e.notification_kinds(), ["OPEN"])
        self.drain()

        # backfill t=3 with value 8: the fault never matured -> correction
        self.e.ingest_sample(3, "cpu", 8)
        self.assertEqual(self.e.notification_kinds(),
                         ["OPEN", "RETRACT"])
        # audit trail still contains the original OPEN revision
        revs = self.e.revision_snapshot()
        self.assertIn("created", [r[2] for r in revs])

    def test_recovery_during_suppression_not_replayed_after_lift(self):
        self.e.add_threshold("cpu", 0, "cpu", 10, 10, 5)
        for t, v in [(0, 11), (6, 12)]:
            self.e.ingest_sample(t, "cpu", v)
        self.drain()  # OPEN confirmed
        self.e.add_suppression("s", 4, 10, metric="cpu", valid_from=4)
        self.assertEqual(self.e.alert_snapshot()[0][7], "suppressed")
        # fault recovers inside the suppressed window
        self.e.ingest_sample(8, "cpu", 0)
        kinds = self.e.notification_kinds()
        self.assertEqual(kinds[-1], "RESOLVE")
        self.e.ingest_sample(12, "cpu", 0)
        self.assertEqual(self.e.notification_kinds().count("OPEN"), 1)

    def test_three_overlapping_suppression_rules(self):
        # [2,8), [5,12), [10,20) union -> [2,20)
        self.e.add_threshold("cpu", 0, "cpu", 10, 10, 3)
        self.e.add_suppression("a", 2, 8, "cpu", valid_from=2)
        self.e.add_suppression("b", 5, 12, "cpu", valid_from=5)
        self.e.add_suppression("c", 10, 20, "cpu", valid_from=10)
        for t in range(0, 20):
            self.e.ingest_sample(t, "cpu", 12)
        self.assertEqual(self.e.alert_snapshot()[0][7], "suppressed")
        # nothing was ever OPEN: matured under cover, so no notification yet
        self.assertEqual(self.e.notification_kinds(), [])
        # fault recovers at 18, still suppressed; lifting c (open-ended via
        # rollback at t=22) must not resurface the dead incident
        self.e.ingest_sample(18, "cpu", 0)
        self.e.ingest_sample(22, "cpu", 0)
        self.assertEqual(self.e.alert_snapshot()[0][7], "resolved")
        self.assertEqual(self.e.notification_kinds(), [])

    def test_threshold_rollback_restores_old_truth(self):
        self.e.add_threshold("cpu", 0, "cpu", 10, 10, 5)
        for t in range(0, 16, 2):
            self.e.ingest_sample(t, "cpu", 12)
        self.drain()
        self.e.add_threshold("cpu", 10, "cpu", 100, 100, 5)  # tight v2
        self.assertEqual(self.e.alert_snapshot()[0][7], "resolved")
        self.assertIn("RESOLVE", self.e.notification_kinds())
        # rollback appends v3 == v1 params from t=14
        self.e.rollback_threshold("cpu", to_version=1, valid_from=14)
        self.assertEqual(self.e.alert_snapshot()[-1][7], "pending")
        # no spurious notification for the re-opened pending incident yet
        self.assertEqual(self.e.notification_kinds().count("OPEN"), 1)

    def test_same_time_sample_correction(self):
        self.e.add_threshold("cpu", 0, "cpu", 10, 10, 3)
        self.e.ingest_sample(0, "cpu", 12)
        self.e.ingest_sample(3, "cpu", 12)
        self.drain()
        # overwrite t=0 with a low value: incident start moves, old retracted
        self.e.ingest_sample(0, "cpu", 1)
        states = [(a[3], a[7], a[9]) for a in self.e.alert_snapshot()]
        # original alert is resolved-never-mature => RETRACT owed
        self.assertTrue(any(s[2] == 0 for s in states))
        self.assertIn("RETRACT", self.e.notification_kinds())
        # correcting the same time again to a high value revives consistently
        self.e.ingest_sample(0, "cpu", 12)
        self.assertIn("OPEN", self.e.notification_kinds())


if __name__ == "__main__":
    unittest.main()
