"""Tests for the transitive dependency resolver: selection, lock-change
attribution, unsatisfiable explanations, cycles, and brute-force
cross-validation against the finite candidate fixtures."""

import itertools
import json
import os
import unittest

from versionset import (BuildInput, Repo, Resolver, Unsatisfiable, Version,
                        VersionSet)

FIXTURES = os.path.join(os.path.dirname(__file__), "..", "fixtures")


def load(name):
    with open(os.path.join(FIXTURES, name), encoding="utf-8") as fh:
        return json.load(fh)


def brute_force_solutions(repo, requirements, target=None, limit=10000):
    """Naively enumerate every partial assignment (each package may be
    absent) and keep exactly the valid ones: root requirements satisfied,
    the dependency closure fully assigned, and no extra packages.  This
    mirrors the resolver's semantics without sharing its code."""
    names = sorted(repo.names())
    choices = [[None] + repo.versions(n) for n in names]
    solutions = []
    for combo in itertools.product(*choices):
        picked = {n: pv for n, pv in zip(names, combo) if pv is not None}
        # Root requirements must be assigned and satisfied.
        if any(n not in picked or not spec.contains(picked[n].version, target)
               for n, spec in requirements.items()):
            continue
        # Dependency closure of the pick must be exactly the picked set.
        closure = set(requirements)
        frontier = list(closure)
        ok = True
        while frontier and ok:
            cur = frontier.pop()
            if cur not in picked:
                ok = False
                break
            for dep in picked[cur].deps:
                if not dep.applies_to(target):
                    continue
                if dep.name not in closure:
                    closure.add(dep.name)
                    frontier.append(dep.name)
        if not ok or closure != set(picked):
            continue
        # Every applicable dependency constraint must hold.
        for pv in picked.values():
            for dep in pv.deps:
                if not dep.applies_to(target):
                    continue
                if not dep.spec.contains(picked[dep.name].version, target):
                    ok = False
                    break
            if not ok:
                break
        if ok:
            solutions.append({n: pv.version for n, pv in picked.items()})
        if len(solutions) >= limit:
            break
    return solutions


class BasicResolutionTest(unittest.TestCase):
    def setUp(self):
        self.repo = Repo.from_json_file(os.path.join(FIXTURES, "repo_basic.json"))
        self.build = BuildInput.from_json_file(
            os.path.join(FIXTURES, "build_basic.json"))

    def test_resolves_highest_compatible(self):
        res = Resolver(self.repo, self.build.target).resolve_build(self.build)
        self.assertEqual(str(res.version_of("app")), "1.1.0")
        self.assertEqual(str(res.version_of("lib")), "1.5.0")
        self.assertEqual(str(res.version_of("util")), "0.6.0")

    def test_prerelease_not_selected_without_channel(self):
        # lib 1.6.0b1 exists but the b channel is never allowed here.
        res = Resolver(self.repo, self.build.target).resolve_build(self.build)
        self.assertNotEqual(str(res.version_of("lib")), "1.6.0b1")
        info = res.reasons["lib"]
        self.assertTrue(any(str(v) == "1.6.0b1" for v, _ in info.rejected))

    def test_reasons_track_excluding_constraints(self):
        res = Resolver(self.repo, self.build.target).resolve_build(self.build)
        info = res.reasons["lib"]
        rejected = {str(v): why for v, why in info.rejected}
        self.assertIn("1.0.0", rejected)
        self.assertIn("app@1.1.0", rejected["1.0.0"])

    def test_target_conditional_dependency(self):
        # app 1.0.0 depends on util only under the linux target; util is
        # also pulled in unconditionally via lib, so we assert on the
        # *constraint provenance* instead of mere presence.
        reqs = {"app": VersionSet.parse("=1.0.0")}
        linux = Resolver(self.repo, "linux").resolve(reqs)
        linux_sources = {c.source for c in linux.reasons["util"].constraints}
        self.assertIn("app@1.0.0", linux_sources)
        other = Resolver(self.repo, "windows").resolve(reqs)
        other_sources = {c.source for c in other.reasons["util"].constraints}
        self.assertNotIn("app@1.0.0", other_sources)


class LockChangeTest(unittest.TestCase):
    def setUp(self):
        self.repo = Repo.from_json_file(os.path.join(FIXTURES, "repo_basic.json"))
        self.reqs = {"app": VersionSet.parse("[1.0.0,2.0.0)")}

    def test_lock_pins_version(self):
        locked = BuildInput.from_json_file(
            os.path.join(FIXTURES, "build_locked.json"))
        res = Resolver(self.repo, locked.target).resolve_build(locked)
        self.assertEqual(str(res.version_of("lib")), "1.2.0")
        self.assertEqual(str(res.version_of("util")), "0.9.0")

    def test_lock_change_attributes_causing_constraints(self):
        resolver = Resolver(self.repo, "linux")
        changes = resolver.explain_lock_change(
            self.reqs,
            old_lock={},
            new_lock={"lib": Version.parse("1.2.0")})
        self.assertEqual(changes["lib"]["old"], "1.5.0")
        self.assertEqual(changes["lib"]["new"], "1.2.0")
        # util moved because lib 1.2.0 admits util 0.9.0 while lib 1.5.0's
        # constraint [0.6.0,0.9.0) excluded it.
        self.assertEqual(changes["util"]["old"], "0.6.0")
        self.assertEqual(changes["util"]["new"], "0.9.0")
        causes = " ".join(changes["util"]["caused_by"])
        self.assertIn("lib@1.2.0", causes)


class CycleTest(unittest.TestCase):
    def setUp(self):
        self.repo = Repo.from_json_file(
            os.path.join(FIXTURES, "repo_cycle_ok.json"))
        self.reqs = {"a": VersionSet.parse(">=1.0.0")}

    def test_compatible_cycle_resolves(self):
        res = Resolver(self.repo).resolve(self.reqs)
        self.assertEqual(str(res.version_of("a")), "1.1.0")
        self.assertEqual(str(res.version_of("b")), "1.1.0")

    def test_cycle_cross_validated_by_brute_force(self):
        resolver = Resolver(self.repo)
        found = {tuple(sorted(s.items()))
                 for s in resolver.iter_solutions(self.reqs)}
        expected = {tuple(sorted(s.items()))
                    for s in brute_force_solutions(self.repo, self.reqs)}
        self.assertEqual(found, expected)
        self.assertTrue(len(found) > 1)  # fixture is non-trivial

    def test_adjacent_open_boundary_in_fixture(self):
        # a 1.0.0 requires c in [1.0.0,2.0.0): c 2.0.0 must never appear
        # next to a 1.0.0 in any solution (open upper bound), while the
        # adjacent (2.0.0,...) region keeps c 2.5.0 unavailable too.
        for sol in brute_force_solutions(self.repo, self.reqs):
            if str(sol["a"]) == "1.0.0":
                self.assertEqual(str(sol["c"]), "1.5.0")

    def test_conflicting_cycle_is_unsatisfiable_with_explanation(self):
        repo = Repo.from_json_file(
            os.path.join(FIXTURES, "repo_cycle_conflict.json"))
        reqs = {"a": VersionSet.parse(">=1.0.0")}
        with self.assertRaises(Unsatisfiable) as ctx:
            Resolver(repo).resolve(reqs)
        text = ctx.exception.explanation()
        self.assertIn("'a'", text)
        self.assertIn("b@2.0.0", text)
        self.assertIn("empty intersection", text)
        # Brute force agrees: no assignment satisfies the fixture.
        self.assertEqual(brute_force_solutions(repo, reqs), [])


class UnsatisfiableTest(unittest.TestCase):
    def test_explanation_names_constraints_and_versions(self):
        repo = Repo.from_json_file(os.path.join(FIXTURES, "repo_basic.json"))
        reqs = {"app": VersionSet.parse("[1.0.0,2.0.0)"),
                "lib": VersionSet.parse("<1.0.0")}
        with self.assertRaises(Unsatisfiable) as ctx:
            Resolver(repo, "linux").resolve(reqs)
        text = ctx.exception.explanation()
        self.assertIn("'lib'", text)
        self.assertIn("root", text)
        self.assertIn("available versions", text)


if __name__ == "__main__":
    unittest.main()
