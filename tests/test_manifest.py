import json
import tempfile
import unittest
from pathlib import Path

from buildcache.manifest import expected_sha256, load_packages, load_plan


class ManifestTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)

    def write_json(self, name, data):
        path = self.root / name
        path.write_text(json.dumps(data), encoding="utf-8")
        return path

    def packages_file(self, packages):
        return self.write_json("packages.json", {"packages": packages})

    def test_load_packages_with_prerelease(self):
        path = self.packages_file([
            {"name": "core", "version": "1.2.3"},
            {"name": "plugin", "version": "0.9.0-rc.1"},
        ])
        packages = load_packages(path)
        self.assertEqual(str(packages["core"].version), "1.2.3")
        self.assertEqual(str(packages["plugin"].version), "0.9.0-rc.1")

    def test_invalid_package_version_rejected(self):
        path = self.packages_file([{"name": "core", "version": "1.2"}])
        with self.assertRaises(ValueError):
            load_packages(path)

    def test_duplicate_package_rejected(self):
        path = self.packages_file([
            {"name": "core", "version": "1.2.3"},
            {"name": "core", "version": "1.2.4"},
        ])
        with self.assertRaises(ValueError):
            load_packages(path)

    def plan_file(self, nodes):
        return self.write_json("plan.json", {"nodes": nodes})

    def node_spec(self, node_id="core-lib", package="core", inputs=("src/core.c",),
                  outputs=("lib/core.o",)):
        return {
            "id": node_id,
            "package": package,
            "inputs": list(inputs),
            "outputs": [
                {"path": path, "sha256": expected_sha256(node_id, tuple(inputs), path)}
                for path in outputs
            ],
        }

    def test_load_plan_and_stable_key(self):
        packages = load_packages(self.packages_file([{"name": "core", "version": "1.2.3"}]))
        plan = self.plan_file([self.node_spec()])
        first, = load_plan(plan, packages)
        again, = load_plan(plan, packages)
        self.assertEqual(first.key, again.key)
        self.assertEqual(len(first.key), 64)

    def test_unknown_package_rejected(self):
        packages = load_packages(self.packages_file([{"name": "core", "version": "1.2.3"}]))
        plan = self.plan_file([self.node_spec(package="ghost")])
        with self.assertRaises(ValueError):
            load_plan(plan, packages)

    def test_bad_output_digest_rejected(self):
        packages = load_packages(self.packages_file([{"name": "core", "version": "1.2.3"}]))
        spec = self.node_spec()
        spec["outputs"][0]["sha256"] = "not-a-digest"
        with self.assertRaises(ValueError):
            load_plan(self.plan_file([spec]), packages)

    def test_node_without_outputs_rejected(self):
        packages = load_packages(self.packages_file([{"name": "core", "version": "1.2.3"}]))
        spec = self.node_spec()
        spec["outputs"] = []
        with self.assertRaises(ValueError):
            load_plan(self.plan_file([spec]), packages)

    def test_key_changes_with_inputs(self):
        packages = load_packages(self.packages_file([{"name": "core", "version": "1.2.3"}]))
        one, = load_plan(self.plan_file([self.node_spec(inputs=("a.c",))]), packages)
        two, = load_plan(self.plan_file([self.node_spec(inputs=("b.c",))]), packages)
        self.assertNotEqual(one.key, two.key)


if __name__ == "__main__":
    unittest.main()
