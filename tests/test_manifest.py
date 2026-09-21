import json
import shutil
import tempfile
import unittest
from pathlib import Path

from buildcache.manifest import PackageError, PackageRegistry


MANIFEST = {
    "packages": [
        {"name": "widget", "version": "1.0.0-alpha", "inputs": {"src": ["w.c"]}},
        {"name": "widget", "version": "1.0.0", "inputs": {"src": ["w.c", "w.h"]}},
        {"name": "widget", "version": "0.9.5", "inputs": {"src": ["old.c"]}},
        {"name": "gadget", "version": "2.3.4-rc.2", "inputs": {"src": ["g.c"]}},
    ]
}


class ManifestTests(unittest.TestCase):
    def setUp(self):
        self.tmp = Path(tempfile.mkdtemp())
        self.path = self.tmp / "packages.json"
        self.path.write_text(json.dumps(MANIFEST), "utf-8")
        self.registry = PackageRegistry.from_file(self.path)

    def tearDown(self):
        shutil.rmtree(self.tmp, ignore_errors=True)

    def test_resolve_highest_version(self):
        pkg = self.registry.resolve("widget")
        self.assertEqual(str(pkg.version), "1.0.0")  # release beats prerelease
        self.assertEqual(pkg.inputs["src"], ["w.c", "w.h"])

    def test_resolve_specific_version(self):
        pkg = self.registry.resolve("widget", "0.9.5")
        self.assertEqual(pkg.inputs["src"], ["old.c"])

    def test_prerelease_resolves(self):
        pkg = self.registry.resolve("gadget")
        self.assertTrue(pkg.version.is_prerelease)
        self.assertEqual(str(pkg.version), "2.3.4-rc.2")

    def test_unknown_package(self):
        with self.assertRaises(PackageError):
            self.registry.resolve("not-on-disk")

    def test_unknown_version(self):
        with self.assertRaises(PackageError):
            self.registry.resolve("widget", "9.9.9")


if __name__ == "__main__":
    unittest.main()
