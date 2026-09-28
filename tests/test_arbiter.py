"""Unit tests for arbitration semantics (acceptance boundaries included)."""

from __future__ import annotations

import os
import sys
import unittest
from itertools import permutations

sys.path.insert(0, os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "src"))

from rename_arbiter.engine import (  # noqa: E402
    C_CAUSAL,
    C_CONCURRENT,
    R_CYCLE,
    R_DUP_DELETE,
    R_NO_OBJECT,
    R_STALE_REVIVE,
    SyncEngine,
)
from rename_arbiter.model import CONFLICTS_ID, EventParseError, Stamp, parse_events  # noqa: E402


def ev(
    event_id,
    etype,
    oid,
    value,
    node="n1",
    deps=(),
    is_dir=False,
    parent=None,
    name=None,
):
    return {
        "id": event_id,
        "type": etype,
        "object_id": oid,
        "stamp": {"value": value, "node": node},
        "deps": list(deps),
        **({"is_dir": is_dir} if etype == "create" else {}),
        **({"new_parent_id": parent} if parent is not None else {}),
        **({"new_name": name} if name is not None else {}),
    }


def resolve(raw):
    return SyncEngine().resolve(parse_events(raw))


def placement(report, oid):
    for p in report.to_dict()["placements"]:
        if p["object_id"] == oid:
            return p
    return None


def conflict_for(report, cname):
    for c in report.conflicts:
        if c.name == cname:
            return c
    return None


def rejection(report, event_id):
    for r in report.rejections:
        if r.event_id == event_id:
            return r
    return None


class ConcurrentRenameTests(unittest.TestCase):
    def base_batch(self):
        return [
            ev("c1", "create", "a", 1, parent="root", name="a.txt"),
            ev("c2", "create", "b", 2, parent="root", name="b.txt"),
            ev("ra", "rename", "a", 10, "n1", deps=["c1"], name="same.md"),
            ev("rb", "rename", "b", 11, "n2", deps=["c2"], name="same.md"),
        ]

    def test_high_stamp_wins(self):
        report = resolve(self.base_batch())
        winner = placement(report, "b")
        loser = placement(report, "a")
        self.assertEqual((winner["parent_id"], winner["name"]), ("root", "same.md"))
        self.assertEqual(loser["parent_id"], CONFLICTS_ID)
        self.assertTrue(loser["in_conflict"])
        c = conflict_for(report, "same.md")
        self.assertEqual(c.reason, C_CONCURRENT)
        self.assertEqual(c.winner_object_id, "b")
        self.assertEqual(c.loser_object_id, "a")
        self.assertEqual(c.winner_event_id, "rb")
        self.assertEqual(c.loser_event_id, "ra")
        self.assertGreater(c.winner_stamp, c.loser_stamp)
        self.assertTrue(c.conflict_path.startswith("/.conflicts/"))

    def test_convergence_all_permutations(self):
        batch = self.base_batch()
        reference = SyncEngine().resolve(parse_events(batch)).to_dict()
        count = 0
        for order in permutations(batch):
            count += 1
            out = SyncEngine().resolve(parse_events(list(order))).to_dict()
            self.assertEqual(out, reference, f"diverged for order {[e['id'] for e in order]}")
        self.assertEqual(count, 24)

    def test_reverse_delivery_order_still_same_winner(self):
        batch = list(reversed(self.base_batch()))
        report = resolve(batch)
        self.assertEqual(placement(report, "b")["name"], "same.md")
        self.assertTrue(placement(report, "a")["in_conflict"])

    def test_lower_stamp_wins_when_roles_reversed(self):
        batch = self.base_batch()
        batch[2]["stamp"] = {"value": 99, "node": "n1"}
        batch[3]["stamp"] = {"value": 50, "node": "n2"}
        report = resolve(batch)
        self.assertEqual(placement(report, "a")["name"], "same.md")
        self.assertTrue(placement(report, "b")["in_conflict"])
        self.assertEqual(conflict_for(report, "same.md").winner_object_id, "a")

    def test_equal_value_node_breaks_tie(self):
        batch = self.base_batch()
        batch[2]["stamp"] = {"value": 10, "node": "n9"}
        batch[3]["stamp"] = {"value": 10, "node": "n2"}
        report = resolve(batch)
        self.assertEqual(conflict_for(report, "same.md").winner_object_id, "a")
        self.assertTrue(placement(report, "b")["in_conflict"])


class CycleTests(unittest.TestCase):
    def setUp(self):
        self.batch = [
            ev("c1", "create", "dir-x", 1, is_dir=True, parent="root", name="x"),
            ev("c2", "create", "dir-y", 2, is_dir=True, parent="dir-x", name="y"),
            ev("bad", "move", "dir-x", 9, deps=["c2"], parent="dir-y"),
        ]

    def test_move_into_descendant_rejected(self):
        report = resolve(self.batch)
        r = rejection(report, "bad")
        self.assertIsNotNone(r)
        self.assertEqual(r.reason, R_CYCLE)
        px = placement(report, "dir-x")
        self.assertEqual((px["parent_id"], px["name"]), ("root", "x"))
        py = placement(report, "dir-y")
        self.assertEqual(py["parent_id"], "dir-x")

    def test_move_into_descendant_rejected_any_order(self):
        ref = resolve(self.batch).to_dict()
        for order in permutations(self.batch):
            self.assertEqual(resolve(list(order)).to_dict(), ref)

    def test_self_move_via_subdir_rejected_deep(self):
        batch = self.batch + [
            ev("c3", "create", "dir-z", 3, is_dir=True, parent="dir-y", name="z"),
            ev("bad2", "move", "dir-x", 10, deps=["c3"], parent="dir-z"),
        ]
        report = resolve(batch)
        self.assertEqual(rejection(report, "bad2").reason, R_CYCLE)


class TombstoneTests(unittest.TestCase):
    def setUp(self):
        self.batch = [
            ev("c", "create", "f", 1, parent="root", name="f.txt"),
            ev("d", "delete", "f", 2, deps=["c"]),
        ]

    def test_delete_then_stale_rename_rejected(self):
        batch = self.batch + [ev("old", "rename", "f", 3, deps=["c"], name="g.txt")]
        report = resolve(batch)
        self.assertEqual(rejection(report, "old").reason, R_DUP_DELETE)
        self.assertIsNone(placement(report, "f"))
        tombs = {t["object_id"]: t for t in report.to_dict()["tombstones"]}
        self.assertIn("f", tombs)
        self.assertEqual(tombs["f"]["deleted_by"], "d")

    def test_concurrent_recreate_rejected(self):
        # Recreate does not causally follow the delete: tombstone survives.
        batch = self.batch + [ev("r", "create", "f", 5, deps=[], parent="root", name="f2.txt")]
        report = resolve(batch)
        self.assertEqual(rejection(report, "r").reason, R_STALE_REVIVE)
        self.assertIsNone(placement(report, "f"))

    def test_causal_recreate_allowed(self):
        batch = self.batch + [ev("r", "create", "f", 5, deps=["d"], parent="root", name="f2.txt")]
        report = resolve(batch)
        self.assertIsNone(rejection(report, "r"))
        self.assertEqual(placement(report, "f")["name"], "f2.txt")

    def test_old_move_after_delete_rejected(self):
        batch = self.batch + [
            ev("d2", "create", "dir", 1, is_dir=True, parent="root", name="d"),
            ev("m", "move", "f", 6, deps=["c", "d2"], parent="dir"),
        ]
        report = resolve(batch)
        self.assertEqual(rejection(report, "m").reason, R_DUP_DELETE)

    def test_delete_of_never_created_rejected(self):
        report = resolve([ev("d", "delete", "ghost", 1)])
        self.assertEqual(rejection(report, "d").reason, R_NO_OBJECT)

    def test_delete_directory_removes_subtree(self):
        batch = [
            ev("p", "create", "parent", 1, is_dir=True, parent="root", name="p"),
            ev("k", "create", "kid", 2, is_dir=False, parent="parent", name="k"),
            ev("dp", "delete", "parent", 3, deps=["k"]),
        ]
        report = resolve(batch)
        self.assertIsNone(placement(report, "parent"))
        self.assertIsNone(placement(report, "kid"))
        self.assertEqual(len(report.to_dict()["tombstones"]), 2)


if __name__ == "__main__":
    unittest.main(verbosity=2)
