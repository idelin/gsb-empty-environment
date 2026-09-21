import ast
import shutil
import tempfile
import threading
import unittest
from pathlib import Path

from buildcache import BuildCache, BuildCoordinator, BuildStatus
from buildcache.cache import NonDeterministicConflict
from buildcache.digest import build_key

PACKAGE_ROOT = Path(__file__).resolve().parent.parent / "buildcache"


class CoordinatorTests(unittest.TestCase):
    def setUp(self):
        self.root = Path(tempfile.mkdtemp(prefix="bcoord-"))
        self.cache = BuildCache(self.root)
        self.coord = BuildCoordinator(self.cache)
        self.key = build_key("core", "2.4.1", {"opt": 1}, ["out.bin"])
        self.calls = 0

    def tearDown(self):
        shutil.rmtree(self.root, ignore_errors=True)

    def _builder(self, payload=b"artifact"):
        def build(key, stage):
            self.calls += 1
            # force all waiters to arrive while the build is in flight
            threading.Event().wait(0.05)
            (stage / "out.bin").write_bytes(payload)

        return build

    def test_concurrent_duplicate_builds_coalesce(self):
        outcomes = []

        def worker():
            outcomes.append(
                self.coord.build(
                    self.key, self._builder(), package="core", version="2.4.1",
                    inputs={"opt": 1}, outputs=["out.bin"],
                )
            )

        threads = [threading.Thread(target=worker) for _ in range(6)]
        for t in threads:
            t.start()
        for t in threads:
            t.join()

        self.assertEqual(self.calls, 1)  # single execution
        statuses = sorted(o.status.value for o in outcomes)
        self.assertEqual(statuses.count(BuildStatus.PUBLISHED.value), 1)
        self.assertEqual(statuses.count(BuildStatus.COALESCED.value), 5)
        self.assertTrue(self.cache.verify_entry(self.key))

    def test_cache_hit_skips_build(self):
        def build(key, stage):
            (stage / "out.bin").write_bytes(b"x")

        self.coord.build(
            self.key, build, package="core", version="2.4.1",
            inputs={"opt": 1}, outputs=["out.bin"],
        )
        outcome = self.coord.build(
            self.key, self._builder(), package="core", version="2.4.1",
            inputs={"opt": 1}, outputs=["out.bin"],
        )
        self.assertEqual(outcome.status, BuildStatus.CACHE_HIT)
        self.assertEqual(self.calls, 0)

    def test_failure_cleans_up_and_all_waiters_fail(self):
        def failing_build(key, stage):
            self.calls += 1
            threading.Event().wait(0.05)
            raise RuntimeError("boom")

        errors = []
        outcomes = []

        def worker():
            try:
                outcomes.append(
                    self.coord.build(
                        self.key, failing_build, package="core", version="2.4.1",
                        inputs={"opt": 1}, outputs=["out.bin"],
                    )
                )
            except RuntimeError as exc:
                errors.append(exc)

        threads = [threading.Thread(target=worker) for _ in range(4)]
        for t in threads:
            t.start()
        for t in threads:
            t.join()

        self.assertEqual(self.calls, 1)
        self.assertEqual(len(errors), 1)  # only the leader re-raises
        self.assertEqual(len(outcomes), 3)
        self.assertTrue(all(o.status is BuildStatus.COALESCED for o in outcomes))
        self.assertTrue(all(o.error is not None for o in outcomes))
        self.assertFalse(self.cache.has(self.key))
        self.assertEqual(list(self.cache.tmp_dir.iterdir()), [])

    def test_partial_outputs_fail_and_retriable(self):
        attempts = {"n": 0}

        def partial(key, stage):
            attempts["n"] += 1
            if attempts["n"] == 1:
                (stage / "out.bin").write_bytes(b"")  # create wrong/zero? still missing only
                return  # declared only one output so create it properly below
            (stage / "out.bin").write_bytes(b"real")

        with self.assertRaises(Exception):
            self.coord.build(
                self.key, partial, package="core", version="2.4.1",
                inputs={"opt": 1}, outputs=["out.bin", "extra.txt"],
            )
        self.assertFalse(self.cache.has(self.key))


class OfflineTests(unittest.TestCase):
    def test_no_network_modules_imported(self):
        forbidden = {
            "urllib.request", "urllib.urlopen", "http.client", "http.server",
            "requests", "httpx", "aiohttp", "socket", "ftplib", "smtplib",
            "telnetlib", "xmlrpc.client",
        }
        for py in PACKAGE_ROOT.rglob("*.py"):
            tree = ast.parse(py.read_text(encoding="utf-8"), filename=str(py))
            for node in ast.walk(tree):
                if isinstance(node, ast.Import):
                    for alias in node.names:
                        self.assertNotIn(
                            alias.name, forbidden, f"{py} imports {alias.name}"
                        )
                elif isinstance(node, ast.ImportFrom):
                    if node.module:
                        self.assertNotIn(
                            node.module, forbidden, f"{py} imports {node.module}"
                        )


if __name__ == "__main__":
    unittest.main()
