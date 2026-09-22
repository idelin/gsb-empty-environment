"""Exhaustive small-object interleavings, cross-checked against the oracle.

For every legal task trace up to length 3 over a small op set, and for a
fixed battery of follow-up filesystem changes, we check:

  * kernel VALID  => oracle reproduces every recorded output (soundness)
  * kernel INVALID / INSUFFICIENT always carry a concrete explanation
  * the kernel is not vacuously conservative: all three verdicts occur
"""

import itertools
import unittest
from collections import Counter

from cachekernel import (VFS, build_evidence, ReverseIndex, apply_change,
                         verdict_for, VALID, INVALID, INSUFFICIENT)
from cachekernel.oracle import replay


def W(path):
    return ("data:" + path).encode()


def setup_vfs():
    vfs = VFS()
    vfs.mkdir("setup", "/d")
    vfs.write("setup", "/d/a", b"A")
    vfs.write("setup", "/d/b", b"B")
    vfs.write("setup", "/f", b"F")
    vfs.symlink("setup", "/f", "/s")
    return vfs


# --- task op templates: each returns the list of events it appended --------
def _open_read(vfs, path):
    ev, h = vfs.open("task", path)
    out = [ev]
    if h is not None:
        out.append(vfs.read("task", h))
    return out


TEMPLATES = [
    lambda v: _open_read(v, "/f"),
    lambda v: _open_read(v, "/d/a"),
    lambda v: _open_read(v, "/s"),          # symlink resolution is a dep
    lambda v: [v.open("task", "/d/missing")[0]],   # failed lookup
    lambda v: [v.open("task", "/missing")[0]],
    lambda v: [v.enumdir("task", "/d")],           # full enumeration
    lambda v: [v.enumdir("task", "/d", 1)],        # prefix consumption
    lambda v: [v.enumdir("task", "/")],
    lambda v: [v.write("task", "/f", W("/f"))],
    lambda v: [v.write("task", "/d/new", W("/d/new"))],
    lambda v: [v.rename("task", "/f", "/f2")],
    lambda v: [v.link("task", "/f", "/fl")],
    lambda v: [v.symlink("task", "/f", "/s2")],
    lambda v: [v.unlink("task", "/d/a")],
    lambda v: _open_read(v, "/d"),
]

CHANGES = [
    [("write", "/f", b"c2")],                          # content change
    [("write", "/newfile", b"c")],                     # create at root
    [("write", "/d/zz", b"c")],                        # add member to /d
    [("write", "/d/missing", b"c")],                   # create after miss
    [("rename", "/f", "/f_renamed")],                  # rename
    [("unlink", "/f"), ("write", "/f", b"c3")],        # replace object at path
    [("symlink", "/d", "/s")],                         # retarget symlink
    [("set_readable", "/f", False)],                   # permission change
    [("unlink", "/d/a")],                              # delete hardlinked name
]


class ExhaustiveTests(unittest.TestCase):
    def test_all_interleavings(self):
        stats = Counter()
        checked = 0
        for length in (1, 2, 3):
            for combo in itertools.product(range(len(TEMPLATES)), repeat=length):
                vfs = setup_vfs()
                snap = vfs.snapshot()
                events = []
                for ti in combo:
                    events.extend(TEMPLATES[ti](vfs))
                evidence = build_evidence("task", events)
                for script in CHANGES:
                    change = apply_change(snap.snapshot(), script)
                    verdict, expl = verdict_for(evidence, change)
                    stats[verdict] += 1
                    checked += 1
                    self.assertTrue(expl, "verdict must carry an explanation")
                    if verdict == INSUFFICIENT:
                        text = " ".join(expl)
                        self.assertTrue(
                            "prefix" in text or "non-atomic" in text,
                            f"conservative verdict needs a concrete reason: {text}")
                    if verdict == VALID:
                        match, note = replay(snap, change, events)
                        self.assertTrue(
                            match,
                            f"kernel approved but oracle disagrees: {note}\n"
                            f"combo={combo} script={script}")
        # not vacuously conservative, not vacuously approving
        self.assertGreater(stats[VALID], 0)
        self.assertGreater(stats[INVALID], 0)
        self.assertGreater(stats[INSUFFICIENT], 0)
        print(f"\nexhaustive: {checked} (trace, change) pairs; "
              f"verdicts={dict(stats)}")

    def test_reverse_index_used_for_all_pairs(self):
        """Every candidate lookup only touches affected keys."""
        vfs = setup_vfs()
        vfs_before_tasks = vfs.snapshot()
        events = []
        for t in TEMPLATES:
            events.extend(t(vfs))
        evidence = build_evidence("task", events)
        idx = ReverseIndex()
        idx.add(evidence)
        snap = vfs_before_tasks.snapshot()
        for script in CHANGES:
            change = apply_change(snap.snapshot(), script)
            idx.reset_access_log()
            cand = idx.candidates(change.affected)
            self.assertEqual(set(idx.accessed), change.affected)
            if "task" in cand:
                verdict_for(evidence, change)


if __name__ == "__main__":
    unittest.main()
