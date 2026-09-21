import unittest

from resolve import (
    BuildInput,
    Catalog,
    Constraint,
    Package,
    ResolutionError,
    Version,
    candidate_chain,
    resolve,
)


def pkg(name, version, provides, conflicts=(), alternatives=(),
        requires=(), os=(), arch=(), priority=0):
    from resolve import parse_package
    return parse_package({
        "name": name,
        "version": version,
        "provides": provides,
        "conflicts": list(conflicts),
        "alternatives": list(alternatives),
        "requires": list(requires),
        "os": list(os),
        "arch": list(arch),
        "priority": priority,
    })


def build(requires, os=(), arch=(), allow_prereleases=False):
    from resolve import Target
    from resolve import parse_requirements
    reqs = parse_requirements(requires)
    for r in reqs:
        object.__setattr__(r, "root", True)
    return BuildInput(reqs, Target(tuple(os), tuple(arch)), allow_prereleases)


def catalog(*packages):
    return Catalog(list(packages))


def capmap(result):
    return {cap: f"{m['package']}@{m['version']}" for cap, m in result["mapping"].items()}


class TestVersion(unittest.TestCase):
    def test_order_and_prerelease(self):
        versions = [
            "1.0.0-alpha", "1.0.0-alpha.1", "1.0.0-alpha.beta",
            "1.0.0-beta", "1.0.0-rc.1", "1.0.0",
        ]
        parsed = [Version(v) for v in versions]
        self.assertEqual(parsed, sorted(parsed))
        self.assertLess(Version("1.0.0-alpha"), Version("1.0.0"))

    def test_invalid(self):
        for bad in ["1.2", "01.2.3", "1.2.3.4", "v1.2.3"]:
            with self.assertRaises(ResolutionError):
                Version(bad)


class TestConstraint(unittest.TestCase):
    def test_basic_operators(self):
        self.assertTrue(Constraint.parse(">=1.2.0").satisfies(Version("2.0.0")))
        self.assertFalse(Constraint.parse(">1.2.0").satisfies(Version("1.2.0")))
        self.assertTrue(Constraint.parse("!=1.2.0").satisfies(Version("1.2.1")))

    def test_caret(self):
        self.assertTrue(Constraint.parse("^1.2.0").satisfies(Version("1.9.9")))
        self.assertFalse(Constraint.parse("^1.2.0").satisfies(Version("2.0.0")))
        self.assertTrue(Constraint.parse("^0.2.0").satisfies(Version("0.2.9")))
        self.assertFalse(Constraint.parse("^0.2.0").satisfies(Version("0.3.0")))
        self.assertFalse(Constraint.parse("^0.0.3").satisfies(Version("0.0.4")))

    def test_tilde(self):
        self.assertTrue(Constraint.parse("~1.2.0").satisfies(Version("1.2.9")))
        self.assertFalse(Constraint.parse("~1.2.0").satisfies(Version("1.3.0")))

    def test_prerelease_allowed_by_constraint(self):
        self.assertTrue(Constraint.parse(">=1.0.0").satisfies(Version("1.5.0-rc.1")))


class TestResolution(unittest.TestCase):
    def test_choose_higher_version(self):
        cat = catalog(
            pkg("a", "1.0.0", ["X"]),
            pkg("b", "2.0.0", ["X"]),
        )
        self.assertEqual(capmap(resolve(build(["X"]), cat))["X"], "b@2.0.0")

    def test_provide_priority_beats_version(self):
        cat = catalog(
            pkg("a", "1.0.0", [{"capability": "X", "version": "1.0.0", "priority": 10}]),
            pkg("b", "2.0.0", ["X"]),
        )
        self.assertEqual(capmap(resolve(build(["X"]), cat))["X"], "a@1.0.0")

    def test_name_breaks_exact_tie(self):
        cat = catalog(pkg("alpha", "1.0.0", ["X"]), pkg("beta", "1.0.0", ["X"]))
        self.assertEqual(capmap(resolve(build(["X"]), cat))["X"], "alpha@1.0.0")

    def test_version_constraint_filters(self):
        cat = catalog(pkg("a", "1.0.0", ["X"]), pkg("b", "2.0.0", ["X"]))
        self.assertEqual(
            capmap(resolve(build([{"capability": "X", "version": "<2.0.0"}]), cat))["X"],
            "a@1.0.0",
        )

    def test_conflict_falls_back_to_alternative(self):
        cat = catalog(
            pkg("impl-a", "1.2.0", [{"capability": "X", "version": "1.2.0", "priority": 10}],
                conflicts=["core"], alternatives=["impl-b"]),
            pkg("impl-b", "2.0.0", ["X"], alternatives=["impl-a"]),
            pkg("core", "9.0.0", ["Y"], conflicts=["impl-a"]),
        )
        result = resolve(build(["X", "Y"]), cat)
        self.assertEqual(capmap(result)["X"], "impl-b@2.0.0")
        self.assertEqual(capmap(result)["Y"], "core@9.0.0")
        actions = [d["action"] for d in result["decisions"] if d["capability"] == "X"]
        self.assertIn("backtrack", actions)
        self.assertEqual(actions[-1], "select")
        self.assertEqual(
            next(d for d in result["decisions"]
                 if d["action"] == "backtrack" and d["capability"] == "X")["package"],
            "impl-a@1.2.0",
        )

    def test_conflict_without_alternative_fails(self):
        cat = catalog(
            pkg("impl-a", "1.0.0", ["X"], conflicts=["core"]),
            pkg("core", "1.0.0", ["Y"], conflicts=["impl-a"]),
        )
        with self.assertRaises(ResolutionError):
            resolve(build(["X", "Y"]), cat)

    def test_single_provider_for_two_packages(self):
        cat = catalog(
            pkg("a", "1.0.0", ["X", "Z"]),
            pkg("b", "2.0.0", ["Z"]),
        )
        result = resolve(build(["X", "Z"]), cat)
        self.assertEqual(capmap(result)["X"], "a@1.0.0")
        self.assertEqual(capmap(result)["Z"], "a@1.0.0")
        self.assertEqual(result["selected_packages"], ["a@1.0.0"])

    def test_overlap_backtracks_to_package_satisfying_all_caps(self):
        cat = catalog(
            pkg("a", "1.0.0", ["X", "Z"]),
            pkg("b", "2.0.0", ["X", "Z"],
                requires=["Y"], conflicts=["safe-y"]),
            pkg("safe-y", "1.0.0", ["Y"]),
        )
        result = resolve(build(["X", "Z", "Y"]), cat)
        self.assertEqual(capmap(result)["X"], "a@1.0.0")
        self.assertEqual(capmap(result)["Z"], "a@1.0.0")
        self.assertEqual(capmap(result)["Y"], "safe-y@1.0.0")
        self.assertEqual(
            sorted(set(result["mapping"])), ["X", "Y", "Z"]
        )

    def test_target_constraint_filters(self):
        cat = catalog(
            pkg("linux-only", "1.0.0", ["X"], os=["linux"]),
            pkg("multi", "2.0.0", ["X"], os=["linux", "darwin"]),
        )
        result = resolve(build(["X"], os=["darwin"], arch=["arm64"]), cat)
        self.assertEqual(capmap(result)["X"], "multi@2.0.0")
        with self.assertRaises(ResolutionError):
            resolve(build(["X"], os=["windows"], arch=["x86_64"]), cat)

    def test_prerelease_filtered_by_default(self):
        cat = catalog(
            pkg("a", "1.0.0", ["X"]),
            pkg("b", "2.0.0-beta.1", ["X"]),
        )
        self.assertEqual(capmap(resolve(build(["X"]), cat))["X"], "a@1.0.0")
        self.assertEqual(
            capmap(resolve(build(["X"], allow_prereleases=True), cat))["X"],
            "b@2.0.0-beta.1",
        )

    def test_alternative_cycle_terminates(self):
        a = pkg("a", "1.0.0", ["X"], alternatives=["b"])
        b = pkg("b", "1.0.0", ["X"], alternatives=["c"])
        c = pkg("c", "1.0.0", ["X"], alternatives=["a"])
        cat = catalog(a, b, c)
        chain = candidate_chain(cat, "X", build([]).target, False)
        self.assertEqual([p.name for p in chain], ["a", "b", "c"])
        self.assertEqual(capmap(resolve(build(["X"]), cat))["X"], "a@1.0.0")

    def test_cycle_starts_from_highest_ranked_member(self):
        a = pkg("a", "1.0.0", ["X"], alternatives=["b"])
        b = pkg("b", "2.0.0", ["X"], alternatives=["a"])
        cat = catalog(a, b)
        self.assertEqual(capmap(resolve(build(["X"]), cat))["X"], "b@2.0.0")

    def test_transitive_requirement(self):
        cat = catalog(
            pkg("app", "1.0.0", ["X"], requires=[{"capability": "net", "version": ">=2.0.0"}]),
            pkg("net1", "1.0.0", ["net"]),
            pkg("net2", "2.5.0", ["net"]),
        )
        result = resolve(build(["X"]), cat)
        self.assertEqual(capmap(result)["net"], "net2@2.5.0")

    def test_backtracking_on_transitive_conflict(self):
        cat = catalog(
            pkg("a", "1.0.0", [{"capability": "X", "version": "1.0.0", "priority": 10}],
                requires=["Y"], conflicts=["zlib2"]),
            pkg("b", "2.0.0", ["X"]),
            pkg("y-impl", "1.0.0", ["Y"], requires=["Z"]),
            pkg("zlib2", "1.0.0", ["Z"]),
        )
        result = resolve(build(["X"]), cat)
        self.assertEqual(capmap(result)["X"], "b@2.0.0")
        self.assertNotIn("Z", capmap(result))

    def test_optional_requirement_skipped_when_impossible(self):
        cat = catalog(
            pkg("a", "1.0.0", ["X"]),
            pkg("z", "1.0.0", ["Z"], os=["linux"]),
        )
        result = resolve(build(["X", {"capability": "Z", "optional": True}], os=["darwin"]), cat)
        self.assertEqual(capmap(result)["X"], "a@1.0.0")
        self.assertNotIn("Z", result["mapping"])

    def test_deterministic_across_runs(self):
        packages = [pkg(f"p{i:02d}", f"1.0.{i}", ["X"]) for i in range(20)]
        cat = catalog(*packages)
        first = capmap(resolve(build(["X"]), cat))
        second = capmap(resolve(build(["X"]), Catalog(list(reversed(cat.packages)))))
        self.assertEqual(first, second)


class TestInputValidation(unittest.TestCase):
    def test_duplicate_package(self):
        with self.assertRaises(ResolutionError):
            catalog(pkg("a", "1.0.0", ["X"]), pkg("a", "1.0.0", ["X"]))

    def test_unknown_alternative(self):
        with self.assertRaises(ResolutionError):
            catalog(pkg("a", "1.0.0", ["X"], alternatives=["ghost"]))

    def test_unknown_capability(self):
        cat = catalog(pkg("a", "1.0.0", ["X"]))
        with self.assertRaises(ResolutionError):
            resolve(build(["NOPE"]), cat)

    def test_bad_constraint(self):
        with self.assertRaises(ResolutionError):
            Constraint.parse("soon")


if __name__ == "__main__":
    unittest.main(verbosity=2)
