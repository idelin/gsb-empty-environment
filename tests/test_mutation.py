"""Mutation semantics: evidence revocation on insert and delete/restore
round-trip consistency."""

from __future__ import annotations

from pwlin import CurveTable, Measurement, Node

from tests.util import PwlinTestCase

NODES = [
    Node(x=0.0, version=1, priority=0, values={"a": Measurement(0.0, 0.2)}),
    Node(x=1.0, version=1, priority=0, values={"a": Measurement(2.0, 2.4)}),
    Node(x=2.0, version=1, priority=0, values={"a": Measurement(1.0, 1.0)}),
    Node(x=3.0, version=1, priority=0, values={"a": Measurement(3.0, 3.6)}),
    Node(x=4.0, version=1, priority=0, values={"a": Measurement(0.5, 0.5)}),
]


def build():
    table = CurveTable(["a"])
    for n in NODES:
        table.insert(n)
    return table


def capture(table):
    vals = []
    for i in range(17):
        x = i * 0.25
        ev = table.evaluate("a", x)
        vals.append((ev.value, ev.derivative, ev.at_breakpoint,
                     tuple(n.x for n in ev.sources)))
    invs = []
    for y in (0.5, 1.0, 1.5, 2.5):
        ev = table.invert("a", y)
        invs.append(tuple((s.x_lo, s.x_hi) for s in ev.solutions))
    return vals, invs


class EvidenceRevocationTest(PwlinTestCase):
    def test_insert_revokes_old_evidence_and_issues_new(self):
        table = build()
        old_value = table.evaluate("a", 1.5)
        old_inverse = table.invert("a", 1.5)
        self.assertTrue(table.is_valid(old_value))
        self.assertTrue(table.is_valid(old_inverse))

        table.insert(Node(x=1.5, version=1, priority=0,
                          values={"a": Measurement(1.4, 1.6)}))
        self.assertFalse(table.is_valid(old_value))    # revoked
        self.assertFalse(table.is_valid(old_inverse))  # revoked

        new_value = table.evaluate("a", 1.5)           # fresh evidence
        new_inverse = table.invert("a", 1.5)
        self.assertTrue(table.is_valid(new_value))
        self.assertTrue(table.is_valid(new_inverse))
        self.assertGreater(new_value.epoch, old_value.epoch)
        self.assertEqual(new_value.sources[0].x, 1.5)  # now an exact breakpoint

    def test_delete_revokes_evidence(self):
        table = build()
        ev = table.evaluate("a", 2.5)
        table.delete(2.0)
        self.assertFalse(table.is_valid(ev))

    def test_invisible_insert_keeps_evidence(self):
        table = build()
        ev = table.evaluate("a", 2.5)
        # lower-priority version at an existing coordinate: no visible change
        changed = table.insert(Node(x=2.0, version=1, priority=-1,
                                    values={"a": Measurement(9.0, 9.0)}))
        self.assertFalse(changed)
        self.assertTrue(table.is_valid(ev))


class RoundTripTest(PwlinTestCase):
    def test_delete_then_restore_is_consistent(self):
        table = build()
        before = capture(table)
        epoch = table.epoch

        self.assertTrue(table.delete(2.0))
        mid = capture(table)
        self.assertNotEqual(before, mid)  # removal is visible

        table.insert(NODES[2])            # restore the identical node
        after = capture(table)
        self.assertEqual(before, after)   # values, derivatives, sources, inverses
        self.assertEqual(table.epoch, epoch + 2)

    def test_restore_matches_fresh_build(self):
        table = build()
        table.delete(2.0)
        table.insert(NODES[2])
        fresh = build()
        self.assertEqual(capture(table), capture(fresh))
