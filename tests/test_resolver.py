import unittest

from buildset.conditions import Condition
from buildset.registry import Dependency, Package, Registry
from buildset.resolver import ConflictError, resolve
from buildset.version import Constraint, Version

CTX = {
    "host_os": "linux",
    "host_arch": "x86_64",
    "target_os": "darwin",
    "target_arch": "arm64",
    "stage": "build",
}


def pkg(name, version, kind="library", deps=()):
    return Package(name, Version.parse(version), kind, tuple(deps))


def dep(name, constraint="", when=None):
    return Dependency(name, Constraint.parse(constraint), Condition.from_json(when))


def roots(*names):
    return [(n, Constraint.any()) for n in names]


class MinimalSetTests(unittest.TestCase):
    def test_false_condition_edges_are_pruned(self):
        registry = Registry([
            pkg("app", "1.0.0", deps=[
                dep("used", ">=1.0.0", when={"os": ["darwin"]}),
                dep("skipped", ">=1.0.0", when={"os": ["windows"]}),
            ]),
            pkg("used", "1.0.0"),
            pkg("skipped", "1.0.0"),
        ])
        res = resolve(registry, roots("app"), CTX)
        names = {n.name for n in res.nodes}
        self.assertEqual(names, {"app", "used"})
        pruned = {(e.package, e.dependency) for e in res.pruned}
        self.assertEqual(pruned, {("app@1.0.0", "skipped")})

    def test_evidence_records_field_checks(self):
        registry = Registry([
            pkg("app", "1.0.0", deps=[
                dep("x", "", when={"os": ["windows"], "stage": ["build"]}),
            ]),
            pkg("x", "1.0.0"),
        ])
        res = resolve(registry, roots("app"), CTX)
        (edge,) = res.evidence
        self.assertFalse(edge.taken)
        checks = {c.field: c for c in edge.checks}
        self.assertFalse(checks["os"].passed)
        self.assertTrue(checks["stage"].passed)

    def test_stage_condition(self):
        registry = Registry([
            pkg("app", "1.0.0", deps=[dep("dbg", "", when={"stage": ["test"]})]),
            pkg("dbg", "1.0.0"),
        ])
        build = resolve(registry, roots("app"), CTX)
        self.assertNotIn("dbg", {n.name for n in build.nodes})
        test = resolve(registry, roots("app"), dict(CTX, stage="test"))
        self.assertIn("dbg", {n.name for n in test.nodes})

    def test_highest_satisfying_version_selected(self):
        registry = Registry([
            pkg("app", "1.0.0", deps=[dep("lib", ">=1.1.0 <2.0.0")]),
            pkg("lib", "1.0.0"),
            pkg("lib", "1.4.0"),
            pkg("lib", "2.0.0"),
        ])
        res = resolve(registry, roots("app"), CTX)
        lib = next(n for n in res.nodes if n.name == "lib")
        self.assertEqual(lib.version, "1.4.0")

    def test_build_order_is_dependency_first(self):
        registry = Registry([
            pkg("app", "1.0.0", deps=[dep("mid", "")]),
            pkg("mid", "1.0.0", deps=[dep("leaf", "")]),
            pkg("leaf", "1.0.0"),
        ])
        res = resolve(registry, roots("app"), CTX)
        self.assertEqual([n.name for n in res.nodes], ["leaf", "mid", "app"])


class ConflictTests(unittest.TestCase):
    def test_taken_edges_conflict(self):
        registry = Registry([
            pkg("a", "1.0.0", deps=[dep("lib", ">=2.0.0")]),
            pkg("b", "1.0.0", deps=[dep("lib", "<2.0.0")]),
            pkg("lib", "1.5.0"),
            pkg("lib", "2.1.0"),
        ])
        with self.assertRaises(ConflictError) as cm:
            resolve(registry, roots("a", "b"), CTX)
        self.assertEqual(cm.exception.name, "lib")

    def test_false_condition_edge_cannot_conflict(self):
        registry = Registry([
            pkg("a", "1.0.0", deps=[dep("lib", ">=2.0.0", when={"os": ["windows"]})]),
            pkg("b", "1.0.0", deps=[dep("lib", "<2.0.0")]),
            pkg("lib", "1.5.0"),
            pkg("lib", "2.1.0"),
        ])
        res = resolve(registry, roots("a", "b"), CTX)
        lib = next(n for n in res.nodes if n.name == "lib")
        self.assertEqual(lib.version, "1.5.0")

    def test_condition_flip_surfaces_conflict(self):
        registry = Registry([
            pkg("a", "1.0.0", deps=[dep("lib", ">=2.0.0", when={"os": ["windows"]})]),
            pkg("b", "1.0.0", deps=[dep("lib", "<2.0.0")]),
            pkg("lib", "1.5.0"),
            pkg("lib", "2.1.0"),
        ])
        win_ctx = dict(CTX, target_os="windows")
        with self.assertRaises(ConflictError):
            resolve(registry, roots("a", "b"), win_ctx)


class CrossCompileTests(unittest.TestCase):
    def make_registry(self):
        return Registry([
            pkg("app", "1.0.0", deps=[dep("gen", ""), dep("lib", "")]),
            pkg("gen", "3.1.0", kind="tool", deps=[dep("tpl", "")]),
            pkg("tpl", "1.4.0", kind="tool"),
            pkg("lib", "2.0.0"),
        ])

    def test_tools_partitioned_to_host_libraries_to_target(self):
        res = resolve(self.make_registry(), roots("app"), CTX)
        by_name = {n.name: n for n in res.nodes}
        self.assertEqual(by_name["gen"].partition, "host")
        self.assertEqual(by_name["gen"].cache_key, ("gen", "3.1.0", "linux", "x86_64"))
        self.assertEqual(by_name["tpl"].partition, "host")
        self.assertEqual(by_name["lib"].partition, "target")
        self.assertEqual(by_name["lib"].cache_key, ("lib", "2.0.0", "darwin", "arm64"))

    def test_two_targets_share_host_tool_keys(self):
        other = dict(CTX, target_os="windows", target_arch="x86_64")
        first = resolve(self.make_registry(), roots("app"), CTX)
        second = resolve(self.make_registry(), roots("app"), other)
        host_first = {n.cache_key for n in first.nodes if n.partition == "host"}
        host_second = {n.cache_key for n in second.nodes if n.partition == "host"}
        self.assertEqual(host_first, host_second)
        target_first = {n.cache_key for n in first.nodes if n.partition == "target"}
        target_second = {n.cache_key for n in second.nodes if n.partition == "target"}
        self.assertTrue(target_first.isdisjoint(target_second))


if __name__ == "__main__":
    unittest.main()
