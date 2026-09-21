import unittest

from buildsys.model import PackageIndex, RootRequirement, TargetContext
from buildsys.resolver import (
    ConflictError,
    CycleError,
    UnresolvedError,
    resolve,
)
from buildsys.version import Constraint, Version


def make_index(spec):
    """spec: {name: {version_string: [dep_dict, ...]}}"""
    data = {
        "packages": [
            {"name": name, "version": version, "deps": deps}
            for name, versions in spec.items()
            for version, deps in versions.items()
        ]
    }
    return PackageIndex.from_data(data)


LINUX = TargetContext(os="linux", arch="x86_64", stage="build")
WINDOWS = TargetContext(os="windows", arch="x86_64", stage="build")


def example_index():
    return make_index({
        "app": {"1.0.0": [
            {"name": "codegen", "constraint": ">=2.0.0 <3.0.0", "kind": "host"},
            {"name": "guilib", "constraint": ">=1.2.0"},
            {"name": "posixsock", "constraint": "==3.1.0", "when": {"os": ["linux"]}},
            {"name": "win32sock", "constraint": "==3.1.0", "when": {"os": ["windows"]}},
            {"name": "testharness", "constraint": ">=1.0.0", "when": {"stage": ["test"]}},
            {"name": "legacy", "constraint": "==0.0.1", "when": {"os": ["plan9"]}},
        ]},
        "codegen": {
            "2.0.0": [],
            "2.1.0": [{"name": "libtempl", "constraint": ">=1.0.0 <2.0.0"}],
        },
        "libtempl": {"1.4.2": []},
        "guilib": {"1.5.0": [
            {"name": "fontlib", "constraint": ">=0.9.0",
             "when": {"arch": ["x86_64", "arm64"]}},
            {"name": "fontstub", "constraint": "==0.1.0", "when": {"arch": ["riscv64"]}},
        ]},
        "fontlib": {"1.0.0": []},
        "fontstub": {"0.1.0": []},
        "posixsock": {"3.1.0": []},
        "win32sock": {"3.1.0": []},
        "testharness": {"1.2.0": []},
        # note: no "legacy" package at all
    })


def roots(*specs):
    return tuple(RootRequirement(name, Constraint.parse(c)) for name, c in specs)


def names(resolution, domain):
    return {name for (d, name) in resolution.selected if d == domain}


class TestConditions(unittest.TestCase):
    def setUp(self):
        self.index = example_index()

    def test_os_condition_selects_platform_dep(self):
        linux = resolve(self.index, LINUX, roots(("app", "==1.0.0")))
        self.assertIn("posixsock", names(linux, "target"))
        self.assertNotIn("win32sock", names(linux, "target"))
        windows = resolve(self.index, WINDOWS, roots(("app", "==1.0.0")))
        self.assertIn("win32sock", names(windows, "target"))
        self.assertNotIn("posixsock", names(windows, "target"))

    def test_stage_condition(self):
        build = resolve(self.index, LINUX, roots(("app", "==1.0.0")))
        self.assertNotIn("testharness", names(build, "target"))
        test_ctx = TargetContext(os="linux", arch="x86_64", stage="test")
        test = resolve(self.index, test_ctx, roots(("app", "==1.0.0")))
        self.assertIn("testharness", names(test, "target"))

    def test_arch_condition(self):
        x86 = resolve(self.index, LINUX, roots(("app", "==1.0.0")))
        self.assertIn("fontlib", names(x86, "target"))
        self.assertNotIn("fontstub", names(x86, "target"))
        riscv = TargetContext(os="linux", arch="riscv64", stage="build")
        rv = resolve(self.index, riscv, roots(("app", "==1.0.0")))
        self.assertIn("fontstub", names(rv, "target"))
        self.assertNotIn("fontlib", names(rv, "target"))

    def test_minimal_set_excludes_false_condition_subtrees(self):
        resolution = resolve(self.index, LINUX, roots(("app", "==1.0.0")))
        self.assertEqual(
            names(resolution, "target"),
            {"app", "guilib", "fontlib", "posixsock"},
        )
        self.assertEqual(names(resolution, "host"), {"codegen", "libtempl"})

    def test_evidence_records_false_edges_with_reasons(self):
        resolution = resolve(self.index, LINUX, roots(("app", "==1.0.0")))
        by_name = {e.name: e for e in resolution.edges}
        win_edge = by_name["win32sock"]
        self.assertFalse(win_edge.included)
        self.assertIn("os", win_edge.reason)
        os_dim = [d for d in win_edge.evaluation.dimensions if d.dimension == "os"][0]
        self.assertEqual(os_dim.required, ("windows",))
        self.assertEqual(os_dim.actual, "linux")
        self.assertFalse(os_dim.matched)
        self.assertTrue(by_name["posixsock"].included)


class TestFalseConditionNoConflict(unittest.TestCase):
    def test_false_condition_dependency_does_not_conflict(self):
        index = make_index({
            "app": {"1.0.0": [
                {"name": "x", "constraint": "==1.0.0", "when": {"os": ["windows"]}},
                {"name": "x", "constraint": "==2.0.0"},
            ]},
            "x": {"1.0.0": [], "2.0.0": []},
        })
        resolution = resolve(index, LINUX, roots(("app", "==1.0.0")))
        self.assertEqual(resolution.selected[("target", "x")], Version.parse("2.0.0"))

    def test_false_condition_unknown_package_is_not_looked_up(self):
        # "legacy" is absent from the index; its edge is false on linux.
        resolution = resolve(example_index(), LINUX, roots(("app", "==1.0.0")))
        self.assertNotIn("legacy", names(resolution, "target"))

    def test_true_condition_conflict_raises(self):
        index = make_index({
            "app": {"1.0.0": [
                {"name": "x", "constraint": "==1.0.0"},
                {"name": "y", "constraint": "==1.0.0"},
            ]},
            "y": {"1.0.0": [{"name": "x", "constraint": "==2.0.0"}]},
            "x": {"1.0.0": [], "2.0.0": []},
        })
        with self.assertRaises(ConflictError) as cm:
            resolve(index, LINUX, roots(("app", "==1.0.0")))
        message = str(cm.exception)
        self.assertIn("target/x", message)
        self.assertIn("app-1.0.0", message)
        self.assertIn("y-1.0.0", message)

    def test_true_condition_unknown_package_raises(self):
        index = make_index({
            "app": {"1.0.0": [{"name": "ghost", "constraint": "*"}]},
        })
        with self.assertRaises(UnresolvedError):
            resolve(index, LINUX, roots(("app", "==1.0.0")))


class TestDomainsAndSelection(unittest.TestCase):
    def test_host_edge_makes_host_subtree_target_default(self):
        resolution = resolve(example_index(), LINUX, roots(("app", "==1.0.0")))
        # codegen is a host tool; its own dep libtempl inherits the host domain.
        self.assertEqual(names(resolution, "host"), {"codegen", "libtempl"})
        # guilib is compiled for the target.
        self.assertIn(("target", "guilib"), resolution.selected)

    def test_two_targets_select_identical_host_tools(self):
        index = example_index()
        linux = resolve(index, LINUX, roots(("app", "==1.0.0")))
        windows = resolve(index, WINDOWS, roots(("app", "==1.0.0")))
        host_of = lambda r: {n: v for (d, n), v in r.selected.items() if d == "host"}
        self.assertEqual(host_of(linux), host_of(windows))

    def test_highest_matching_version_and_prerelease(self):
        index = make_index({
            "app": {"1.0.0": [{"name": "x", "constraint": ">=2.0.0-rc.1 <2.0.0"}]},
            "x": {"2.0.0-rc.1": [], "2.0.0-rc.2": [], "1.9.0": []},
        })
        resolution = resolve(index, LINUX, roots(("app", "==1.0.0")))
        self.assertEqual(resolution.selected[("target", "x")], Version.parse("2.0.0-rc.2"))

    def test_build_order_dependencies_first(self):
        resolution = resolve(example_index(), LINUX, roots(("app", "==1.0.0")))
        order = resolution.build_order
        self.assertLess(order.index(("target", "guilib")), order.index(("target", "app")))
        self.assertLess(order.index(("host", "libtempl")), order.index(("host", "codegen")))

    def test_cycle_detected(self):
        index = make_index({
            "a": {"1.0.0": [{"name": "b", "constraint": "*"}]},
            "b": {"1.0.0": [{"name": "a", "constraint": "*"}]},
        })
        with self.assertRaises(CycleError):
            resolve(index, LINUX, roots(("a", "*")))


if __name__ == "__main__":
    unittest.main()
