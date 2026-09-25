"""Snapshots are immutable: calibration updates never leak into them."""

from __future__ import annotations

from pwlin import CurveTable, Measurement, Node

from tests.util import PwlinTestCase


def build():
    table = CurveTable(["a"])
    table.insert(Node(x=0.0, version=1, priority=0,
                      values={"a": Measurement(0.0, 0.2)}))
    table.insert(Node(x=1.0, version=1, priority=0,
                      values={"a": Measurement(1.0, 1.4)}))
    table.insert(Node(x=2.0, version=1, priority=0,
                      values={"a": Measurement(0.5, 0.5)}))
    return table


class SnapshotTest(PwlinTestCase):
    def test_snapshot_immune_to_calibration(self):
        table = build()
        snap = table.snapshot()
        table.calibrate(1.0, "a", 1.1, 1.3, version=2)

        new_ev = table.evaluate("a", 1.0)
        old_ev = snap.evaluate("a", 1.0)
        self.assertIntervalAlmostEqual(new_ev.value, 1.1, 1.3)
        self.assertIntervalAlmostEqual(old_ev.value, 1.0, 1.4)
        self.assertEqual(old_ev.sources[0].version, 1)
        self.assertEqual(new_ev.sources[0].version, 2)

    def test_snapshot_inverse_unchanged(self):
        table = build()
        snap = table.snapshot()
        before = [(s.x_lo, s.x_hi) for s in snap.invert("a", 1.0).solutions]
        table.calibrate(1.0, "a", 1.1, 1.3, version=2)
        table.delete(2.0)
        after = [(s.x_lo, s.x_hi) for s in snap.invert("a", 1.0).solutions]
        self.assertEqual(before, after)

    def test_snapshot_evidence_stays_valid(self):
        table = build()
        snap = table.snapshot()
        snap_ev = snap.evaluate("a", 0.5)
        table_ev = table.evaluate("a", 0.5)
        table.calibrate(1.0, "a", 1.1, 1.3, version=2)
        self.assertTrue(snap.is_valid(snap_ev))     # snapshot epoch frozen
        self.assertFalse(table.is_valid(table_ev))  # table evidence revoked

    def test_snapshot_epoch_frozen(self):
        table = build()
        snap = table.snapshot()
        epoch = snap.epoch
        table.insert(Node(x=3.0, version=1, priority=0,
                          values={"a": Measurement.exact(9.0)}))
        self.assertEqual(snap.epoch, epoch)
        self.assertGreater(table.epoch, epoch)
