"""Additional semantic, convergence, parsing and CLI tests."""

from __future__ import annotations

import json
import os
import random
import subprocess
import sys
import unittest

sys.path.insert(0, os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "src"))

from rename_arbiter.engine import (  # noqa: E402
    C_CAUSAL,
    C_CONCURRENT,
    R_BAD_TARGET,
    R_STALE_CREATE,
    R_TARGET_MISSING,
    CausalCycleError,
    SyncEngine,
)
from rename_arbiter.model import EventParseError, parse_events  # noqa: E402

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
sys.path.insert(0, os.path.join(ROOT, "src"))


def ev(event_id, etype, oid, value, node="n1", deps=(), is_dir=False, parent=None, name=None):
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


class CausalSemanticsTests(unittest.TestCase):
    def test_causal_rename_supersedes_even_with_higher_resident_stamp(self):
        # Resident file got the name with a very high stamp, but the losing
        # rename causally follows both placements: causality beats stamps.
        batch = [
            ev("c1", "create", "a", 1, parent="root", name="a"),
            ev("c2", "create", "b", 2, parent="root", name="b"),
            ev("ra", "rename", "a", 100, deps=["c1"], name="shared"),
            ev("rb", "rename", "b", 5, deps=["c2", "ra"], name="shared"),
        ]
        report = resolve(batch)
        c = next(x for x in report.conflicts if x.name == "shared")
        self.assertEqual(c.reason, C_CAUSAL)
        self.assertEqual(c.winner_object_id, "b")
        self.assertTrue(placement(report, "a")["in_conflict"])

    def test_move_into_file_rejected(self):
        batch = [
            ev("cf", "create", "f", 1, parent="root", name="f"),
            ev("cg", "create", "g", 2, parent="root", name="g"),
            ev("m", "move", "g", 3, deps=["cf", "cg"], parent="f"),
        ]
        report = resolve(batch)
        r = next(x for x in report.rejections if x.event_id == "m")
        self.assertEqual(r.reason, R_BAD_TARGET)

    def test_move_into_missing_dir_rejected(self):
        batch = [
            ev("cg", "create", "g", 1, parent="root", name="g"),
            ev("m", "move", "g", 2, deps=["cg"], parent="void"),
        ]
        report = resolve(batch)
        self.assertEqual(
            next(x for x in report.rejections if x.event_id == "m").reason,
            R_TARGET_MISSING,
        )

    def test_duplicate_live_create_rejected(self):
        batch = [
            ev("c1", "create", "x", 1, parent="root", name="x"),
            ev("c2", "create", "x", 2, parent="root", name="x2"),
        ]
        report = resolve(batch)
        self.assertEqual(
            next(x for x in report.rejections if x.event_id == "c2").reason,
            R_STALE_CREATE,
        )

    def test_directory_subtree_moves_with_conflict_loser(self):
        batch = [
            ev("pd", "create", "pd", 1, is_dir=True, parent="root", name="pd"),
            ev("pe", "create", "pe", 2, is_dir=True, parent="root", name="pe"),
            ev("kd", "create", "kd", 3, parent="pd", name="child"),
            ev("mp", "move", "pd", 10, deps=["kd"], parent="pe"),
        ]
        report = resolve(batch)
        # pd moves into pe; no collision there. child must move along.
        self.assertEqual(placement(report, "pd")["parent_id"], "pe")
        self.assertEqual(placement(report, "kd")["parent_id"], "pd")

        # Now another directory concurrently claims pe/pd -> loser subtree
        # (pd + child) both end up under the conflict directory.
        batch += [
            ev("pf", "create", "pf", 4, is_dir=True, parent="pe", name="collision"),
            ev("rn", "rename", "pf", 11, deps=["pf"], name="pd"),
        ]
        report = resolve(batch)
        self.assertTrue(placement(report, "pd")["in_conflict"])
        self.assertEqual(placement(report, "kd")["parent_id"], "pd")

    def test_concurrent_create_same_name_arbitrated_by_stamp(self):
        batch = [
            ev("d", "create", "d", 1, is_dir=True, parent="root", name="d"),
            ev("a", "create", "obj-a", 10, deps=["d"], parent="d", name="x"),
            ev("b", "create", "obj-b", 20, deps=["d"], parent="d", name="x"),
        ]
        report = resolve(batch)
        self.assertEqual(placement(report, "obj-b")["name"], "x")
        self.assertTrue(placement(report, "obj-a")["in_conflict"])
        self.assertEqual(
            next(x for x in report.conflicts if x.name == "x").reason,
            C_CONCURRENT,
        )


class ConvergenceTests(unittest.TestCase):
    def test_random_shuffles_converge(self):
        batch = [
            ev("d", "create", "dir", 1, is_dir=True, parent="root", name="dir"),
            ev("a", "create", "a", 2, deps=["d"], parent="dir", name="a"),
            ev("b", "create", "b", 3, deps=["d"], parent="dir", name="b"),
            ev("s", "create", "sub", 4, is_dir=True, deps=["d"], parent="dir", name="sub"),
            ev("k", "create", "k", 5, deps=["s"], parent="sub", name="k"),
            ev("ra", "rename", "a", 10, "n1", deps=["a"], name="same"),
            ev("rb", "rename", "b", 11, "n2", deps=["b"], name="same"),
            ev("cy", "move", "dir", 12, deps=["k"], parent="sub"),
            ev("dk", "delete", "k", 13, deps=["k"]),
        ]
        reference = SyncEngine().resolve(parse_events(batch)).to_dict()
        rng = random.Random(20260928)
        for _ in range(200):
            shuffled = batch[:]
            rng.shuffle(shuffled)
            out = SyncEngine().resolve(parse_events(shuffled)).to_dict()
            self.assertEqual(out, reference)


class ParseAndCycleTests(unittest.TestCase):
    def test_bad_json_structure(self):
        with self.assertRaises(EventParseError):
            parse_events({"not": "a list"})

    def test_unknown_dependency(self):
        with self.assertRaises(EventParseError):
            parse_events([ev("a", "create", "x", 1, deps=["ghost"], parent="root", name="x")])

    def test_self_dependency(self):
        with self.assertRaises(EventParseError):
            parse_events([ev("a", "create", "x", 1, deps=["a"], parent="root", name="x")])

    def test_duplicate_event_id(self):
        with self.assertRaises(EventParseError):
            parse_events([
                ev("a", "create", "x", 1, parent="root", name="x"),
                ev("a", "create", "y", 2, parent="root", name="y"),
            ])

    def test_bad_name(self):
        with self.assertRaises(EventParseError):
            parse_events([ev("a", "create", "x", 1, parent="root", name="a/b")])

    def test_dependency_cycle_raises(self):
        batch = [
            ev("a", "create", "x", 1, deps=["b"], parent="root", name="x"),
            ev("b", "create", "y", 2, deps=["a"], parent="root", name="y"),
        ]
        with self.assertRaises(CausalCycleError):
            SyncEngine().resolve(parse_events(batch))


class CliTests(unittest.TestCase):
    def test_cli_on_example(self):
        example = os.path.join(ROOT, "examples", "events.json")
        proc = subprocess.run(
            [sys.executable, os.path.join(ROOT, "rename-arbiter"), example, "--compact"],
            capture_output=True,
            text=True,
            check=False,
        )
        self.assertEqual(proc.returncode, 0, proc.stderr)
        data = json.loads(proc.stdout)
        self.assertEqual(data["summary"]["events_total"], 16)
        reasons = {c["reason"] for c in data["conflicts"]}
        self.assertEqual(reasons, {C_CONCURRENT, C_CAUSAL})
        rejected = {r["reason"] for r in data["rejected"]}
        self.assertIn("CYCLE", rejected)
        self.assertIn("ALREADY_DELETED", rejected)

    def test_cli_stdin_and_error_code(self):
        proc = subprocess.run(
            [sys.executable, os.path.join(ROOT, "rename-arbiter")],
            input="not json",
            capture_output=True,
            text=True,
            check=False,
        )
        self.assertEqual(proc.returncode, 2)


if __name__ == "__main__":
    unittest.main(verbosity=2)
