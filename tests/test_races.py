"""Race tests with barrier-scheduled interleavings.

These tests do not merely exercise static paths: they use the proxy's
trace hooks plus ``threading.Barrier`` to *deterministically* schedule the
dangerous interleavings from the threat model:

* a symlink is replaced after a component was checked but before it is
  opened/read (``component_checked`` / ``final_open`` hooks);
* a rename target is created concurrently between the policy check and
  the atomic rename (``rename_commit`` hook).

Each test asserts the security property (decoy outside the root is never
read or written; no clobbering; audit identity matches the object actually
accessed), not a particular scheduling.
"""

import os
import shutil
import sys
import tempfile
import threading
import unittest

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

from buildproxy import (
    EscapeAttempt,
    FileProxy,
    ListSink,
    Policy,
    PolicyDenied,
)

BARRIER_TIMEOUT = 15


class RaceTestCase(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.mkdtemp(prefix="proxy-race-")
        self.addCleanup(shutil.rmtree, self.tmp, True)
        self.root = os.path.join(self.tmp, "root")
        self.outside = os.path.join(self.tmp, "outside")
        os.makedirs(os.path.join(self.root, "in"))
        os.makedirs(os.path.join(self.root, "out"))
        os.makedirs(self.outside)
        self.sink = ListSink()
        self.policy = Policy(readable=("in",), writable=("out",))

    def plant(self, rel, data=b""):
        path = os.path.join(self.root, rel)
        os.makedirs(os.path.dirname(path), exist_ok=True)
        with open(path, "wb") as fh:
            fh.write(data)
        return path

    def plant_outside(self, rel, data=b""):
        path = os.path.join(self.outside, rel)
        os.makedirs(os.path.dirname(path), exist_ok=True)
        with open(path, "wb") as fh:
            fh.write(data)
        return path

    def run_threads(self, *targets):
        threads = [threading.Thread(target=t) for t in targets]
        for t in threads:
            t.start()
        for t in threads:
            t.join(BARRIER_TIMEOUT * 2)
        for t in threads:
            self.assertFalse(t.is_alive(), "thread stuck (barrier timeout?)")

    # -- 1. link swap after component check, before open ---------------------

    def test_link_swap_after_check_before_open(self):
        """`in/link` is checked as a safe relative symlink, then swapped to
        an absolute symlink at the decoy before the proxy uses it.  The
        proxy must re-validate and refuse; the decoy must never be read."""
        decoy = self.plant_outside("decoy.txt", b"DECOY-OUTSIDE-ROOT")
        self.plant("in/real/file.txt", b"REAL")
        os.symlink("real", os.path.join(self.root, "in/link"))

        barrier = threading.Barrier(2)
        swapped = threading.Event()
        result = {}

        def trace(event, **fields):
            if (
                event == "component_checked"
                and fields.get("component") == "link"
                and not swapped.is_set()
            ):
                # checked: pause here so the swap lands before readlink
                barrier.wait(BARRIER_TIMEOUT)
                swapped.wait(BARRIER_TIMEOUT)

        proxy = FileProxy(
            self.root, self.policy, audit_sink=self.sink, trace_hook=trace
        )
        self.addCleanup(proxy.close)

        def reader():
            try:
                result["data"] = proxy.read("in/link/file.txt")
            except Exception as exc:  # noqa: BLE001 - asserted below
                result["exc"] = exc

        def swapper():
            barrier.wait(BARRIER_TIMEOUT)
            link = os.path.join(self.root, "in/link")
            os.unlink(link)
            os.symlink(decoy, link)  # absolute symlink to outside decoy
            swapped.set()

        self.run_threads(reader, swapper)

        self.assertNotIn("data", result, "decoy content must never be returned")
        self.assertIsInstance(result.get("exc"), EscapeAttempt)
        with open(decoy, "rb") as fh:
            self.assertEqual(fh.read(), b"DECOY-OUTSIDE-ROOT")
        denied = [e for e in self.sink.events if e.result == "denied"]
        self.assertTrue(denied, "expected a denied audit event")

    def test_final_component_swap_triggers_revalidation(self):
        """The final component is checked as a regular file, then swapped
        for a symlink to the decoy before the open.  O_NOFOLLOW makes the
        open fail, the proxy re-resolves, and the escape is refused."""
        decoy = self.plant_outside("decoy.txt", b"DECOY-OUTSIDE-ROOT")
        self.plant("in/victim.txt", b"GOOD")

        barrier = threading.Barrier(2)
        swapped = threading.Event()
        result = {}

        def trace(event, **fields):
            if event == "final_open" and not swapped.is_set():
                barrier.wait(BARRIER_TIMEOUT)
                swapped.wait(BARRIER_TIMEOUT)

        proxy = FileProxy(
            self.root, self.policy, audit_sink=self.sink, trace_hook=trace
        )
        self.addCleanup(proxy.close)

        def reader():
            try:
                result["data"] = proxy.read("in/victim.txt")
            except Exception as exc:  # noqa: BLE001
                result["exc"] = exc

        def swapper():
            barrier.wait(BARRIER_TIMEOUT)
            victim = os.path.join(self.root, "in/victim.txt")
            os.unlink(victim)
            os.symlink(decoy, victim)
            swapped.set()

        self.run_threads(reader, swapper)

        self.assertNotIn("data", result, "decoy content must never be returned")
        self.assertIsInstance(result.get("exc"), EscapeAttempt)
        with open(decoy, "rb") as fh:
            self.assertEqual(fh.read(), b"DECOY-OUTSIDE-ROOT")

    def test_swap_to_relative_escape_rejected(self):
        """Same interleaving, but the swapped-in symlink uses '..' to reach
        the decoy; the resolver must reject the escape above the root."""
        decoy = self.plant_outside("decoy.txt", b"DECOY-OUTSIDE-ROOT")
        self.plant("in/real/file.txt", b"REAL")
        os.symlink("real", os.path.join(self.root, "in/link"))

        barrier = threading.Barrier(2)
        swapped = threading.Event()
        result = {}

        def trace(event, **fields):
            if (
                event == "component_checked"
                and fields.get("component") == "link"
                and not swapped.is_set()
            ):
                barrier.wait(BARRIER_TIMEOUT)
                swapped.wait(BARRIER_TIMEOUT)

        proxy = FileProxy(
            self.root, self.policy, audit_sink=self.sink, trace_hook=trace
        )
        self.addCleanup(proxy.close)

        def reader():
            try:
                result["data"] = proxy.read("in/link/file.txt")
            except Exception as exc:  # noqa: BLE001
                result["exc"] = exc

        def swapper():
            barrier.wait(BARRIER_TIMEOUT)
            link = os.path.join(self.root, "in/link")
            os.unlink(link)
            os.symlink("../../../outside/decoy.txt", link)
            swapped.set()

        self.run_threads(reader, swapper)

        self.assertNotIn("data", result)
        self.assertIsInstance(result.get("exc"), EscapeAttempt)
        with open(decoy, "rb") as fh:
            self.assertEqual(fh.read(), b"DECOY-OUTSIDE-ROOT")

    # -- 2. fd pinning: swap after resolution cannot redirect the read --------

    def test_resolved_dirfd_is_pinned_against_later_swap(self):
        """Once resolution has opened the target directory, swapping the
        symlink cannot redirect the operation; the audit identity must be
        the object actually read, not the check-time path."""
        self.plant("in/dirA/file.txt", b"FROM-A")
        self.plant("in/dirB/file.txt", b"FROM-B")
        os.symlink("dirA", os.path.join(self.root, "in/link"))

        barrier = threading.Barrier(2)
        swapped = threading.Event()
        result = {}

        def trace(event, **fields):
            if event == "final_open" and not swapped.is_set():
                barrier.wait(BARRIER_TIMEOUT)
                swapped.wait(BARRIER_TIMEOUT)

        proxy = FileProxy(
            self.root, self.policy, audit_sink=self.sink, trace_hook=trace
        )
        self.addCleanup(proxy.close)

        def reader():
            result["data"] = proxy.read("in/link/file.txt")

        def swapper():
            barrier.wait(BARRIER_TIMEOUT)
            link = os.path.join(self.root, "in/link")
            os.unlink(link)
            os.symlink("dirB", link)
            swapped.set()

        self.run_threads(reader, swapper)

        # The dirfd for dirA was already pinned: the read serves dirA.
        self.assertEqual(result["data"], b"FROM-A")
        ev = self.sink.events[-1]
        sta = os.stat(os.path.join(self.root, "in/dirA/file.txt"))
        stb = os.stat(os.path.join(self.root, "in/dirB/file.txt"))
        self.assertEqual((ev.dev, ev.ino), (sta.st_dev, sta.st_ino))
        self.assertNotEqual((ev.dev, ev.ino), (stb.st_dev, stb.st_ino))

    # -- 3. rename target concurrently occupied --------------------------------

    def test_rename_target_occupied_between_check_and_rename(self):
        """The target name is free when checked, then occupied before the
        atomic rename.  renameat2(RENAME_NOREPLACE) must fail cleanly:
        neither path changes and the occupant's content survives."""
        self.plant("out/a.txt", b"AAA")

        barrier = threading.Barrier(2)
        occupied = threading.Event()
        result = {}

        def trace(event, **fields):
            if event == "rename_commit" and not occupied.is_set():
                barrier.wait(BARRIER_TIMEOUT)
                occupied.wait(BARRIER_TIMEOUT)

        proxy = FileProxy(
            self.root, self.policy, audit_sink=self.sink, trace_hook=trace
        )
        self.addCleanup(proxy.close)

        def renamer():
            try:
                proxy.rename("out/a.txt", "out/b.txt")
                result["renamed"] = True
            except Exception as exc:  # noqa: BLE001
                result["exc"] = exc

        def occupier():
            barrier.wait(BARRIER_TIMEOUT)
            with open(os.path.join(self.root, "out/b.txt"), "wb") as fh:
                fh.write(b"OCCUPIED")
            occupied.set()

        self.run_threads(renamer, occupier)

        self.assertNotIn("renamed", result)
        self.assertIsInstance(result.get("exc"), FileExistsError)
        # rejected operation changed neither path
        with open(os.path.join(self.root, "out/a.txt"), "rb") as fh:
            self.assertEqual(fh.read(), b"AAA")
        with open(os.path.join(self.root, "out/b.txt"), "rb") as fh:
            self.assertEqual(fh.read(), b"OCCUPIED")
        ev = self.sink.events[-1]
        self.assertEqual(ev.result, "error")
        self.assertEqual(ev.op, "rename")

    # -- 4. replace commit race: target swapped for outside symlink -----------

    def test_replace_commit_never_writes_through_swapped_symlink(self):
        """Between temp-file write and the atomic rename, the target name is
        swapped for a symlink to the decoy.  renameat replaces the *link*,
        so the decoy outside the root is never written."""
        decoy = self.plant_outside("decoy.txt", b"DECOY-OUTSIDE-ROOT")
        self.plant("out/f.txt", b"OLD")

        barrier = threading.Barrier(2)
        swapped = threading.Event()

        def trace(event, **fields):
            if event == "replace_commit" and not swapped.is_set():
                barrier.wait(BARRIER_TIMEOUT)
                swapped.wait(BARRIER_TIMEOUT)

        proxy = FileProxy(
            self.root, self.policy, audit_sink=self.sink, trace_hook=trace
        )
        self.addCleanup(proxy.close)

        def replacer():
            proxy.replace("out/f.txt", b"NEW")

        def swapper():
            barrier.wait(BARRIER_TIMEOUT)
            target = os.path.join(self.root, "out/f.txt")
            os.unlink(target)
            os.symlink(decoy, target)
            swapped.set()

        self.run_threads(replacer, swapper)

        with open(decoy, "rb") as fh:
            self.assertEqual(fh.read(), b"DECOY-OUTSIDE-ROOT")
        # the official name is now a regular file with the new content
        # (the symlink itself was atomically replaced)
        path = os.path.join(self.root, "out/f.txt")
        self.assertFalse(os.path.islink(path))
        with open(path, "rb") as fh:
            self.assertEqual(fh.read(), b"NEW")


if __name__ == "__main__":
    unittest.main()
