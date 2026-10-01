"""Unittest suite for alert_machine. All data is synthetic."""

import json
import os
import tempfile
import unittest

from alert_machine import (
    AlertMachine, Config, SequencedAlertProcessor, StateStore,
    STATE_ALERT, STATE_NORMAL, EVENT_ALERT, EVENT_RESOLVE,
)

CONFIG = Config(high=10, low=5, m=2, n=2)


def make_processor(tmpdir, config=CONFIG):
    store = StateStore(os.path.join(tmpdir, "state.json"))
    return SequencedAlertProcessor.restore(config, store), store


class TestHysteresis(unittest.TestCase):
    def setUp(self):
        self.machine = AlertMachine(CONFIG)

    def feed(self, values):
        return [self.machine.process(v) for v in values]

    def test_acceptance_high_path(self):
        # 11, missing, 11 -> no alert; one more 11 -> alert
        self.assertEqual(self.feed([11, None, 11]), [None, None, None])
        self.assertEqual(self.machine.state, STATE_NORMAL)
        self.assertEqual(self.feed([11]), [EVENT_ALERT])
        self.assertEqual(self.machine.state, STATE_ALERT)

    def test_acceptance_low_path(self):
        self.feed([11, 11])  # enter ALERT
        # 4, 7 (middle), 4 -> no resolve; one more 4 -> resolve
        self.assertEqual(self.feed([4, 7, 4]), [None, None, None])
        self.assertEqual(self.machine.state, STATE_ALERT)
        self.assertEqual(self.feed([4]), [EVENT_RESOLVE])
        self.assertEqual(self.machine.state, STATE_NORMAL)

    def test_middle_value_resets_both_counters(self):
        self.feed([11])          # high_count = 1
        self.machine.process(8)  # middle: reset
        self.assertEqual(self.machine.high_count, 0)
        self.assertEqual(self.feed([11, 11]), [None, EVENT_ALERT])

    def test_missing_resets_counters(self):
        self.feed([11])
        self.machine.process(None)
        self.assertEqual((self.machine.high_count, self.machine.low_count), (0, 0))

    def test_boundary_values_inclusive(self):
        # value == H counts as high, value == L counts as low
        self.assertEqual(self.feed([10, 10]), [None, EVENT_ALERT])
        self.assertEqual(self.feed([5, 5]), [None, EVENT_RESOLVE])

    def test_counters_reset_after_transition(self):
        self.feed([11, 11])  # ALERT, counters cleared
        self.assertEqual((self.machine.high_count, self.machine.low_count), (0, 0))
        # single low then highs: no resolve, and alert re-entry needs m again
        self.assertEqual(self.feed([4, 11, 4, 4]), [None, None, None, EVENT_RESOLVE])

    def test_invalid_config_rejected(self):
        with self.assertRaises(ValueError):
            Config(high=5, low=5, m=2, n=2)
        with self.assertRaises(ValueError):
            Config(high=5, low=10, m=2, n=2)
        with self.assertRaises(ValueError):
            Config(high=10, low=5, m=0, n=2)


class TestSequencing(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.proc, _ = make_processor(self.tmp.name)

    def tearDown(self):
        self.tmp.cleanup()

    def test_duplicate_same_value_idempotent(self):
        r1 = self.proc.process(1, 11)
        r2 = self.proc.process(1, 11)
        self.assertEqual(r1["status"], "processed")
        self.assertEqual(r2["status"], "duplicate")
        self.assertEqual(self.proc.machine.high_count, 1)  # counted once

    def test_duplicate_different_value_rejected(self):
        self.proc.process(1, 11)
        r = self.proc.process(1, 12)
        self.assertEqual(r["status"], "rejected")
        self.assertEqual(self.proc.machine.high_count, 1)

    def test_out_of_order_rejected(self):
        self.proc.process(5, 11)
        r = self.proc.process(3, 11)
        self.assertEqual(r["status"], "rejected")

    def test_duplicate_missing_idempotent(self):
        self.proc.process(1, None)
        r = self.proc.process(1, None)
        self.assertEqual(r["status"], "duplicate")

    def test_invalid_value_rejected(self):
        r = self.proc.process(1, "abc")
        self.assertEqual(r["status"], "rejected")

    def test_missing_tokens(self):
        for i, tok in enumerate((None, "MISSING", "NaN", "null", "")):
            store = StateStore(os.path.join(self.tmp.name, f"state-{i}.json"))
            proc = SequencedAlertProcessor.restore(CONFIG, store)
            r = proc.process(1, tok)
            self.assertEqual(r["status"], "processed")
            self.assertEqual(proc.machine.high_count, 0)


class TestConfigSwitch(unittest.TestCase):
    def test_reconfigure_resets_counters_and_applies_forward(self):
        machine = AlertMachine(CONFIG)
        machine.process(11)  # high_count = 1
        machine.reconfigure(Config(high=20, low=5, m=2, n=2))
        self.assertEqual(machine.high_count, 0)
        # old threshold value 11 no longer counts as high
        self.assertIsNone(machine.process(11))
        self.assertEqual(machine.high_count, 0)
        self.assertEqual(machine.process(21), None)
        self.assertEqual(machine.process(21), EVENT_ALERT)


class TestPersistence(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.state_path = os.path.join(self.tmp.name, "state.json")

    def tearDown(self):
        self.tmp.cleanup()

    def restore(self, config=CONFIG):
        return SequencedAlertProcessor.restore(config, StateStore(self.state_path))

    def test_restart_does_not_reemit_state_change(self):
        proc = self.restore()
        self.assertEqual(proc.process(1, 11)["event"], None)
        self.assertEqual(proc.process(2, 11)["event"], EVENT_ALERT)
        # simulate restart
        proc2 = self.restore()
        self.assertEqual(proc2.machine.state, STATE_ALERT)
        # replaying the triggering record is a duplicate, not a new ALERT event
        r = proc2.process(2, 11)
        self.assertEqual((r["status"], r["event"]), ("duplicate", None))
        # continue stream: resolve emitted exactly once
        self.assertEqual(proc2.process(3, 4)["event"], None)
        self.assertEqual(proc2.process(4, 4)["event"], EVENT_RESOLVE)
        proc3 = self.restore()
        self.assertEqual(proc3.machine.state, STATE_NORMAL)
        self.assertEqual(proc3.process(4, 4)["event"], None)  # duplicate, no re-emit

    def test_counters_survive_restart(self):
        proc = self.restore()
        proc.process(1, 11)
        proc2 = self.restore()
        self.assertEqual(proc2.machine.high_count, 1)
        self.assertEqual(proc2.process(2, 11)["event"], EVENT_ALERT)

    def test_config_change_on_restart_resets_counters_keeps_state(self):
        proc = self.restore()
        proc.process(1, 11)
        proc.process(2, 11)  # ALERT
        new_config = Config(high=30, low=2, m=2, n=2)
        proc2 = self.restore(new_config)
        self.assertEqual(proc2.machine.state, STATE_ALERT)   # state kept
        self.assertEqual(proc2.machine.low_count, 0)          # counters reset
        # old low value 4 is now a middle value under new config
        self.assertIsNone(proc2.process(3, 4)["event"])
        self.assertEqual(proc2.process(4, 2)["event"], None)
        self.assertEqual(proc2.process(5, 2)["event"], EVENT_RESOLVE)

    def test_state_file_is_valid_json_after_each_write(self):
        proc = self.restore()
        for i, v in enumerate([11, None, 11, 11, 4, 7, 4, 4], start=1):
            proc.process(i, v)
            with open(self.state_path, encoding="utf-8") as fh:
                json.load(fh)  # must parse


class TestAcceptanceEndToEnd(unittest.TestCase):
    """Full acceptance boundary: H10 L5 m2 n2 via the persisted processor."""

    def test_boundary_sequence(self):
        with tempfile.TemporaryDirectory() as tmp:
            proc, _ = make_processor(tmp)
            stream = [11, None, 11, 11, 4, 7, 4, 4]
            events = [proc.process(i, v)["event"] for i, v in enumerate(stream, 1)]
            self.assertEqual(events, [None, None, None, EVENT_ALERT,
                                      None, None, None, EVENT_RESOLVE])


if __name__ == "__main__":
    unittest.main()
