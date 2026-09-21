import unittest

from lockmerge.lockfile import LockfileError, validate_lockfile
from lockmerge.merge import merge_lockfiles
from lockmerge.versions import VersionError


def lock(roots=None, packages=None, build_inputs=None):
    return validate_lockfile({
        "version": 1,
        "roots": roots or {},
        "packages": packages or {},
        "build_inputs": build_inputs or {},
    })


def pkg(version, deps=None):
    return {"version": version, "dependencies": deps or {}}


class IndependentMergeTest(unittest.TestCase):
    def test_independent_changes_auto_merge(self):
        base = lock(packages={"a": pkg("1.0.0"), "b": pkg("1.0.0")})
        ours = lock(packages={"a": pkg("1.1.0"), "b": pkg("1.0.0")})
        theirs = lock(packages={"a": pkg("1.0.0"), "b": pkg("2.0.0")})
        result = merge_lockfiles(base, ours, theirs)
        self.assertTrue(result.clean, result.to_dict())
        self.assertEqual(result.lock.packages["a"]["version"], "1.1.0")
        self.assertEqual(result.lock.packages["b"]["version"], "2.0.0")
        self.assertEqual(result.provenance["packages"], {"a": "ours", "b": "theirs"})

    def test_identical_upgrade_kept_once(self):
        base = lock(packages={"a": pkg("1.0.0")})
        ours = lock(packages={"a": pkg("1.2.0")})
        theirs = lock(packages={"a": pkg("1.2.0")})
        result = merge_lockfiles(base, ours, theirs)
        self.assertTrue(result.clean)
        self.assertEqual(result.lock.packages["a"]["version"], "1.2.0")
        self.assertEqual(result.provenance["packages"]["a"], "both")

    def test_unchanged_entries_keep_base_provenance(self):
        base = lock(packages={"a": pkg("1.0.0")})
        result = merge_lockfiles(base, base, base)
        self.assertTrue(result.clean)
        self.assertEqual(result.provenance["packages"]["a"], "base")

    def test_additions_from_both_sides_combine(self):
        base = lock()
        ours = lock(packages={"a": pkg("1.0.0")})
        theirs = lock(packages={"b": pkg("2.0.0")})
        result = merge_lockfiles(base, ours, theirs)
        self.assertTrue(result.clean)
        self.assertEqual(set(result.lock.packages), {"a", "b"})

    def test_delete_on_one_side_kept_when_other_untouched(self):
        base = lock(packages={"a": pkg("1.0.0"), "b": pkg("1.0.0")})
        ours = lock(packages={"a": pkg("1.0.0")})
        theirs = lock(packages={"a": pkg("1.0.0"), "b": pkg("1.0.0")})
        result = merge_lockfiles(base, ours, theirs)
        self.assertTrue(result.clean)
        self.assertNotIn("b", result.lock.packages)

    def test_build_inputs_merge(self):
        base = lock(build_inputs={"opt": "O2", "target": "x86_64"})
        ours = lock(build_inputs={"opt": "O3", "target": "x86_64"})
        theirs = lock(build_inputs={"opt": "O2", "target": "aarch64"})
        result = merge_lockfiles(base, ours, theirs)
        self.assertTrue(result.clean)
        self.assertEqual(result.lock.build_inputs, {"opt": "O3", "target": "aarch64"})
        self.assertEqual(result.provenance["build_inputs"],
                         {"opt": "ours", "target": "theirs"})


class ConflictTest(unittest.TestCase):
    def test_modify_vs_modify_conflict(self):
        base = lock(packages={"a": pkg("1.0.0")})
        ours = lock(packages={"a": pkg("1.1.0")})
        theirs = lock(packages={"a": pkg("1.2.0")})
        result = merge_lockfiles(base, ours, theirs)
        self.assertFalse(result.clean)
        self.assertEqual(len(result.conflicts), 1)
        conflict = result.conflicts[0]
        self.assertEqual(conflict["kind"], "modify-vs-modify")
        self.assertEqual(conflict["name"], "a")
        # The conflicting entry is not silently concatenated into the lock.
        self.assertNotIn("a", result.lock.packages)

    def test_delete_vs_modify_conflict(self):
        base = lock(packages={"a": pkg("1.0.0")})
        ours = lock()
        theirs = lock(packages={"a": pkg("1.1.0")})
        result = merge_lockfiles(base, ours, theirs)
        self.assertFalse(result.clean)
        self.assertEqual(result.conflicts[0]["kind"], "delete-vs-modify")

    def test_root_constraint_conflict(self):
        base = lock(roots={"a": "^1.0.0"}, packages={"a": pkg("1.0.0")})
        ours = lock(roots={"a": "^1.1.0"}, packages={"a": pkg("1.1.0")})
        theirs = lock(roots={"a": "^1.2.0"}, packages={"a": pkg("1.2.0")})
        result = merge_lockfiles(base, ours, theirs)
        self.assertFalse(result.clean)
        self.assertEqual({c["section"] for c in result.conflicts}, {"roots", "packages"})


class ClosureValidationTest(unittest.TestCase):
    def test_shared_dependency_constraint_conflict_not_concatenated(self):
        # Both sides upgrade different roots; ours bumps the shared package to
        # 2.0.0, theirs keeps a root that still requires shared ^1.0.0.
        base = lock(
            roots={"app-a": "^1.0.0", "app-b": "^1.0.0"},
            packages={
                "app-a": pkg("1.0.0", {"shared": "^1.0.0"}),
                "app-b": pkg("1.0.0", {"shared": "^1.0.0"}),
                "shared": pkg("1.5.0"),
            },
        )
        ours = lock(
            roots={"app-a": "^2.0.0", "app-b": "^1.0.0"},
            packages={
                "app-a": pkg("2.0.0", {"shared": "^2.0.0"}),
                "app-b": pkg("1.0.0", {"shared": "^1.0.0"}),
                "shared": pkg("2.0.0"),
            },
        )
        theirs = lock(
            roots={"app-a": "^1.0.0", "app-b": "^1.1.0"},
            packages={
                "app-a": pkg("1.0.0", {"shared": "^1.0.0"}),
                "app-b": pkg("1.1.0", {"shared": "^1.0.0"}),
                "shared": pkg("1.5.0"),
            },
        )
        result = merge_lockfiles(base, ours, theirs)
        self.assertFalse(result.clean)
        self.assertEqual(result.conflicts, [])
        violations = [u for u in result.unresolvable
                      if u["kind"] == "constraint-violation"]
        self.assertEqual(len(violations), 1)
        self.assertEqual(violations[0]["package"], "shared")
        self.assertEqual(violations[0]["referenced_by"], "packages:app-b")
        self.assertEqual(violations[0]["locked_version"], "2.0.0")

    def test_deleted_package_still_referenced_is_rejected(self):
        base = lock(
            roots={"app": "^1.0.0"},
            packages={"app": pkg("1.0.0", {"lib": "^1.0.0"}), "lib": pkg("1.0.0")},
        )
        ours = lock(roots={"app": "^1.0.0"}, packages={"app": pkg("1.0.0", {"lib": "^1.0.0"})})
        theirs = base
        result = merge_lockfiles(base, ours, theirs)
        self.assertFalse(result.clean)
        rejected = [u for u in result.unresolvable if u["kind"] == "deleted-but-referenced"]
        self.assertEqual(len(rejected), 1)
        self.assertEqual(rejected[0]["package"], "lib")
        self.assertEqual(rejected[0]["referenced_by"], "packages:app")

    def test_missing_dependency_reported(self):
        base = lock()
        ours = lock(packages={"app": pkg("1.0.0", {"ghost": "^1.0.0"})})
        theirs = lock()
        result = merge_lockfiles(base, ours, theirs)
        self.assertFalse(result.clean)
        self.assertEqual(result.unresolvable[0]["kind"], "missing-dependency")

    def test_root_constraint_satisfied_after_merge(self):
        base = lock(roots={"a": "^1.0.0"}, packages={"a": pkg("1.0.0")})
        ours = lock(roots={"a": "^1.1.0"}, packages={"a": pkg("1.1.0")})
        theirs = base
        result = merge_lockfiles(base, ours, theirs)
        self.assertTrue(result.clean, result.to_dict())
        self.assertEqual(result.lock.roots["a"], "^1.1.0")

    def test_transitive_closure_validated(self):
        base = lock(packages={
            "app": pkg("1.0.0", {"mid": "^1.0.0"}),
            "mid": pkg("1.0.0", {"leaf": "^1.0.0"}),
            "leaf": pkg("1.0.0"),
        })
        ours = lock(packages={
            "app": pkg("1.0.0", {"mid": "^1.0.0"}),
            "mid": pkg("1.1.0", {"leaf": "^2.0.0"}),
            "leaf": pkg("1.0.0"),
        })
        theirs = base
        result = merge_lockfiles(base, ours, theirs)
        self.assertFalse(result.clean)
        self.assertEqual(result.unresolvable[0]["kind"], "constraint-violation")
        self.assertEqual(result.unresolvable[0]["package"], "leaf")


class InputValidationTest(unittest.TestCase):
    def test_bad_version_rejected(self):
        with self.assertRaises(VersionError):
            validate_lockfile({"packages": {"a": {"version": "1.2"}}})

    def test_bad_constraint_rejected(self):
        with self.assertRaises(VersionError):
            validate_lockfile({"roots": {"a": ">=1.2"}})

    def test_bad_structure_rejected(self):
        with self.assertRaises(LockfileError):
            validate_lockfile({"packages": []})
        with self.assertRaises(LockfileError):
            validate_lockfile({"version": 99})
        with self.assertRaises(LockfileError):
            validate_lockfile({"packages": {"a": {"dependencies": {}}}})


if __name__ == "__main__":
    unittest.main()
