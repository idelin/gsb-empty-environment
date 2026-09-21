import json
import tempfile
import unittest
from pathlib import Path

from build_state.registry import (
    PackageRegistry,
    RegistryError,
    UnknownPackageError,
    UnknownVersionError,
)
from build_state.versions import parse_version


def _write(registry: dict) -> Path:
    tmp = tempfile.NamedTemporaryFile("w", suffix=".json", delete=False)
    json.dump(registry, tmp)
    tmp.close()
    return Path(tmp.name)


class RegistryTests(unittest.TestCase):
    def setUp(self):
        self.path = _write({
            "packages": {
                "libcore": ["1.0.0", "1.1.0", "2.0.0-rc.1"],
                "libnet": ["0.9.1-beta.2", "1.0.0"],
            }
        })
        self.registry = PackageRegistry.from_file(self.path)

    def tearDown(self):
        self.path.unlink(missing_ok=True)

    def test_exact_pin(self):
        self.assertEqual(
            self.registry.resolve("libcore", "1.1.0"),
            parse_version("1.1.0"),
        )

    def test_latest_stable_skips_prerelease(self):
        self.assertEqual(
            self.registry.resolve("libcore", "latest"),
            parse_version("1.1.0"),
        )

    def test_latest_includes_prerelease_when_allowed(self):
        self.assertEqual(
            self.registry.resolve("libcore", "latest", allow_prerelease=True),
            parse_version("2.0.0-rc.1"),
        )

    def test_prerelease_pin_requires_flag(self):
        with self.assertRaises(RegistryError):
            self.registry.resolve("libcore", "2.0.0-rc.1")
        self.assertEqual(
            self.registry.resolve(
                "libcore", "2.0.0-rc.1", allow_prerelease=True
            ),
            parse_version("2.0.0-rc.1"),
        )

    def test_unknown_package_and_version(self):
        with self.assertRaises(UnknownPackageError):
            self.registry.resolve("ghost", "1.0.0")
        with self.assertRaises(UnknownVersionError):
            self.registry.resolve("libcore", "9.9.9")

    def test_latest_stable_missing(self):
        path = _write({"packages": "only-pre"})  # invalid shape
        try:
            with self.assertRaises(RegistryError):
                PackageRegistry.from_file(path)
        finally:
            path.unlink(missing_ok=True)

    def test_only_prerelease_latest_stable_fails(self):
        path = _write({"packages": {"alpha": ["0.1.0-rc.1"]}})
        try:
            registry = PackageRegistry.from_file(path)
            with self.assertRaises(UnknownVersionError):
                registry.resolve("alpha", "latest")
        finally:
            path.unlink(missing_ok=True)


if __name__ == "__main__":
    unittest.main()
