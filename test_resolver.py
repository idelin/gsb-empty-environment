import unittest

from resolver import (
    Constraint,
    Package,
    Repository,
    ResolutionError,
    Resolver,
    Version,
)


def pkg(name, version, provides=(), conflicts=(), targets=(), requires=None):
    return Package.from_dict({
        "name": name,
        "version": version,
        "provides": list(provides),
        "conflicts": list(conflicts),
        "targets": list(targets),
        "requires": requires or {},
    })


def resolve(packages, requires, target=""):
    repo = Repository(packages)
    reqs = {cap: Constraint.parse(c) for cap, c in requires.items()}
    return Resolver(repo, target=target).resolve(reqs)


class VersionTest(unittest.TestCase):
    def test_ordering_with_prerelease(self):
        ordered = [
            "1.0.0-alpha", "1.0.0-alpha.1", "1.0.0-alpha.beta", "1.0.0-beta",
            "1.0.0-beta.2", "1.0.0-beta.11", "1.0.0-rc.1", "1.0.0",
        ]
        shuffled = [ordered[i] for i in (3, 0, 7, 1, 5, 2, 6, 4)]
        self.assertEqual(
            [str(v) for v in sorted(map(Version.parse, shuffled))], ordered
        )

    def test_numeric_fields_compare_numerically(self):
        self.assertLess(Version.parse("1.2.0"), Version.parse("1.10.0"))
        self.assertLess(Version.parse("1.10.0"), Version.parse("2.0.0"))

    def test_release_beats_prerelease(self):
        self.assertLess(Version.parse("2.0.0-rc.1"), Version.parse("2.0.0"))

    def test_invalid_version_rejected(self):
        for bad in ("1.0", "1.0.0.0", "v1.0.0", "1.0.x"):
            with self.assertRaises(ValueError):
                Version.parse(bad)


class ConstraintTest(unittest.TestCase):
    def test_range(self):
        c = Constraint.parse(">=1.2.0,<2.0.0")
        self.assertTrue(c.allows(Version.parse("1.5.0")))
        self.assertFalse(c.allows(Version.parse("2.0.0")))
        self.assertFalse(c.allows(Version.parse("1.0.0")))

    def test_empty_matches_any(self):
        self.assertTrue(Constraint.parse(None).allows(Version.parse("9.9.9")))

    def test_not_equal(self):
        c = Constraint.parse("!=1.0.0")
        self.assertFalse(c.allows(Version.parse("1.0.0")))
        self.assertTrue(c.allows(Version.parse("1.0.1")))


class ResolverTest(unittest.TestCase):
    def test_highest_version_wins(self):
        mapping = resolve(
            [pkg("a", "1.2.0", ["cap"]), pkg("a", "1.10.0", ["cap"])],
            {"cap": None},
        )
        self.assertEqual(str(mapping["cap"]), "a@1.10.0")

    def test_tie_break_by_name(self):
        mapping = resolve(
            [pkg("beta", "1.0.0", ["cap"]), pkg("alpha", "1.0.0", ["cap"])],
            {"cap": None},
        )
        self.assertEqual(str(mapping["cap"]), "alpha@1.0.0")

    def test_version_constraint_selects_older(self):
        mapping = resolve(
            [pkg("a", "2.0.0", ["cap"]), pkg("a", "1.5.0", ["cap"])],
            {"cap": "<2.0.0"},
        )
        self.assertEqual(str(mapping["cap"]), "a@1.5.0")

    def test_target_constraint_filters_provider(self):
        mapping = resolve(
            [
                pkg("win-only", "3.0.0", ["cap"], targets=["windows"]),
                pkg("linux-ok", "1.0.0", ["cap"], targets=["linux"]),
            ],
            {"cap": None},
            target="linux",
        )
        self.assertEqual(str(mapping["cap"]), "linux-ok@1.0.0")

    def test_conflict_falls_back_to_alternative(self):
        # Capability X can be provided by A or B; A conflicts with an
        # already-selected package, so B must be tried.
        mapping = resolve(
            [
                pkg("base", "1.0.0", ["core"]),
                pkg("impl-a", "2.0.0", ["x"], conflicts=["base"]),
                pkg("impl-b", "1.0.0", ["x"]),
            ],
            {"core": None, "x": None},
        )
        self.assertEqual(str(mapping["x"]), "impl-b@1.0.0")

    def test_single_provider_per_capability(self):
        mapping = resolve(
            [pkg("a", "1.0.0", ["cap"]), pkg("b", "1.0.0", ["cap"])],
            {"cap": None},
        )
        providers = {id(p) for p in mapping.values()}
        self.assertEqual(len(providers), 1)

    def test_one_package_can_cover_multiple_capabilities(self):
        mapping = resolve(
            [pkg("multi", "1.0.0", ["x", "y"])],
            {"x": None, "y": None},
        )
        self.assertIs(mapping["x"], mapping["y"])

    def test_backtracking_across_capabilities(self):
        # json is resolved first and picks 2.1.0, but the only logging
        # provider needs json <2.0.0, so json must be re-resolved to 1.9.2.
        mapping = resolve(
            [
                pkg("jnew", "2.1.0", ["json"]),
                pkg("jold", "1.9.2", ["json"]),
                pkg("logger", "1.1.0", ["logging"], requires={"json": "<2.0.0"}),
            ],
            {"json": None, "logging": None},
        )
        self.assertEqual(str(mapping["json"]), "jold@1.9.2")
        self.assertEqual(str(mapping["logging"]), "logger@1.1.0")

    def test_cyclic_alternative_chain_terminates(self):
        mapping = resolve(
            [
                pkg("cycle-a", "1.0.0", ["capA"], requires={"capB": None}),
                pkg("cycle-b", "1.0.0", ["capB"], requires={"capA": None}),
            ],
            {"capA": None},
        )
        self.assertEqual(str(mapping["capA"]), "cycle-a@1.0.0")
        self.assertEqual(str(mapping["capB"]), "cycle-b@1.0.0")

    def test_unsatisfiable_cycle_raises_quickly(self):
        packages = [
            pkg("cycle-a", "1.0.0", ["capA"], requires={"capB": ">=2.0.0"}),
            pkg("cycle-b", "1.0.0", ["capB"], requires={"capA": None}),
        ]
        with self.assertRaises(ResolutionError):
            resolve(packages, {"capA": None})

    def test_missing_capability_raises(self):
        with self.assertRaises(ResolutionError):
            resolve([], {"nope": None})

    def test_deterministic_result(self):
        packages = [
            pkg("b", "1.0.0", ["x"]),
            pkg("a", "1.0.0", ["x"]),
            pkg("c", "2.0.0", ["y"], requires={"x": None}),
        ]
        first = resolve(packages, {"y": None, "x": None})
        second = resolve(list(reversed(packages)), {"x": None, "y": None})
        self.assertEqual(
            {c: str(p) for c, p in first.items()},
            {c: str(p) for c, p in second.items()},
        )


if __name__ == "__main__":
    unittest.main()
