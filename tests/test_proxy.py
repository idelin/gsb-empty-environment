"""Linux tests for fsproxy.

Run from the repo root:  python3.11 -m unittest discover -s tests -v

The race tests use threading events as barriers to hit exact interleavings
(symlink swapped between component check and open; rename target created
between authorization and rename(2)).  They demonstrate those specific
interleavings are handled; they are not a proof of race safety -- only the
dirfd/O_NOFOLLOW design argument is.
"""

import os
import shutil
import sys
import tempfile
import threading
import unittest

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

from fsproxy import (  # noqa: E402
    BuildFSProxy,
    EscapeError,
    Hooks,
    ObjectId,
    Policy,
    PolicyDenied,
    SymlinkLimitExceeded,
)

DECOY = b"DECOY-SECRET-NEVER-TOUCH"
SEED = b"seed-data"
WAIT = 10  # seconds; generous so slow CI never deadlocks silently


def make_tree(tmp):
    root = os.path.join(tmp, "root")
    os.makedirs(os.path.join(root, "in", "sub"))
    os.makedirs(os.path.join(root, "out"))
    with open(os.path.join(root, "in", "seed.txt"), "wb") as f:
        f.write(SEED)
    with open(os.path.join(root, "in", "sub", "deep.txt"), "wb") as f:
        f.write(b"deep")
    outside = os.path.join(tmp, "outside")
    os.makedirs(outside)
    with open(os.path.join(outside, "decoy.txt"), "wb") as f:
        f.write(DECOY)
    return root, outside


class ProxyCase(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.mkdtemp(prefix="fsproxy-test-")
        self.addCleanup(shutil.rmtree, self.tmp, True)
        self.root, self.outside = make_tree(self.tmp)
        self.decoy = os.path.join(self.outside, "decoy.txt")

    def proxy(self, policy=None, hooks=None):
        if policy is None:
            policy = Policy(readable=["in"], writable=["out"])
        p = BuildFSProxy(self.root, policy, hooks=hooks)
        self.addCleanup(p.close)
        return p

    def rpath(self, *parts):
        return os.path.join(self.root, *parts)

    def decoy_content(self):
        with open(self.decoy, "rb") as f:
            return f.read()


class TestStaticPaths(ProxyCase):
    """Static-path behaviour.  Passing these says nothing about races."""

    def test_read_and_list(self):
        p = self.proxy()
        self.assertEqual(p.read("in/seed.txt"), SEED)
        self.assertEqual(p.list("in"), ["seed.txt", "sub"])
        with self.assertRaises(PolicyDenied):
            p.list("")  # the root itself is not a readable subtree
        p_all = self.proxy(Policy(readable=[""], writable=["out"]))
        self.assertEqual(p_all.list(""), ["in", "out"])
        with self.assertRaises(NotADirectoryError):
            p.list("in/seed.txt")

    def test_dotdot_resolution(self):
        p = self.proxy()
        self.assertEqual(p.read("in/sub/../seed.txt"), SEED)
        self.assertEqual(p.audit[-1].resolved, "in/seed.txt")
        # same thing through a symlink whose target contains ".."
        os.symlink("sub/../seed.txt", self.rpath("in", "link"))
        self.assertEqual(p.read("in/link"), SEED)
        self.assertEqual(p.audit[-1].resolved, "in/seed.txt")

    def test_dotdot_escape_denied(self):
        p = self.proxy()
        with self.assertRaises(EscapeError):
            p.read("../outside/decoy.txt")
        with self.assertRaises(EscapeError):
            p.read("in/../../outside/decoy.txt")
        self.assertEqual(self.decoy_content(), DECOY)

    def test_absolute_symlink_denied(self):
        os.symlink(self.decoy, self.rpath("in", "abs"))  # absolute target
        p = self.proxy()
        with self.assertRaises(EscapeError):
            p.read("in/abs")
        self.assertEqual(self.decoy_content(), DECOY)

    def test_relative_escape_symlink_denied(self):
        os.symlink("../../outside/decoy.txt", self.rpath("in", "evil"))
        os.symlink("../outside/decoy.txt", self.rpath("evil2"))
        p = self.proxy(Policy(readable=[""], writable=["out"]))
        for name in ("in/evil", "evil2"):
            with self.assertRaises(EscapeError):
                p.read(name)
        self.assertEqual(self.decoy_content(), DECOY)

    def test_write_through_escape_symlink_denied(self):
        os.symlink("../../outside", self.rpath("out", "esc"))
        p = self.proxy()
        with self.assertRaises(EscapeError):
            p.replace("out/esc/decoy.txt", b"pwned")
        self.assertEqual(self.decoy_content(), DECOY)

    def test_same_prefix_sibling_not_matched(self):
        os.makedirs(self.rpath("input"))
        with open(self.rpath("input", "notes.txt"), "wb") as f:
            f.write(b"sibling")
        os.makedirs(self.rpath("out2"))
        p = self.proxy()  # readable=["in"], writable=["out"]
        with self.assertRaises(PolicyDenied):
            p.read("input/notes.txt")
        with self.assertRaises(PolicyDenied):
            p.create("out2/x")

    def test_symlink_loop_denied(self):
        os.symlink("loop2", self.rpath("in", "loop1"))
        os.symlink("loop1", self.rpath("in", "loop2"))
        p = self.proxy()
        with self.assertRaises(SymlinkLimitExceeded):
            p.read("in/loop1")
        self.assertEqual(p.audit[-1].result, "denied")

    def test_policy_applies_to_resolved_path(self):
        # symlink inside readable "in" pointing into write-only "out"
        with open(self.rpath("out", "hidden.txt"), "wb") as f:
            f.write(b"output-only")
        os.symlink("../out/hidden.txt", self.rpath("in", "peek"))
        p = self.proxy()  # "out" is writable but NOT readable
        with self.assertRaises(PolicyDenied):
            p.read("in/peek")
        self.assertEqual(p.audit[-1].resolved, "out/hidden.txt")


class TestWrites(ProxyCase):
    def test_create_replace_unlink_cycle(self):
        p = self.proxy()
        p.create("out/a.txt", b"v1")
        with open(self.rpath("out", "a.txt"), "rb") as f:
            self.assertEqual(f.read(), b"v1")
        p.replace("out/a.txt", b"v2")
        with open(self.rpath("out", "a.txt"), "rb") as f:
            self.assertEqual(f.read(), b"v2")
        with self.assertRaises(FileExistsError):
            p.create("out/a.txt", b"v3")
        p.unlink("out/a.txt")
        self.assertFalse(os.path.exists(self.rpath("out", "a.txt")))

    def test_denied_writes_change_nothing(self):
        p = self.proxy()
        for call in (
            lambda: p.create("in/new.txt", b"x"),
            lambda: p.replace("in/seed.txt", b"x"),
            lambda: p.unlink("in/seed.txt"),
        ):
            with self.assertRaises(PolicyDenied):
                call()
        self.assertEqual(self.decoy_content(), DECOY)
        with open(self.rpath("in", "seed.txt"), "rb") as f:
            self.assertEqual(f.read(), SEED)
        self.assertFalse(os.path.exists(self.rpath("in", "new.txt")))

    def test_replace_creates_missing_and_leaves_no_temp(self):
        p = self.proxy(Policy(readable=["in", "out"], writable=["out"]))
        p.replace("out/fresh.txt", b"fresh")
        self.assertEqual(p.list("out"), ["fresh.txt"])

    def test_injected_write_failure_leaves_no_partial_output(self):
        class FailSecondChunk(Hooks):
            def on_write_chunk(self, *, fd, offset, chunk):
                if offset > 0:
                    raise OSError(5, "injected write failure")  # EIO

        p = self.proxy(Policy(readable=["in", "out"], writable=["out"]),
                       hooks=FailSecondChunk())
        p.create("out/big.txt", b"old")
        payload = b"A" * 200_000  # spans several chunks
        with self.assertRaises(OSError):
            p.replace("out/big.txt", payload)
        self.assertEqual(p.audit[-1].op, "replace")
        self.assertEqual(p.audit[-1].result, "error")
        with open(self.rpath("out", "big.txt"), "rb") as f:
            self.assertEqual(f.read(), b"old")  # official output untouched
        self.assertEqual(p.list("out"), ["big.txt"])  # temp file cleaned up

    def test_rename_needs_both_permissions(self):
        p = self.proxy()
        p.create("out/a.txt", b"A")
        p.rename("out/a.txt", "out/b.txt")  # out -> out: allowed
        self.assertFalse(os.path.exists(self.rpath("out", "a.txt")))
        with open(self.rpath("out", "b.txt"), "rb") as f:
            self.assertEqual(f.read(), b"A")
        # out -> in: target create denied; neither path changes
        with self.assertRaises(PolicyDenied):
            p.rename("out/b.txt", "in/c.txt")
        self.assertTrue(os.path.exists(self.rpath("out", "b.txt")))
        self.assertFalse(os.path.exists(self.rpath("in", "c.txt")))
        # in -> out: source delete denied; neither path changes
        with self.assertRaises(PolicyDenied):
            p.rename("in/seed.txt", "out/copied.txt")
        self.assertTrue(os.path.exists(self.rpath("in", "seed.txt")))
        self.assertFalse(os.path.exists(self.rpath("out", "copied.txt")))


class TestAudit(ProxyCase):
    def test_audit_records_actual_object_identity(self):
        p = self.proxy()
        p.read("in/seed.txt")
        ev = p.audit[-1]
        st = os.stat(self.rpath("in", "seed.txt"))
        self.assertEqual(ev.result, "ok")
        self.assertEqual(ev.resolved, "in/seed.txt")
        self.assertIn(ObjectId(st.st_dev, st.st_ino), ev.objects)

    def test_denied_events_carry_reason(self):
        p = self.proxy()
        with open(self.rpath("out", "x"), "wb") as f:
            f.write(b"x")
        with self.assertRaises(PolicyDenied):
            p.read("out/x")
        ev = p.audit[-1]
        self.assertEqual(ev.result, "denied")
        self.assertIn("read not permitted", ev.reason)

    def test_rename_event_covers_both_paths(self):
        p = self.proxy()
        p.create("out/a", b"")
        p.rename("out/a", "out/b")
        ev = p.audit[-1]
        self.assertEqual((ev.path, ev.extra["dst"]), ("out/a", "out/b"))
        self.assertEqual((ev.resolved, ev.extra["resolved_dst"]), ("out/a", "out/b"))
        self.assertEqual(len(ev.objects), 2)


class TestRaces(ProxyCase):
    """Barrier-orchestrated interleavings against a whole-root policy."""

    def open_policy(self):
        return Policy(readable=[""], writable=[""])

    def test_symlink_swap_between_check_and_open_escape(self):
        os.mkdir(self.rpath("victim"))
        checked, proceed = threading.Event(), threading.Event()

        class SwapHooks(Hooks):
            fired = False

            def after_component_check(self, *, name, **kw):
                if name == "victim" and not self.fired:
                    self.fired = True
                    checked.set()
                    proceed.wait(WAIT)

        p = self.proxy(self.open_policy(), SwapHooks())
        outcome = {}

        def do_read():
            try:
                outcome["data"] = p.read("victim/payload.txt")
            except Exception as exc:  # noqa: BLE001
                outcome["exc"] = exc

        t = threading.Thread(target=do_read)
        t.start()
        self.assertTrue(checked.wait(WAIT))
        # racer: replace the checked directory with an escaping symlink
        os.rmdir(self.rpath("victim"))
        os.symlink("../outside/decoy.txt", self.rpath("victim"))
        proceed.set()
        t.join(WAIT)
        self.assertFalse(t.is_alive())
        self.assertNotIn("data", outcome)
        self.assertIsInstance(outcome.get("exc"), EscapeError)
        self.assertEqual(self.decoy_content(), DECOY)  # decoy never read
        self.assertEqual(p.audit[-1].result, "denied")

    def test_symlink_swap_between_check_and_open_internal(self):
        os.mkdir(self.rpath("victim"))
        os.mkdir(self.rpath("real"))
        with open(self.rpath("real", "payload.txt"), "wb") as f:
            f.write(b"REAL-DATA")
        checked, proceed = threading.Event(), threading.Event()

        class SwapHooks(Hooks):
            fired = False

            def after_component_check(self, *, name, **kw):
                if name == "victim" and not self.fired:
                    self.fired = True
                    checked.set()
                    proceed.wait(WAIT)

        p = self.proxy(self.open_policy(), SwapHooks())
        outcome = {}

        def do_read():
            try:
                outcome["data"] = p.read("victim/payload.txt")
            except Exception as exc:  # noqa: BLE001
                outcome["exc"] = exc

        t = threading.Thread(target=do_read)
        t.start()
        self.assertTrue(checked.wait(WAIT))
        os.rmdir(self.rpath("victim"))
        os.symlink("real", self.rpath("victim"))  # in-root redirect
        proceed.set()
        t.join(WAIT)
        self.assertFalse(t.is_alive())
        self.assertEqual(outcome.get("data"), b"REAL-DATA")
        # audit must name the final object, not the check-time path
        ev = p.audit[-1]
        self.assertEqual(ev.resolved, "real/payload.txt")
        st = os.stat(self.rpath("real", "payload.txt"))
        self.assertIn(ObjectId(st.st_dev, st.st_ino), ev.objects)

    def test_rename_target_created_concurrently(self):
        with open(self.rpath("a"), "wb") as f:
            f.write(b"A")
        checked, proceed = threading.Event(), threading.Event()

        class RenameHooks(Hooks):
            def before_rename_syscall(self, **kw):
                checked.set()
                proceed.wait(WAIT)

        p = self.proxy(self.open_policy(), RenameHooks())
        outcome = {}

        def do_rename():
            try:
                p.rename("a", "b")
                outcome["ok"] = True
            except Exception as exc:  # noqa: BLE001
                outcome["exc"] = exc

        t = threading.Thread(target=do_rename)
        t.start()
        self.assertTrue(checked.wait(WAIT))
        with open(self.rpath("b"), "wb") as f:  # racer occupies the target
            f.write(b"OCCUPIED")
        proceed.set()
        t.join(WAIT)
        self.assertFalse(t.is_alive())
        self.assertTrue(outcome.get("ok"), outcome)
        with open(self.rpath("b"), "rb") as f:
            self.assertEqual(f.read(), b"A")  # atomic replacement, not a mix
        self.assertFalse(os.path.exists(self.rpath("a")))

    def test_rename_onto_nonempty_dir_fails_without_changes(self):
        with open(self.rpath("a"), "wb") as f:
            f.write(b"A")
        checked, proceed = threading.Event(), threading.Event()

        class RenameHooks(Hooks):
            def before_rename_syscall(self, **kw):
                checked.set()
                proceed.wait(WAIT)

        p = self.proxy(self.open_policy(), RenameHooks())
        outcome = {}

        def do_rename():
            try:
                p.rename("a", "b")
                outcome["ok"] = True
            except OSError as exc:
                outcome["exc"] = exc

        t = threading.Thread(target=do_rename)
        t.start()
        self.assertTrue(checked.wait(WAIT))
        os.mkdir(self.rpath("b"))  # racer occupies target with non-empty dir
        with open(self.rpath("b", "keep"), "wb") as f:
            f.write(b"keep")
        proceed.set()
        t.join(WAIT)
        self.assertFalse(t.is_alive())
        self.assertNotIn("ok", outcome)
        self.assertIsInstance(outcome.get("exc"), OSError)
        with open(self.rpath("a"), "rb") as f:  # source untouched
            self.assertEqual(f.read(), b"A")
        with open(self.rpath("b", "keep"), "rb") as f:  # target untouched
            self.assertEqual(f.read(), b"keep")
        self.assertEqual(p.audit[-1].result, "error")


@unittest.skipUnless(os.path.isdir("/proc/self/fd"), "needs Linux /proc")
class TestFdRelease(ProxyCase):
    @staticmethod
    def fd_count():
        return len(os.listdir("/proc/self/fd"))

    def test_no_fd_leak_on_success_and_failure(self):
        class FailingHooks(Hooks):
            def on_write_chunk(self, *, fd, offset, chunk):
                raise OSError(5, "injected")

        p = self.proxy()
        failing = BuildFSProxy(self.root, Policy(readable=["in"], writable=["out"]),
                               hooks=FailingHooks())
        self.addCleanup(failing.close)

        def exercise():
            p.read("in/seed.txt")
            p.list("")
            p.create("out/t", b"x")
            p.replace("out/t", b"y")
            p.rename("out/t", "out/t2")
            p.unlink("out/t2")
            for call in (lambda: p.read("out/t"),
                         lambda: p.read("../escape"),
                         lambda: p.read("in/missing"),
                         lambda: p.unlink("in/seed.txt"),
                         lambda: failing.replace("out/z", b"data")):
                try:
                    call()
                except Exception:  # noqa: BLE001
                    pass

        exercise()  # warm-up
        before = self.fd_count()
        for _ in range(20):
            exercise()
        self.assertEqual(self.fd_count(), before)


if __name__ == "__main__":
    unittest.main()
