import os
import shutil
import tempfile
import unittest

from alerting.engine import Engine
from alerting.outbox import CrashAt, Pump, Transport


class OutboxRestartTest(unittest.TestCase):
    def setUp(self):
        self.dir = tempfile.mkdtemp()
        self.path = os.path.join(self.dir, "pump.db")

    def tearDown(self):
        shutil.rmtree(self.dir, ignore_errors=True)

    def _build(self):
        e = Engine(self.path)
        e.add_threshold("cpu", 0, "cpu", 10, 10, 5)
        e.ingest_sample(0, "cpu", 11)
        e.ingest_sample(6, "cpu", 12)
        return e

    def test_crash_after_send_is_redelivered_once(self):
        e = self._build()
        p = Pump(e)
        p.crash = CrashAt.AFTER_SEND
        with self.assertRaises(RuntimeError):
            p.run()
        self.assertEqual(e.pending_count(), 1)  # recorded, not confirmed

        # real restart: a new process opens the same file
        e2 = Engine(self.path)
        t2 = Transport()
        Pump(e2, t2).run()
        all_keys = [s[0] for s in p.transport.sent + t2.sent]
        self.assertEqual(len(all_keys), 2)       # one retry happened
        self.assertEqual(len(set(all_keys)), 1)  # same dedupe, never a dup id
        self.assertEqual(e2.pending_count(), 0)
        self.assertEqual([k for _, k in e2.delivered_snapshot()], ["OPEN"])

    def test_crash_before_send_is_simply_sent(self):
        e = self._build()
        p = Pump(e)
        p.crash = CrashAt.BEFORE_SEND
        with self.assertRaises(RuntimeError):
            p.run()
        self.assertEqual(p.transport.sent, [])
        e2 = Engine(self.path)
        t2 = Transport()
        Pump(e2, t2).run()
        self.assertEqual(len(t2.sent), 1)
        self.assertEqual(e2.pending_count(), 0)

    def test_correction_crash_between_send_and_confirm(self):
        e = self._build()
        Pump(e).run()  # OPEN confirmed
        e.ingest_sample(3, "cpu", 8)  # fault retracted
        self.assertEqual(e.pending_count(), 1)

        p = Pump(e)
        p.crash = CrashAt.AFTER_SEND
        with self.assertRaises(RuntimeError):
            p.run()

        e2 = Engine(self.path)
        t2 = Transport()
        Pump(e2, t2).run()
        kinds = e2.notification_kinds()
        self.assertEqual(kinds, ["OPEN", "RETRACT"])
        self.assertEqual(e2.pending_count(), 0)

    def test_multiple_restarts_never_duplicate(self):
        e = self._build()
        transports = []
        for _ in range(3):
            pump = Pump(e, Transport())
            transports.append(pump.transport)
            pump.run()
        confirmed = [k for _, k in e.delivered_snapshot()]
        self.assertEqual(confirmed, ["OPEN"])

    def test_unconfirmed_open_then_resolve_no_duplicate_open(self):
        # OPEN sits recorded-but-unconfirmed while the fault resolves; the
        # recovery path must not add a second OPEN and must still emit RESOLVE.
        e = self._build()
        self.assertEqual(e.pending_count(), 1)
        e.ingest_sample(10, "cpu", 0)  # resolved before anyone pumped
        kinds = e.notification_kinds()
        self.assertEqual(kinds, ["OPEN", "RESOLVE"])
        Pump(e).run()
        kinds = e.notification_kinds()
        self.assertEqual(kinds, ["OPEN", "RESOLVE"])
        self.assertEqual(e.pending_count(), 0)

    def test_unconfirmed_open_retracted_then_restart(self):
        # crash BEFORE the transport got OPEN; backfill turns the alert
        # into a never-happened fault; restart must deliver only RETRACT
        e = self._build()
        p = Pump(e)
        p.crash = CrashAt.BEFORE_SEND
        with self.assertRaises(RuntimeError):
            p.run()
        e.ingest_sample(3, "cpu", 8)
        e2 = Engine(self.path)
        Pump(e2, Transport()).run()
        self.assertEqual(e2.notification_kinds(), ["OPEN", "RETRACT"])
        # both still confirmable on restart; neither lost nor duplicated
        self.assertEqual(e2.pending_count(), 0)


if __name__ == "__main__":
    unittest.main()
