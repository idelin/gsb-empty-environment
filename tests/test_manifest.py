import json
import unittest
from pathlib import Path

from buildcache.manifest import ManifestError, load_manifest, parse_manifest
from buildcache.version import Version

DOC = {
    "packages": [
        {"name": "core", "version": "2.4.1"},
        {"name": "beta-lib", "version": "3.0.0-rc.2", "prerelease": "rc.2"},
    ],
    "nodes": [
        {
            "name": "compile",
            "package": "core",
            "version": "2.4.1",
            "inputs": {"flags": ["-O2"], "deps": {"beta-lib": "3.0.0-rc.2"}},
            "outputs": ["core.o", "core.map"],
        },
        {
            "name": "beta-build",
            "package": "beta-lib",
            "inputs": {},
            "outputs": ["libbeta.a"],
        },
    ],
}


class ManifestTests(unittest.TestCase):
    def test_parse(self):
        manifest = parse_manifest(DOC)
        self.assertEqual(set(manifest.packages), {"core", "beta-lib"})
        self.assertEqual(manifest.packages["core"].version, Version(2, 4, 1))
        self.assertTrue(manifest.packages["beta-lib"].version.is_prerelease)
        node = manifest.nodes["compile"]
        self.assertEqual(node.outputs, ("core.o", "core.map"))
        self.assertEqual(node.cache_key, manifest.nodes["compile"].cache_key)

    def test_prerelease_bool(self):
        doc = {
            "packages": [{"name": "p", "version": "1.0.0-alpha", "prerelease": True}],
            "nodes": [{"name": "n", "package": "p", "outputs": ["a"]}],
        }
        self.assertTrue(parse_manifest(doc).packages["p"].version.is_prerelease)

    def test_prerelease_mismatch(self):
        doc = {
            "packages": [{"name": "p", "version": "1.0.0-alpha", "prerelease": "beta"}],
            "nodes": [],
        }
        with self.assertRaises(ManifestError):
            parse_manifest(doc)

    def test_unknown_package(self):
        doc = {
            "packages": [{"name": "p", "version": "1.0.0"}],
            "nodes": [{"name": "n", "package": "ghost", "outputs": []}],
        }
        with self.assertRaises(ManifestError):
            parse_manifest(doc)

    def test_version_mismatch(self):
        doc = {
            "packages": [{"name": "p", "version": "1.0.0"}],
            "nodes": [{"name": "n", "package": "p", "version": "1.0.1", "outputs": []}],
        }
        with self.assertRaises(ManifestError):
            parse_manifest(doc)

    def test_duplicate_outputs_rejected(self):
        doc = {
            "packages": [{"name": "p", "version": "1.0.0"}],
            "nodes": [{"name": "n", "package": "p", "outputs": ["a", "a"]}],
        }
        with self.assertRaises(ManifestError):
            parse_manifest(doc)

    def test_load_from_local_file_only(self):
        path = Path(self._testMethodName + ".json")
        try:
            path.write_text(json.dumps(DOC), encoding="utf-8")
            manifest = load_manifest(path)
            self.assertEqual(manifest.source, path)
        finally:
            path.unlink(missing_ok=True)

    def test_bad_json(self):
        path = Path(self._testMethodName + ".json")
        try:
            path.write_text("{not json", encoding="utf-8")
            with self.assertRaises(ManifestError):
                load_manifest(path)
        finally:
            path.unlink(missing_ok=True)


if __name__ == "__main__":
    unittest.main()
