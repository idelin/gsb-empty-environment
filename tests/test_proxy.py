"""Functional, policy, escape, audit and fd-hygiene tests.

These tests use static paths only; they do NOT prove race safety (see
tests/test_races.py for barrier-scheduled interleavings).
"""

import os
import shutil
import sys
import tempfile
import unittest

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

from buildproxy import (
    EscapeAttempt,
    FileProxy,
    ListSink,
    Policy,
    PolicyDenied,
    SymlinkLoop,
)


def fd_count():
    for proc in ("/proc/self/fd", "/dev/fd"):
        if os.path.isdir(proc):
            return len(os.listdir(proc))
    return None


class ProxyTestCase(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.mkdtemp(prefix="proxy-test-")
        self.addCleanup(shutil.rmtree, self.tmp, True)
        self.root = os.path.join(self.tmp, "root")
        self.outside = os.path.join(self.tmp, "outside")
        for d in ("in", "out", "both", "input"):
            os.makedirs(os.path.join(self.root, d))
        os.makedirs(self.outside)
        self.sink = ListSink()
        self.policy = Policy(readable=("in", "both"), writable=("out", "both"))
        self.proxy = FileProxy(self.root, self.policy, audit_sink=self.sink)
        self.addCleanup(self.proxy.close)

    # -- helpers -----------------------------------------------------------

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

    def symlink(self, target, rel):
        os.symlink(target, os.path.join(self.root, rel))

    def last_event(self):
        return self.sink.events[-1]

    # -- basic operations ----------------------------------------------------

    def test_read_ok_and_audit_identity(self):
        path = self.plant("in/a.txt", b"hello")
        self.assertEqual(self.proxy.read("in/a.txt"), b"hello")
        ev = self.last_event()
        self.assertEqual(ev.op, "read")
        self.assertEqual(ev.path, "in/a.txt")
        self.assertEqual(ev.resolved, "in/a.txt")
        self.assertEqual(ev.result, "ok")
        st = os.stat(path)
        self.assertEqual((ev.dev, ev.ino), (st.st_dev, st.st_ino))

    def test_create_list_unlink_cycle(self):
        self.proxy.create("out/new.txt", b"data")
        self.assertIn("new.txt", os.listdir(os.path.join(self.root, "out")))
        # make out/ readable too so we can list it
        self.proxy.close()
        self.policy = Policy(readable=("in", "both", "out"), writable=("out", "both"))
        self.proxy = FileProxy(self.root, self.policy, audit_sink=self.sink)
        self.assertEqual(self.proxy.list("out"), ["new.txt"])
        self.proxy.unlink("out/new.txt")
        self.assertEqual(self.proxy.list("out"), [])

    def test_create_exclusive(self):
        self.plant("out/x.txt", b"old")
        with self.assertRaises(FileExistsError):
            self.proxy.create("out/x.txt", b"new")
        with open(os.path.join(self.root, "out/x.txt"), "rb") as fh:
            self.assertEqual(fh.read(), b"old")

    def test_list_root_needs_policy(self):
        with self.assertRaises(PolicyDenied):
            self.proxy.list("")

    def test_list_on_file_raises(self):
        self.plant("in/a.txt", b"x")
        with self.assertRaises(NotADirectoryError):
            self.proxy.list("in/a.txt")

    def test_read_directory_raises(self):
        with self.assertRaises(IsADirectoryError):
            self.proxy.read("in")

    # -- policy --------------------------------------------------------------

    def test_read_denied_in_write_only_subtree(self):
        self.plant("out/secret.txt", b"secret")
        with self.assertRaises(PolicyDenied):
            self.proxy.read("out/secret.txt")
        ev = self.last_event()
        self.assertEqual(ev.result, "denied")
        self.assertIn("readable", ev.reason)
        self.assertEqual(ev.resolved, "out/secret.txt")

    def test_write_denied_in_read_only_subtree(self):
        with self.assertRaises(PolicyDenied):
            self.proxy.create("in/x.txt", b"x")
        self.assertNotIn("x.txt", os.listdir(os.path.join(self.root, "in")))

    def test_same_prefix_sibling_not_permitted(self):
        # readable subtree is "in"; "input" merely shares a string prefix.
        self.plant("input/x.txt", b"sibling")
        with self.assertRaises(PolicyDenied):
            self.proxy.read("input/x.txt")

    def test_rename_requires_both_permissions(self):
        self.plant("out/a.txt", b"a")
        self.plant("in/b.txt", b"b")
        # writable -> read-only: no create permission on target
        with self.assertRaises(PolicyDenied):
            self.proxy.rename("out/a.txt", "in/a.txt")
        # read-only -> writable: no delete permission on source
        with self.assertRaises(PolicyDenied):
            self.proxy.rename("in/b.txt", "out/b.txt")
        # nothing changed on either path
        self.assertTrue(os.path.exists(os.path.join(self.root, "out/a.txt")))
        self.assertTrue(os.path.exists(os.path.join(self.root, "in/b.txt")))
        self.assertFalse(os.path.exists(os.path.join(self.root, "in/a.txt")))
        self.assertFalse(os.path.exists(os.path.join(self.root, "out/b.txt")))
        ev = self.last_event()
        self.assertEqual(ev.result, "denied")
        self.assertEqual(ev.resolved, "in/b.txt")
        self.assertEqual(ev.resolved2, "out/b.txt")

    def test_rename_ok_within_writable(self):
        self.plant("out/a.txt", b"a")
        self.proxy.rename("out/a.txt", "out/b.txt")
        self.assertFalse(os.path.exists(os.path.join(self.root, "out/a.txt")))
        with open(os.path.join(self.root, "out/b.txt"), "rb") as fh:
            self.assertEqual(fh.read(), b"a")
        ev = self.last_event()
        self.assertEqual(ev.result, "ok")
        self.assertEqual((ev.path, ev.path2), ("out/a.txt", "out/b.txt"))

    def test_rename_never_clobbers_existing_target(self):
        self.plant("out/a.txt", b"a")
        self.plant("out/b.txt", b"b")
        with self.assertRaises(FileExistsError):
            self.proxy.rename("out/a.txt", "out/b.txt")
        with open(os.path.join(self.root, "out/b.txt"), "rb") as fh:
            self.assertEqual(fh.read(), b"b")
        self.assertTrue(os.path.exists(os.path.join(self.root, "out/a.txt")))

    # -- dotdot / normalization ---------------------------------------------

    def test_dotdot_resolves_inside_root(self):
        os.makedirs(os.path.join(self.root, "both/sub"))
        self.plant("both/b.txt", b"B")
        self.assertEqual(self.proxy.read("both/sub/../b.txt"), b"B")
        self.assertEqual(self.last_event().resolved, "both/b.txt")

    def test_dotdot_above_root_rejected(self):
        self.plant("both/b.txt", b"B")
        with self.assertRaises(EscapeAttempt):
            self.proxy.read("both/../../etc/passwd")
        self.assertEqual(self.last_event().result, "denied")

    def test_absolute_path_rejected(self):
        with self.assertRaises(EscapeAttempt):
            self.proxy.read("/etc/passwd")

    # -- symlinks --------------------------------------------------------------

    def test_relative_symlink_inside_root_ok(self):
        self.plant("both/data.txt", b"data")
        self.symlink("../both/data.txt", "in/link")
        self.assertEqual(self.proxy.read("in/link"), b"data")
        self.assertEqual(self.last_event().resolved, "both/data.txt")

    def test_absolute_symlink_rejected(self):
        decoy = self.plant_outside("decoy.txt", b"DECOY")
        self.symlink(decoy, "in/abs")
        with self.assertRaises(EscapeAttempt):
            self.proxy.read("in/abs")
        self.assertEqual(self.last_event().result, "denied")

    def test_symlink_escaping_with_dotdot_rejected(self):
        self.plant_outside("decoy.txt", b"DECOY")
        self.symlink("../../outside/decoy.txt", "in/rel")
        with self.assertRaises(EscapeAttempt):
            self.proxy.read("in/rel")

    def test_symlink_loop_detected(self):
        self.symlink("b", "in/a")
        self.symlink("a", "in/b")
        with self.assertRaises(SymlinkLoop):
            self.proxy.read("in/a")
        self.symlink("self", "in/self")
        with self.assertRaises(SymlinkLoop):
            self.proxy.read("in/self")

    def test_symlink_cannot_smuggle_policy_escape(self):
        # The link lives in a readable subtree but points into a
        # write-only one; policy applies to the *resolved* logical path.
        self.plant("out/secret.txt", b"secret")
        self.symlink("../out/secret.txt", "in/sneak")
        with self.assertRaises(PolicyDenied):
            self.proxy.read("in/sneak")
        self.assertEqual(self.last_event().resolved, "out/secret.txt")

    def test_unlink_removes_link_not_target(self):
        self.plant("out/target.txt", b"target")
        self.symlink("target.txt", "out/link")
        self.proxy.unlink("out/link")
        self.assertTrue(os.path.exists(os.path.join(self.root, "out/target.txt")))
        self.assertFalse(os.path.exists(os.path.join(self.root, "out/link")))

    def test_unlink_denied_keeps_file(self):
        self.plant("in/keep.txt", b"keep")
        with self.assertRaises(PolicyDenied):
            self.proxy.unlink("in/keep.txt")
        self.assertTrue(os.path.exists(os.path.join(self.root, "in/keep.txt")))

    # -- decoy outside root -----------------------------------------------------

    def test_decoy_never_read_or_written(self):
        decoy = self.plant_outside("decoy.txt", b"DECOY")
        self.symlink(decoy, "out/abs-link")
        # read through absolute link: denied, decoy content never returned
        with self.assertRaises(EscapeAttempt):
            self.proxy.read("out/abs-link")
        # replace onto the link replaces the *link itself*, not the decoy
        self.proxy.replace("out/abs-link", b"new content")
        with open(decoy, "rb") as fh:
            self.assertEqual(fh.read(), b"DECOY")
        # create through a symlinked directory pointing outside: denied
        self.symlink(self.outside, "out/dir-link")
        with self.assertRaises(EscapeAttempt):
            self.proxy.create("out/dir-link/pwned.txt", b"PWNED")
        self.assertFalse(os.path.exists(os.path.join(self.outside, "pwned.txt")))
        with open(decoy, "rb") as fh:
            self.assertEqual(fh.read(), b"DECOY")

    # -- replace ---------------------------------------------------------------

    def test_replace_success_and_audit_identity(self):
        self.plant("out/f.txt", b"old")
        self.proxy.replace("out/f.txt", b"new")
        with open(os.path.join(self.root, "out/f.txt"), "rb") as fh:
            self.assertEqual(fh.read(), b"new")
        ev = self.last_event()
        st = os.stat(os.path.join(self.root, "out/f.txt"))
        self.assertEqual((ev.dev, ev.ino), (st.st_dev, st.st_ino))

    def test_replace_failure_injection_leaves_no_partial_output(self):
        self.plant("out/f.txt", b"original")

        def boom(offset, chunk):
            raise OSError("injected write failure")

        self.proxy.close()
        self.proxy = FileProxy(
            self.root, self.policy, audit_sink=self.sink, write_hook=boom
        )
        with self.assertRaises(OSError):
            self.proxy.replace("out/f.txt", b"replacement")
        # official name still holds the old content
        with open(os.path.join(self.root, "out/f.txt"), "rb") as fh:
            self.assertEqual(fh.read(), b"original")
        # no temporary files left behind
        leftovers = [
            n for n in os.listdir(os.path.join(self.root, "out"))
            if ".proxy-tmp-" in n
        ]
        self.assertEqual(leftovers, [])
        ev = self.last_event()
        self.assertEqual(ev.result, "error")

    def test_replace_failure_on_new_file_leaves_nothing(self):
        def boom(offset, chunk):
            raise OSError("injected write failure")

        self.proxy.close()
        self.proxy = FileProxy(
            self.root, self.policy, audit_sink=self.sink, write_hook=boom
        )
        with self.assertRaises(OSError):
            self.proxy.replace("out/brand-new.txt", b"data")
        self.assertEqual(os.listdir(os.path.join(self.root, "out")), [])

    # -- audit -----------------------------------------------------------------

    def test_audit_denied_has_no_fake_identity(self):
        self.plant("out/secret.txt", b"secret")
        with self.assertRaises(PolicyDenied):
            self.proxy.read("out/secret.txt")
        ev = self.last_event()
        self.assertEqual(ev.result, "denied")
        self.assertIsNone(ev.dev)
        self.assertIsNone(ev.ino)
        self.assertTrue(ev.chain)  # pinned directory chain is recorded

    def test_audit_records_caller_path_and_result(self):
        self.plant("in/a.txt", b"x")
        self.proxy.read("in/a.txt")
        self.assertEqual(self.last_event().result, "ok")
        with self.assertRaises(FileNotFoundError):
            self.proxy.read("in/missing.txt")
        ev = self.last_event()
        self.assertEqual(ev.result, "error")
        self.assertEqual(ev.path, "in/missing.txt")

    # -- fd hygiene --------------------------------------------------------------

    @unittest.skipIf(fd_count() is None, "need /proc/self/fd or /dev/fd")
    def test_fds_released_on_success_and_failure(self):
        self.plant("in/a.txt", b"x")
        self.plant("out/o.txt", b"y")
        self.symlink("loop", "in/loop")
        baseline = fd_count()

        def boom(offset, chunk):
            raise OSError("injected")

        failing = FileProxy(
            self.root, self.policy, write_hook=boom
        )
        for _ in range(5):
            self.proxy.read("in/a.txt")
            self.proxy.create("out/c.txt", b"c")
            self.proxy.unlink("out/c.txt")
            self.proxy.replace("out/o.txt", b"z")
            self.proxy.rename("out/o.txt", "out/o2.txt")
            self.proxy.rename("out/o2.txt", "out/o.txt")
            for thunk in (
                lambda: self.proxy.read("in/loop"),            # symlink loop
                lambda: self.proxy.read("in/../../etc/passwd"),  # escape
                lambda: self.proxy.read("out/o.txt"),          # policy denied
                lambda: self.proxy.read("in/missing.txt"),     # ENOENT
                lambda: failing.replace("out/o.txt", b"q"),    # injected failure
            ):
                try:
                    thunk()
                except Exception:
                    pass
        failing.close()
        self.assertEqual(fd_count(), baseline)


if __name__ == "__main__":
    unittest.main()

class CreateFailureTestCase(unittest.TestCase):
    def test_create_failure_injection_leaves_no_partial_file(self):
        tmp = tempfile.mkdtemp(prefix="proxy-test-")
        self.addCleanup(shutil.rmtree, tmp, True)
        root = os.path.join(tmp, "root")
        os.makedirs(os.path.join(root, "out"))

        def boom(offset, chunk):
            raise OSError("injected write failure")

        proxy = FileProxy(
            root, Policy(writable=("out",)), write_hook=boom
        )
        self.addCleanup(proxy.close)
        with self.assertRaises(OSError):
            proxy.create("out/partial.txt", b"data")
        self.assertEqual(os.listdir(os.path.join(root, "out")), [])
