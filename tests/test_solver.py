import unittest

from depsolver.model import Lock, Repository, Requirements
from depsolver.report import render
from depsolver.solver import LockError, Solver, Status
from depsolver.version import Version


def repo(spec):
    return Repository.from_json({"packages": spec})


def lock(pinned):
    return Lock.from_json({"locked": pinned})


def reqs(requirements):
    return Requirements.from_json({"requirements": requirements})


def v(text):
    return Version.parse(text)


SHARED_REPO = {
    "app": {
        "1.0.0": {"dependencies": {"lib": ">=1.0.0,<3.0.0"}},
    },
    "lib": {
        "1.0.0": {},
        "2.0.0": {},
        "2.1.0": {},
    },
    "util": {
        "1.0.0": {},
    },
}

CASCADE_REPO = {
    "app": {
        "1.0.0": {"dependencies": {"lib": "<2.0.0"}},
        "2.0.0": {"dependencies": {"lib": ">=2.0.0"}},
    },
    "lib": {
        "1.5.0": {},
        "2.0.0": {},
    },
}


class TestMinimalChange(unittest.TestCase):
    def test_only_shared_dependency_changes(self):
        solver = Solver(
            repo(SHARED_REPO),
            lock({"app": "1.0.0", "lib": "1.0.0", "util": "1.0.0"}),
            reqs({"app": ">=1.0.0", "lib": ">=2.0.0", "util": ">=1.0.0"}),
        )
        result = solver.solve()
        self.assertIs(result.status, Status.SOLVED)
        self.assertTrue(result.optimal)
        self.assertEqual(
            result.resolution,
            {"app": v("1.0.0"), "lib": v("2.1.0"), "util": v("1.0.0")},
        )
        self.assertEqual(len(result.changes), 1)
        change = result.changes[0]
        self.assertEqual((change.package, change.kind), ("lib", "upgraded"))
        self.assertEqual((change.old, change.new), (v("1.0.0"), v("2.1.0")))

    def test_prefers_newest_among_equal_change_count(self):
        solver = Solver(
            repo(SHARED_REPO),
            lock({"lib": "1.0.0"}),
            reqs({"lib": ">=2.0.0"}),
        )
        result = solver.solve()
        self.assertEqual(result.resolution["lib"], v("2.1.0"))

    def test_lock_already_satisfying_needs_no_changes(self):
        solver = Solver(
            repo(SHARED_REPO),
            lock({"lib": "2.0.0"}),
            reqs({"lib": ">=1.0.0,<3.0.0"}),
        )
        result = solver.solve()
        self.assertIs(result.status, Status.SOLVED)
        self.assertEqual(result.changes, [])
        self.assertEqual(result.resolution, {"lib": v("2.0.0")})


class TestCascadeAndChains(unittest.TestCase):
    def solve_cascade(self):
        solver = Solver(
            repo(CASCADE_REPO),
            lock({"app": "1.0.0", "lib": "1.5.0"}),
            reqs({"app": "*", "lib": ">=2.0.0"}),
        )
        return solver.solve()

    def test_cascade_upgrades_dependent_package(self):
        result = self.solve_cascade()
        self.assertIs(result.status, Status.SOLVED)
        self.assertEqual(
            result.resolution, {"app": v("2.0.0"), "lib": v("2.0.0")}
        )
        kinds = {c.package: c.kind for c in result.changes}
        self.assertEqual(kinds, {"app": "upgraded", "lib": "upgraded"})

    def test_report_contains_causal_chain(self):
        result = self.solve_cascade()
        text = render(result, lock({"app": "1.0.0", "lib": "1.5.0"}), False)
        self.assertIn("chain: root -> lib", text)
        self.assertIn("chain: root -> app", text)
        self.assertIn('locked 1.5.0 violates ">=2.0.0" from root requirement', text)
        self.assertIn("app 1.0.0 requires lib <2.0.0", text)

    def test_added_dependency_is_reported(self):
        solver = Solver(
            repo(CASCADE_REPO),
            lock({"app": "2.0.0"}),
            reqs({"app": ">=2.0.0"}),
        )
        result = solver.solve()
        kinds = {c.package: c.kind for c in result.changes}
        self.assertEqual(kinds, {"lib": "added"})
        text = render(result, lock({"app": "2.0.0"}), False)
        self.assertIn("newly required by app 2.0.0", text)


class TestOutcomes(unittest.TestCase):
    def test_unsatisfiable_is_proven(self):
        solver = Solver(
            repo(SHARED_REPO),
            lock({"lib": "1.0.0"}),
            reqs({"lib": ">=5.0.0"}),
        )
        result = solver.solve()
        self.assertIs(result.status, Status.UNSATISFIABLE)
        self.assertTrue(result.optimal)
        self.assertEqual(result.resolution, {})
        self.assertTrue(any("lib" in c for c in result.conflicts))

    def test_search_limit_is_distinguished(self):
        solver = Solver(
            repo(CASCADE_REPO),
            lock({"app": "1.0.0", "lib": "1.5.0"}),
            reqs({"app": "*", "lib": ">=2.0.0"}),
            max_states=1,
        )
        result = solver.solve()
        self.assertIs(result.status, Status.LIMIT)
        self.assertFalse(result.optimal)
        # A larger budget proves the same instance solvable.
        solver = Solver(
            repo(CASCADE_REPO),
            lock({"app": "1.0.0", "lib": "1.5.0"}),
            reqs({"app": "*", "lib": ">=2.0.0"}),
            max_states=1000,
        )
        self.assertIs(solver.solve().status, Status.SOLVED)

    def test_locked_version_missing_is_an_error(self):
        solver = Solver(
            repo(SHARED_REPO),
            lock({"lib": "9.9.9"}),
            reqs({"lib": ">=1.0.0"}),
        )
        with self.assertRaisesRegex(LockError, "9.9.9"):
            solver.solve()

    def test_locked_package_missing_is_an_error(self):
        solver = Solver(
            repo(SHARED_REPO),
            lock({"ghost": "1.0.0"}),
            reqs({"lib": ">=1.0.0"}),
        )
        with self.assertRaisesRegex(LockError, "ghost"):
            solver.solve()


class TestOrphanPolicy(unittest.TestCase):
    TREE_REPO = {
        "app": {"1.0.0": {"dependencies": {"lib": ">=1.0.0"}}},
        "lib": {"1.0.0": {}},
    }

    def test_removed_root_prunes_unreachable_transitives(self):
        solver = Solver(
            repo(self.TREE_REPO),
            lock({"app": "1.0.0", "lib": "1.0.0"}),
            reqs({}),
        )
        result = solver.solve()
        self.assertIs(result.status, Status.SOLVED)
        self.assertEqual(result.resolution, {})
        kinds = {c.package: c.kind for c in result.changes}
        self.assertEqual(kinds, {"app": "removed", "lib": "removed"})
        text = render(result, lock({"app": "1.0.0", "lib": "1.0.0"}), False)
        self.assertIn("orphan policy: prune", text)

    def test_keep_orphans_pins_unreachable_packages(self):
        solver = Solver(
            repo(self.TREE_REPO),
            lock({"app": "1.0.0", "lib": "1.0.0"}),
            reqs({}),
            keep_orphans=True,
        )
        result = solver.solve()
        self.assertEqual(
            result.resolution, {"app": v("1.0.0"), "lib": v("1.0.0")}
        )
        self.assertEqual(result.changes, [])


class TestPrereleaseResolution(unittest.TestCase):
    def test_newest_prerelease_wins_when_it_is_the_maximum(self):
        solver = Solver(
            repo({"tool": {"1.0.0": {}, "1.1.0-beta.1": {}}}),
            lock({}),
            reqs({"tool": ">=1.0.0"}),
        )
        result = solver.solve()
        self.assertEqual(result.resolution["tool"], v("1.1.0-beta.1"))

    def test_locked_version_is_kept_over_newer_prerelease(self):
        # Minimal change beats "prefer newer": a satisfying lock is kept.
        solver = Solver(
            repo({"tool": {"1.0.0": {}, "1.1.0-beta.1": {}}}),
            lock({"tool": "1.0.0"}),
            reqs({"tool": ">=1.0.0"}),
        )
        result = solver.solve()
        self.assertEqual(result.resolution["tool"], v("1.0.0"))
        self.assertEqual(result.changes, [])

    def test_release_sorts_above_prerelease(self):
        solver = Solver(
            repo({"tool": {"1.1.0-beta.1": {}, "1.1.0": {}}}),
            lock({}),
            reqs({"tool": ">=1.0.0"}),
        )
        result = solver.solve()
        self.assertEqual(result.resolution["tool"], v("1.1.0"))


if __name__ == "__main__":
    unittest.main()
