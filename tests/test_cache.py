import json
import tempfile
import unittest
from pathlib import Path

from buildsys.cache import Cache
from buildsys.model import BuildInput, RootRequirement, TargetContext
from buildsys.resolver import resolve
from buildsys.version import Constraint

from tests.test_resolver import example_index, roots


def make_input(os_, arch, stage="build"):
    return BuildInput(
        context=TargetContext(os=os_, arch=arch, stage=stage),
        roots=roots(("app", "==1.0.0")),
        host_os="macos",
        host_arch="arm64",
    )


class TestCacheLayout(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.cache = Cache(Path(self.tmp.name) / "cache")
        self.index = example_index()

    def plan(self, build_input):
        resolution = resolve(self.index, build_input.context, build_input.roots)
        return self.cache.materialize(resolution, build_input)

    def test_host_and_target_cached_separately(self):
        entries = self.plan(make_input("linux", "x86_64"))
        by_name = {e.name: e for e in entries}
        self.assertIn("host-macos-arm64", str(by_name["codegen"].path))
        self.assertIn("host-macos-arm64", str(by_name["libtempl"].path))
        self.assertIn("target-linux-x86_64", str(by_name["guilib"].path))
        self.assertIn("target-linux-x86_64", str(by_name["posixsock"].path))
        for entry in entries:
            self.assertEqual(entry.status, "created")
            manifest = json.loads((entry.path / "manifest.json").read_text())
            self.assertEqual(manifest["name"], entry.name)
            self.assertEqual(manifest["domain"], entry.domain)

    def test_two_targets_share_host_tools(self):
        first = self.plan(make_input("linux", "x86_64"))
        second = self.plan(make_input("windows", "x86_64"))
        second_by_name = {e.name: e for e in second}
        # Host tools are reused from the shared host cache...
        self.assertEqual(second_by_name["codegen"].status, "cached")
        self.assertEqual(second_by_name["libtempl"].status, "cached")
        # ...while target artifacts land in their own per-target trees.
        self.assertEqual(second_by_name["win32sock"].status, "created")
        self.assertIn("target-windows-x86_64", str(second_by_name["win32sock"].path))
        self.assertIn("target-windows-x86_64", str(second_by_name["guilib"].path))
        # The linux tree is untouched and distinct.
        self.assertTrue((self.cache.root / "target-linux-x86_64").is_dir())
        self.assertTrue((self.cache.root / "target-windows-x86_64").is_dir())
        self.assertEqual(
            sorted(p.name for p in (self.cache.root / "host-macos-arm64").iterdir()),
            ["codegen", "libtempl"],
        )

    def test_rerun_is_fully_cached(self):
        self.plan(make_input("linux", "x86_64"))
        again = self.plan(make_input("linux", "x86_64"))
        self.assertTrue(all(e.status == "cached" for e in again))


if __name__ == "__main__":
    unittest.main()
