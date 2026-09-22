"""Targeted scenario tests for the cache dependency kernel.

Semantics under test: a change is applied to the pre-task filesystem state;
the cache is valid iff re-running the task in the changed state would
reproduce every recorded output.  The kernel decides from evidence + the
change's resulting-state facts alone; the oracle replays the raw trace.
"""

import unittest

from cachekernel import (VFS, build_evidence, evidence_to_json,
                         evidence_from_json, ReverseIndex, apply_change,
                         verdict_for, VALID, INVALID, INSUFFICIENT)
from cachekernel.oracle import replay
from cachekernel.vfs import content_hash


def W(path):
    """Deterministic task-write content (oracle derives the same)."""
    return ("data:" + path).encode()


def base_vfs():
    vfs = VFS()
    vfs.mkdir("setup", "/d")
    vfs.write("setup", "/d/a", b"A")
    vfs.write("setup", "/d/b", b"B")
    vfs.write("setup", "/f", b"F")
    vfs.symlink("setup", "/f", "/s")
    return vfs


def run(vfs, task, ops):
    start = len(vfs.log)
    for spec in ops:
        op, args = spec[0], spec[1:]
        getattr(vfs, op)(task, *args)
    return vfs.log[start:]


def judge(snap, evidence, change_script):
    """Verdict from evidence + change facts computed on the pre-task state."""
    change = apply_change(snap.snapshot(), change_script)
    verdict, expl = verdict_for(evidence, change)
    return verdict, expl, change


class ScenarioTests(unittest.TestCase):
    def test_failed_lookup_then_create(self):
        vfs = base_vfs()
        snap = vfs.snapshot()
        ev = run(vfs, "t", [("open", "/d/nope")])
        self.assertEqual(ev[0]["result"]["error"], "ENOENT")
        self.assertEqual(ev[0]["result"]["miss"]["name"], "nope")
        evd = build_evidence("t", ev)
        verdict, expl, change = judge(snap, evd, [("write", "/d/nope", b"N")])
        self.assertEqual(verdict, INVALID)
        self.assertIn("nope", expl[1])
        match, _ = replay(snap, change, ev)
        self.assertFalse(match)

    def test_open_then_path_replaced_by_other_object(self):
        vfs = base_vfs()
        snap = vfs.snapshot()
        ev = run(vfs, "t", [("open", "/f")])
        h = ev[0]["result"]["ok"]["handle"]
        ev += run(vfs, "t", [("read", h)])
        old_obj = ev[0]["result"]["ok"]["obj"]
        evd = build_evidence("t", ev)
        script = [("unlink", "/f"), ("write", "/f", b"F2")]
        verdict, expl, change = judge(snap, evd, script)
        self.assertEqual(verdict, INVALID)
        self.assertIn("/f", expl[1])
        # the old object itself is untouched: its content dep still holds
        self.assertNotIn(("obj", old_obj), change.affected)
        match, _ = replay(snap, change, ev)
        self.assertFalse(match)

    def test_symlink_retargeted_same_final_content(self):
        vfs = base_vfs()
        vfs.write("setup", "/g", b"F")  # same content as /f
        snap = vfs.snapshot()
        ev = run(vfs, "t", [("open", "/s")])
        h = ev[0]["result"]["ok"]["handle"]
        ev += run(vfs, "t", [("read", h)])
        evd = build_evidence("t", ev)
        script = [("symlink", "/g", "/s")]  # retarget; final content identical
        verdict, expl, change = judge(snap, evd, script)
        self.assertNotEqual(verdict, VALID)  # resolution itself is a dep
        match, _ = replay(snap, change, ev)
        self.assertFalse(match)

    def test_unlink_one_of_two_hardlinks(self):
        script = [("unlink", "/f")]
        verdicts = {}
        for task, path in (("t1", "/f"), ("t2", "/fl")):
            vfs = base_vfs()
            vfs.link("setup", "/f", "/fl")
            snap = vfs.snapshot()
            ev = run(vfs, task, [("open", path)])
            evd = build_evidence(task, ev)
            verdicts[task], _, _ = judge(snap, evd, script)
        self.assertEqual(verdicts["t1"], INVALID)  # opened via deleted path
        self.assertEqual(verdicts["t2"], VALID)    # opened via surviving link

    def test_prefix_enum_then_new_member(self):
        vfs = base_vfs()
        snap = vfs.snapshot()
        ev = run(vfs, "t", [("enumdir", "/d", 1)])
        self.assertEqual(ev[0]["result"]["ok"]["mode"], "prefix")
        evd = build_evidence("t", ev)
        script = [("write", "/d/zz", b"Z")]  # sorts after: prefix output same
        verdict, expl, change = judge(snap, evd, script)
        self.assertEqual(verdict, INSUFFICIENT)
        self.assertTrue(any("prefix" in line for line in expl))
        match, _ = replay(snap, change, ev)
        self.assertTrue(match)  # oracle: output reproducible; the kernel
        # stays conservative and says exactly why

    def test_eacces_distinct_from_enoent(self):
        vfs = base_vfs()
        vfs.write("setup", "/secret", b"S")
        vfs.set_readable("setup", "/secret", False)
        snap = vfs.snapshot()
        ev_denied = run(vfs, "t1", [("open", "/secret")])
        ev_missing = run(vfs, "t2", [("open", "/secret2")])
        self.assertEqual(ev_denied[0]["result"]["error"], "EACCES")
        self.assertEqual(ev_missing[0]["result"]["error"], "ENOENT")
        evd1 = build_evidence("t1", ev_denied)
        evd2 = build_evidence("t2", ev_missing)
        v1, _, _ = judge(snap, evd1, [("set_readable", "/secret", True)])
        v2, _, _ = judge(snap, evd2, [("set_readable", "/secret", True)])
        self.assertEqual(v1, INVALID)  # permission flip observed
        self.assertEqual(v2, VALID)    # missing-name dep untouched

    def test_non_atomic_snapshot_not_approved(self):
        vfs = base_vfs()
        snap = vfs.snapshot()
        ev1, h = vfs.open("t", "/f")
        vfs.read("t", h)
        vfs.write("other", "/f", b"F-changed")  # interleaved content change
        vfs.read("t", h)
        events = [e for e in vfs.log if e["task"] == "t"]
        evd = build_evidence("t", events)
        obj = ev1["result"]["ok"]["obj"]
        self.assertIn(obj, evd["non_atomic"])
        verdict, expl, _ = judge(snap, evd, [("write", "/f", b"F3")])
        self.assertNotEqual(verdict, VALID)

    def test_rename_after_open_handle_survives(self):
        vfs = base_vfs()
        ev, h = vfs.open("t", "/f")
        vfs.rename("t", "/f", "/f_moved")
        r = vfs.read("t", h)
        self.assertEqual(r["result"]["ok"]["hash"], content_hash(b"F"))

    def test_index_locality_unrelated_subtrees(self):
        vfs = VFS()
        for sub in ("a", "b", "c"):
            vfs.mkdir("setup", f"/{sub}")
            vfs.write("setup", f"/{sub}/f", b"x")
        snap = vfs.snapshot()
        evds = {}
        for sub in ("a", "b", "c"):
            ev = run(vfs, f"t_{sub}", [("open", f"/{sub}/f")])
            evds[f"t_{sub}"] = build_evidence(f"t_{sub}", ev)
        idx = ReverseIndex()
        for evd in evds.values():
            idx.add(evd)
        change = apply_change(snap.snapshot(), [("write", "/a/f", b"y")])
        idx.reset_access_log()
        cand = idx.candidates(change.affected)
        self.assertEqual(cand, {"t_a"})
        self.assertEqual(set(idx.accessed), change.affected)
        verdict, _ = verdict_for(evds["t_a"], change)
        self.assertEqual(verdict, INVALID)
        for sub in ("b", "c"):
            verdict, _ = verdict_for(evds[f"t_{sub}"], change)
            self.assertEqual(verdict, VALID)

    def test_index_removal_leaves_no_residue(self):
        vfs = base_vfs()
        snap = vfs.snapshot()
        idx = ReverseIndex()
        evds = []
        for i, path in enumerate(("/f", "/d/a", "/d/b")):
            ev = run(vfs, f"t{i}", [("open", path)])
            evd = build_evidence(f"t{i}", ev)
            evds.append(evd)
            idx.add(evd)
        for i in range(3):
            idx.remove(f"t{i}")
        self.assertTrue(idx.is_empty())
        # re-add after "recompute": only fresh deps present
        idx.add(evds[0])
        change = apply_change(snap.snapshot(), [("write", "/d/a", b"A2")])
        self.assertEqual(idx.candidates(change.affected), set())

    def test_evidence_json_roundtrip(self):
        vfs = base_vfs()
        snap = vfs.snapshot()
        ev = run(vfs, "t", [("open", "/s"), ("enumdir", "/d", 1),
                            ("open", "/d/nope")])
        evd = build_evidence("t", ev)
        restored = evidence_from_json(evidence_to_json(evd))
        self.assertEqual(restored, evd)
        for script in ([("write", "/d/zz", b"Z")],
                       [("write", "/d/nope", b"N")],
                       [("unlink", "/f")]):
            va = judge(snap, evd, script)
            vb = judge(snap, restored, script)
            self.assertEqual(va[0], vb[0])
            self.assertEqual(va[1], vb[1])


if __name__ == "__main__":
    unittest.main()
