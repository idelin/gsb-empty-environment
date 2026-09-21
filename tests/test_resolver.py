import unittest

from feature_resolver import (
    ModelError,
    ResolutionError,
    Resolver,
    Version,
    VersionReq,
    load_document,
)
from feature_resolver.expr import evaluate, parse


def pkg(name, version, features=None, default_features=()):
    return {
        "name": name,
        "version": version,
        "default_features": list(default_features),
        "features": features or {},
    }


def resolve(packages, roots):
    pkgs, rts = load_document({"packages": packages, "build": {"roots": roots}})
    return Resolver(pkgs).resolve(rts)


def features_of(res, name):
    for inst in res.instances.values():
        if inst.pkg.name == name:
            return inst.active
    raise KeyError(name)


class VersionTests(unittest.TestCase):
    def test_parse_plain_and_prerelease(self):
        v = Version.parse("1.2.3")
        self.assertEqual((v.major, v.minor, v.patch, v.prerelease), (1, 2, 3, ()))
        v = Version.parse("1.2.3-alpha.1")
        self.assertEqual(v.prerelease, ("alpha", "1"))
        self.assertEqual(str(v), "1.2.3-alpha.1")

    def test_invalid_versions_rejected(self):
        for bad in ["1.2", "1.2.3.4", "01.2.3", "1.2.x", ""]:
            with self.assertRaises(ValueError, msg=bad):
                Version.parse(bad)

    def test_ordering_with_prerelease(self):
        order = [
            "0.9.9",
            "1.0.0-alpha",
            "1.0.0-alpha.1",
            "1.0.0-alpha.beta",
            "1.0.0-beta",
            "1.0.0-beta.2",
            "1.0.0-beta.11",
            "1.0.0-rc.1",
            "1.0.0",
            "1.0.1",
        ]
        versions = [Version.parse(s) for s in order]
        for a, b in zip(versions, versions[1:]):
            self.assertLess(a, b)
            self.assertGreater(b, a)
            self.assertNotEqual(a, b)

    def test_requirements(self):
        self.assertTrue(VersionReq.parse("1.2.3").matches(Version.parse("1.2.3")))
        self.assertFalse(VersionReq.parse("1.2.3").matches(Version.parse("1.2.4")))
        caret = VersionReq.parse("^1.2.3")
        self.assertTrue(caret.matches(Version.parse("1.9.0")))
        self.assertFalse(caret.matches(Version.parse("2.0.0")))
        self.assertFalse(caret.matches(Version.parse("1.2.2")))
        caret0 = VersionReq.parse("^0.2.3")
        self.assertTrue(caret0.matches(Version.parse("0.2.9")))
        self.assertFalse(caret0.matches(Version.parse("0.3.0")))
        tilde = VersionReq.parse("~1.2.3")
        self.assertTrue(tilde.matches(Version.parse("1.2.9")))
        self.assertFalse(tilde.matches(Version.parse("1.3.0")))
        self.assertTrue(VersionReq.parse(">=1.0.0").matches(Version.parse("2.0.0")))


class ExprTests(unittest.TestCase):
    def test_precedence_and_not(self):
        ast = parse("a || b && !c")
        self.assertTrue(evaluate(ast, {"a"}))
        self.assertTrue(evaluate(ast, {"b"}))
        self.assertFalse(evaluate(ast, {"b", "c"}))
        self.assertFalse(evaluate(ast, set()))

    def test_parentheses(self):
        ast = parse("(a || b) && c")
        self.assertFalse(evaluate(ast, {"a"}))
        self.assertTrue(evaluate(ast, {"b", "c"}))


class ClosureTests(unittest.TestCase):
    def test_transitive_closure_and_union(self):
        packages = [
            pkg("a", "1.0.0", {
                "f1": {"enables": ["f2"]},
                "f2": {"enables": ["f3"]},
                "f3": {},
                "g1": {"enables": ["g2"]},
                "g2": {},
            }),
        ]
        roots = [
            {"package": "a", "version": "1.0.0", "enable": ["f1"], "default_features": False},
            {"package": "a", "version": "1.0.0", "enable": ["g1"], "default_features": False},
        ]
        res = resolve(packages, roots)
        self.assertTrue(res.ok)
        self.assertEqual(features_of(res, "a"), {"f1", "f2", "f3", "g1", "g2"})
        self.assertEqual(len(res.instances), 1)  # unified into one instance

    def test_dependency_closure_across_packages(self):
        packages = [
            pkg("a", "1.0.0", {
                "x": {"deps": [{"package": "b", "version": "^1.0.0", "features": ["y"]}]},
            }),
            pkg("b", "1.0.0", {"y": {}, "z": {}}, default_features=["z"]),
            pkg("b", "1.5.0", {"y": {}, "z": {}}, default_features=["z"]),
        ]
        res = resolve(packages, [
            {"package": "a", "version": "1.0.0", "enable": ["x"], "default_features": False},
        ])
        self.assertTrue(res.ok)
        self.assertEqual(features_of(res, "b"), {"y", "z"})
        b_inst = [i for i in res.instances.values() if i.pkg.name == "b"][0]
        self.assertEqual(str(b_inst.pkg.version), "1.5.0")  # highest matching

    def test_conditional_dependency_by_expression(self):
        packages = [
            pkg("a", "1.0.0", {
                "std": {},
                "minimal": {},
                "net": {
                    "conditional": [{
                        "when": "std && !minimal",
                        "enables": ["async"],
                        "deps": [{"package": "io", "version": "1.0.0"}],
                    }],
                },
                "async": {},
            }),
            pkg("io", "1.0.0", {"core": {}}, default_features=["core"]),
        ]
        res = resolve(packages, [
            {"package": "a", "version": "1.0.0",
             "enable": ["net", "std"], "disable": ["minimal"],
             "default_features": False},
        ])
        self.assertEqual(features_of(res, "a"), {"net", "std", "async"})
        self.assertIn("io@1.0.0", res.instances)

        res2 = resolve(packages, [
            {"package": "a", "version": "1.0.0",
             "enable": ["net", "minimal"], "default_features": False},
        ])
        self.assertEqual(features_of(res2, "a"), {"net", "minimal"})
        self.assertNotIn("io@1.0.0", res2.instances)


class DefaultAndDisableTests(unittest.TestCase):
    def test_default_features_applied_unless_disabled(self):
        packages = [pkg("a", "1.0.0", {"std": {}, "alloc": {}}, default_features=["std"])]
        res = resolve(packages, [{"package": "a", "version": "1.0.0"}])
        self.assertEqual(features_of(res, "a"), {"std"})

        res = resolve(packages, [
            {"package": "a", "version": "1.0.0", "default_features": False},
        ])
        self.assertEqual(features_of(res, "a"), set())

    def test_explicit_disable_of_default_feature(self):
        packages = [pkg("a", "1.0.0", {"std": {}, "alloc": {}}, default_features=["std", "alloc"])]
        res = resolve(packages, [
            {"package": "a", "version": "1.0.0", "disable": ["alloc"]},
        ])
        self.assertTrue(res.ok)
        self.assertEqual(features_of(res, "a"), {"std"})

    def test_dep_level_default_features_off(self):
        packages = [
            pkg("a", "1.0.0", {
                "x": {"deps": [{"package": "b", "version": "1.0.0",
                                "default_features": False}]},
            }),
            pkg("b", "1.0.0", {"std": {}}, default_features=["std"]),
        ]
        res = resolve(packages, [
            {"package": "a", "version": "1.0.0", "enable": ["x"], "default_features": False},
        ])
        self.assertEqual(features_of(res, "b"), set())


class ConflictTests(unittest.TestCase):
    def test_disable_vs_transitive_requirement_reports_source(self):
        packages = [pkg("a", "1.0.0", {
            "std": {"enables": ["alloc"]},
            "alloc": {},
        }, default_features=["std"])]
        res = resolve(packages, [
            {"package": "a", "version": "1.0.0", "disable": ["alloc"]},
        ])
        self.assertFalse(res.ok)
        self.assertEqual(len(res.conflicts), 1)
        msg = res.conflicts[0]
        self.assertIn("a@1.0.0:alloc", msg)
        self.assertIn("root request", msg)                 # disable source
        self.assertIn("a@1.0.0:std", msg)                  # requiring feature
        self.assertIn("default features of a@1.0.0", msg)  # activation chain

    def test_dep_disable_vs_other_dep_requirement(self):
        packages = [
            pkg("top", "1.0.0", {
                "via_left": {"deps": [{"package": "shared", "version": "1.0.0",
                                       "features": ["crypto"]}]},
                "via_right": {"deps": [{"package": "shared", "version": "1.0.0",
                                        "disable": ["crypto"]}]},
            }),
            pkg("shared", "1.0.0", {"crypto": {}}),
        ]
        res = resolve(packages, [
            {"package": "top", "version": "1.0.0",
             "enable": ["via_left", "via_right"], "default_features": False},
        ])
        self.assertFalse(res.ok)
        msg = res.conflicts[0]
        self.assertIn("shared@1.0.0:crypto", msg)
        self.assertIn("top@1.0.0:via_right", msg)  # who disabled
        self.assertIn("top@1.0.0:via_left", msg)   # who required

    def test_enable_and_disable_same_feature_conflicts(self):
        packages = [pkg("a", "1.0.0", {"x": {}})]
        res = resolve(packages, [
            {"package": "a", "version": "1.0.0", "enable": ["x"], "disable": ["x"]},
        ])
        self.assertFalse(res.ok)
        self.assertIn("a@1.0.0:x", res.conflicts[0])


class CycleTests(unittest.TestCase):
    def test_unseeded_mutual_cycle_stays_inactive(self):
        packages = [pkg("a", "1.0.0", {
            "p": {"enables": ["q"]},
            "q": {"enables": ["p"]},
            "seed": {},
        })]
        res = resolve(packages, [
            {"package": "a", "version": "1.0.0", "enable": ["seed"],
             "default_features": False},
        ])
        self.assertTrue(res.ok)
        self.assertEqual(features_of(res, "a"), {"seed"})
        self.assertEqual(res.cycles, [])

    def test_seeded_cycle_activates_and_is_reported(self):
        packages = [pkg("a", "1.0.0", {
            "p": {"enables": ["q"]},
            "q": {"enables": ["p"]},
        })]
        res = resolve(packages, [
            {"package": "a", "version": "1.0.0", "enable": ["p"],
             "default_features": False},
        ])
        self.assertTrue(res.ok)
        self.assertEqual(features_of(res, "a"), {"p", "q"})
        self.assertEqual(res.cycles, [["a@1.0.0:p", "a@1.0.0:q"]])

    def test_cross_package_cycle_detected(self):
        packages = [
            pkg("a", "1.0.0", {
                "x": {"deps": [{"package": "b", "version": "1.0.0", "features": ["y"]}]},
            }),
            pkg("b", "1.0.0", {
                "y": {"deps": [{"package": "a", "version": "1.0.0", "features": ["x"]}]},
            }),
        ]
        res = resolve(packages, [
            {"package": "a", "version": "1.0.0", "enable": ["x"],
             "default_features": False},
        ])
        self.assertTrue(res.ok)
        self.assertEqual(features_of(res, "a"), {"x"})
        self.assertEqual(features_of(res, "b"), {"y"})
        self.assertEqual(res.cycles, [["a@1.0.0:x", "b@1.0.0:y"]])


class IdempotenceTests(unittest.TestCase):
    def test_repeated_requests_are_idempotent(self):
        packages = [pkg("a", "1.0.0", {
            "x": {"enables": ["y"]},
            "y": {},
        }, default_features=["x"])]
        roots = [
            {"package": "a", "version": "1.0.0", "enable": ["x", "x", "y"]},
            {"package": "a", "version": "1.0.0", "enable": ["y", "x"]},
        ]
        first = resolve(packages, roots).to_dict()
        second = resolve(packages, roots).to_dict()
        single = resolve(packages, roots[:1]).to_dict()
        self.assertEqual(first, second)
        self.assertEqual(first["packages"], single["packages"])
        self.assertEqual(features_of(resolve(packages, roots), "a"), {"x", "y"})


class InputValidationTests(unittest.TestCase):
    def test_unknown_package_and_version(self):
        with self.assertRaises(ResolutionError):
            resolve([], [{"package": "nope", "version": "1.0.0"}])
        packages = [pkg("a", "1.0.0", {"x": {}})]
        with self.assertRaises(ResolutionError):
            resolve(packages, [{"package": "a", "version": "9.9.9"}])

    def test_unknown_feature_rejected(self):
        packages = [pkg("a", "1.0.0", {"x": {}})]
        with self.assertRaises(ResolutionError):
            resolve(packages, [
                {"package": "a", "version": "1.0.0", "enable": ["ghost"]},
            ])

    def test_model_validation(self):
        with self.assertRaises(ModelError):
            load_document({"packages": [pkg("a", "1.0.0", {"x": {"enables": ["ghost"]}})]})
        with self.assertRaises(ModelError):
            load_document({"packages": [pkg("a", "1.0.0", {"x": {}}, default_features=["ghost"])]})
        with self.assertRaises(ModelError):
            load_document({"packages": [pkg("a", "1.0.0"), pkg("a", "1.0.0")]})


if __name__ == "__main__":
    unittest.main()
