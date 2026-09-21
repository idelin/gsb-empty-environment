from __future__ import annotations

import json
import pathlib
import sys
import tempfile
import unittest

sys.path.insert(0, str(pathlib.Path(__file__).resolve().parent.parent))

from buildcache import ManifestError, Version
from buildcache.manifest import parse_manifest


class VersionTest(unittest.TestCase):
    def test_core_release(self):
        v = Version.parse("1.2.3")
        self.assertEqual((v.major, v.minor, v.patch), (1, 2, 3))
        self.assertEqual(str(v), "1.2.3")
        self.assertFalse(v.is_prerelease)

    def test_prerelease_flag(self):
        v = Version.parse("2.4.1-rc.1")
        self.assertTrue(v.is_prerelease)
        self.assertEqual(v.prerelease, ("rc", "1"))
        self.assertEqual(str(v), "2.4.1-rc.1")

    def test_prerelease_lower_than_release(self):
        self.assertLess(Version.parse("1.0.0-alpha"), Version.parse("1.0.0"))

    def test_prerelease_ordering(self):
        ordered = [
            "1.0.0-alpha",
            "1.0.0-alpha.1",
            "1.0.0-alpha.beta",
            "1.0.0-beta",
            "1.0.0-beta.2",
            "1.0.0-beta.11",
            "1.0.0-rc.1",
            "1.0.0",
        ]
        parsed = [Version.parse(v) for v in ordered]
        self.assertEqual(parsed, sorted(parsed))

    def test_numeric_identifiers_compare_numerically(self):
        self.assertLess(Version.parse("1.0.0-beta.2"), Version.parse("1.0.0-beta.11"))

    def test_invalid_versions(self):
        for bad in ("1.2", "1.2.3.4", "v1.2.3", "01.2.3", "1.2.3-", "1.2.3-foo..bar"):
            with self.subTest(bad=bad):
                with self.assertRaises(ValueError):
                    Version.parse(bad)


def _minimal_manifest(**node_overrides):
    node = {
        "key": "//a:b",
        "package": "pkg",
        "outputs": [{"path": "out.txt", "template": "hello {VERSION}\n"}],
    }
    node.update(node_overrides)
    return {
        "packages": [{"name": "pkg", "version": "1.2.3-rc.1"}],
        "nodes": [node],
    }


class ManifestTest(unittest.TestCase):
    def test_parses_packages_and_nodes(self):
        manifest = parse_manifest(_minimal_manifest())
        pkg = manifest.packages["pkg"]
        self.assertEqual(str(pkg.version), "1.2.3-rc.1")
        node = manifest.nodes["//a:b"]
        self.assertEqual(node.outputs[0].template, "hello {VERSION}\n")

    def test_version_object_form(self):
        data = _minimal_manifest()
        data["packages"][0]["version"] = {"core": "9.9.9", "prerelease": "beta.3"}
        manifest = parse_manifest(data)
        self.assertEqual(str(manifest.packages["pkg"].version), "9.9.9-beta.3")

    def test_unknown_package_rejected(self):
        with self.assertRaises(ManifestError):
            parse_manifest(_minimal_manifest(package="nope"))

    def test_empty_outputs_rejected(self):
        with self.assertRaises(ManifestError):
            parse_manifest(_minimal_manifest(outputs=[]))

    def test_unsafe_path_rejected(self):
        for bad_path in ("../escape.txt", "/abs.txt", "a/../../b.txt"):
            with self.subTest(bad_path=bad_path):
                with self.assertRaises(ManifestError):
                    parse_manifest(
                        _minimal_manifest(outputs=[{"path": bad_path, "template": "x"}])
                    )

    def test_duplicate_output_path_rejected(self):
        with self.assertRaises(ManifestError):
            parse_manifest(
                _minimal_manifest(
                    outputs=[
                        {"path": "o.txt", "template": "a"},
                        {"path": "o.txt", "template": "b"},
                    ]
                )
            )

    def test_bad_sha256_rejected(self):
        with self.assertRaises(ManifestError):
            parse_manifest(
                _minimal_manifest(outputs=[{"path": "o", "template": "x", "sha256": "zzz"}])
            )

    def test_loads_local_json_file(self):
        path = pathlib.Path(__file__).resolve().parent.parent / "examples" / "manifest.json"
        from buildcache import load_manifest

        manifest = load_manifest(path)
        self.assertIn("//core:release", manifest.nodes)
        self.assertTrue(manifest.packages["core-lib"].is_prerelease)

    def test_missing_file_raises(self):
        from buildcache import load_manifest

        with tempfile.TemporaryDirectory() as tmp:
            with self.assertRaises(ManifestError):
                load_manifest(pathlib.Path(tmp) / "nope.json")

    def test_invalid_json_raises(self):
        from buildcache import load_manifest

        with tempfile.TemporaryDirectory() as tmp:
            path = pathlib.Path(tmp) / "bad.json"
            path.write_text("{not json", encoding="utf-8")
            with self.assertRaises(ManifestError):
                load_manifest(path)


if __name__ == "__main__":
    unittest.main(verbosity=2)
