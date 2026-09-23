import unittest

from lockmerge.merger import merge_locks
from lockmerge.models import Lockfile, Registry


def lock(roots=None, inputs=None, **packages):
    pkgs = {name: {"version": ver} for name, ver in packages.items()}
    return Lockfile({"roots": roots or {},
                     "build_inputs": inputs or {},
                     "packages": pkgs})


def registry(data):
    return Registry({"packages": data})


def pkgs(lock_obj):
    return {n: e["version"] for n, e in lock_obj.to_dict()["packages"].items()}


REG = registry({
    "app": {
        "1.0.0": {"dependencies": {"web": "^2.0.0", "util": "^1.0.0"}},
        "1.1.0": {"dependencies": {"web": "^2.0.0", "util": "^1.1.0"}},
    },
    "web": {
        "2.4.0": {"dependencies": {"util": "^1.2.0"}},
        "2.5.0": {"dependencies": {"util": "^1.4.0"}},
        "3.0.0": {"dependencies": {"util": "^2.0.0"}},
    },
    "util": {
        "1.2.0": {}, "1.4.0": {}, "2.0.0": {},
    },
    "extra": {"1.0.0": {}},
    "solo": {"1.0.0": {}},
    "tool": {"1.0.0": {"dependencies": {"util": "^1.0.0"}}},
})


class TestIndependentChanges(unittest.TestCase):
    def test_different_packages_auto_merge(self):
        base = lock(app="1.0.0", web="2.4.0", util="1.4.0", roots={"app": "1.0.0"})
        ours = lock(app="1.0.0", web="2.5.0", util="1.4.0", roots={"app": "1.0.0"})
        theirs = lock(app="1.1.0", web="2.4.0", util="1.4.0", roots={"app": "1.1.0"})
        result = merge_locks(base, ours, theirs, REG)
        self.assertEqual(result.status, "merged")
        self.assertEqual(pkgs(result.merged_lock and Lockfile(result.merged_lock)),
                         {"app": "1.1.0", "web": "2.5.0", "util": "1.4.0"})
        sources = result.sources["packages"]
        self.assertEqual(sources["app"], "theirs")
        self.assertEqual(sources["web"], "ours")
        self.assertEqual(sources["util"], "base")

    def test_add_and_remove_root_auto_merge(self):
        base = lock(solo="1.0.0", roots={"solo": "1.0.0"})
        ours = lock(solo="1.0.0", extra="1.0.0",
                    roots={"solo": "1.0.0", "extra": "1.0.0"})
        theirs = lock(solo="1.0.0", roots={"solo": "1.0.0"},
                      inputs={"python": ">=3.11"})
        result = merge_locks(base, ours, theirs, REG)
        self.assertEqual(result.status, "merged")
        merged = result.merged_lock
        self.assertEqual(merged["roots"], {"solo": "1.0.0", "extra": "1.0.0"})
        self.assertEqual(merged["build_inputs"], {"python": ">=3.11"})
        self.assertEqual(result.sources["roots"]["extra"], "ours")


class TestSameChanges(unittest.TestCase):
    def test_identical_upgrade_recorded_once(self):
        base = lock(web="2.4.0", util="1.4.0")
        ours = lock(web="2.5.0", util="1.4.0")
        theirs = lock(web="2.5.0", util="1.4.0")
        result = merge_locks(base, ours, theirs, REG)
        self.assertEqual(result.status, "merged")
        self.assertEqual(result.conflict_hunks, [])
        self.assertEqual(pkgs(Lockfile(result.merged_lock)),
                         {"web": "2.5.0", "util": "1.4.0"})
        self.assertEqual(result.sources["packages"]["web"], "both")


class TestSharedConflict(unittest.TestCase):
    def test_both_change_same_package_differently_is_conflict(self):
        base = lock(app="1.0.0", web="2.4.0", util="1.4.0", roots={"app": "1.0.0"})
        ours = lock(app="1.0.0", web="2.5.0", util="1.4.0", roots={"app": "1.0.0"})
        theirs = lock(app="1.0.0", web="3.0.0", util="2.0.0", roots={"app": "1.0.0"})
        result = merge_locks(base, ours, theirs, REG)
        self.assertEqual(result.status, "needs_manual")
        # shared package is never spliced; base value is retained
        self.assertEqual(result.merged_lock["packages"]["web"]["version"], "2.4.0")
        self.assertEqual(result.sources["packages"]["web"], "conflict")
        hunk = result.conflict_hunks[0]
        self.assertEqual(hunk["type"], "both_changed_conflict")
        self.assertEqual(
            {hunk["base"], hunk["ours"], hunk["theirs"]},
            {"2.4.0", "2.5.0", "3.0.0"})

    def test_modify_delete_conflict(self):
        base = lock(app="1.0.0", web="2.4.0")
        ours = lock(app="1.0.0", web="2.5.0")
        theirs = lock(app="1.0.0")
        result = merge_locks(base, ours, theirs, REG)
        self.assertEqual(result.status, "needs_manual")
        self.assertEqual(result.conflict_hunks[0]["type"], "modify_delete_conflict")
        self.assertEqual(result.merged_lock["packages"]["web"]["version"], "2.5.0")

    def test_both_added_with_different_versions(self):
        base = lock(app="1.0.0")
        ours = lock(app="1.0.0", util="1.2.0")
        theirs = lock(app="1.0.0", util="1.4.0")
        result = merge_locks(base, ours, theirs, REG)
        kinds = {h["type"] for h in result.conflict_hunks}
        self.assertIn("both_added_conflict", kinds)
        self.assertEqual(result.status, "needs_manual")

    def test_build_input_conflict_is_not_spliced(self):
        base = lock(inputs={"python": ">=3.10,<4"})
        ours = lock(inputs={"python": ">=3.11,<4"})
        theirs = lock(inputs={"python": ">=3.12,<4"})
        result = merge_locks(base, ours, theirs, REG)
        self.assertEqual(result.status, "needs_manual")
        self.assertEqual(result.merged_lock["build_inputs"]["python"], ">=3.10,<4")
        self.assertEqual(result.sources["build_inputs"]["python"], "conflict")


class TestDeletions(unittest.TestCase):
    def test_delete_still_referenced_is_rejected(self):
        base = lock(app="1.0.0", web="2.4.0", util="1.4.0", roots={"app": "1.0.0"})
        ours = lock(app="1.0.0", util="1.4.0", roots={"app": "1.0.0"})
        theirs = lock(app="1.0.0", web="2.4.0", util="1.4.0", roots={"app": "1.0.0"})
        result = merge_locks(base, ours, theirs, REG)
        self.assertEqual(result.status, "needs_manual")
        self.assertEqual(len(result.rejected_deletions), 1)
        rejected = result.rejected_deletions[0]
        self.assertEqual(rejected["package"], "web")
        self.assertIn("app@1.0.0", rejected["referenced_by"])
        self.assertEqual(result.merged_lock["packages"]["web"]["version"], "2.4.0")
        self.assertEqual(result.sources["packages"]["web"], "restored")

    def test_delete_unreferenced_is_accepted(self):
        base = lock(solo="1.0.0", extra="1.0.0", roots={"solo": "1.0.0"})
        ours = lock(solo="1.0.0", roots={"solo": "1.0.0"})
        theirs = lock(solo="1.0.0", extra="1.0.0", roots={"solo": "1.0.0"})
        result = merge_locks(base, ours, theirs, REG)
        self.assertEqual(result.status, "merged")
        self.assertNotIn("extra", result.merged_lock["packages"])
        self.assertEqual(result.sources["packages"]["extra"], "ours-deleted")

    def test_both_delete_is_accepted(self):
        base = lock(solo="1.0.0", extra="1.0.0", roots={"solo": "1.0.0"})
        ours = lock(solo="1.0.0", roots={"solo": "1.0.0"})
        theirs = lock(solo="1.0.0", roots={"solo": "1.0.0"})
        result = merge_locks(base, ours, theirs, REG)
        self.assertEqual(result.status, "merged")
        self.assertNotIn("extra", result.merged_lock["packages"])


class TestClosureVerification(unittest.TestCase):
    def test_shared_constraint_violation_flagged(self):
        # theirs takes util 2.0.0 but merged app/web still need ^1.x
        base = lock(app="1.0.0", web="2.4.0", util="1.4.0", roots={"app": "1.0.0"})
        ours = lock(app="1.1.0", web="2.5.0", util="1.4.0", roots={"app": "1.1.0"})
        theirs = lock(app="1.0.0", web="2.4.0", util="2.0.0", roots={"app": "1.0.0"})
        result = merge_locks(base, ours, theirs, REG)
        violations = {(x["type"], x["name"]) for x in result.closure_violations}
        self.assertIn(("constraint_violation", "util"), violations)
        self.assertEqual(result.status, "needs_manual")

    def test_missing_dependency_flagged(self):
        reg = registry({
            "solo": {"1.0.0": {}},
            "newpkg": {"1.0.0": {"dependencies": {"ghost": "^1.0.0"}}},
        })
        base = lock(solo="1.0.0", roots={"solo": "1.0.0"})
        ours = lock(solo="1.0.0", newpkg="1.0.0", roots={"solo": "1.0.0"})
        result = merge_locks(base, ours, base, reg)
        self.assertTrue(any(v["type"] == "missing_dependency"
                            for v in result.closure_violations))

    def test_unknown_package_version_flagged(self):
        base = lock(app="1.0.0", web="2.4.0", util="1.4.0", roots={"app": "1.0.0"})
        ours = lock(app="1.0.0", web="9.9.9", util="1.4.0", roots={"app": "1.0.0"})
        result = merge_locks(base, ours, base, REG)
        self.assertTrue(any(v["type"] == "unknown_package"
                            for v in result.closure_violations))

    def test_root_version_mismatch_flagged(self):
        base = lock(app="1.0.0", roots={"app": "1.0.0"})
        ours = Lockfile({"roots": {"app": "1.1.0"}, "build_inputs": {},
                         "packages": {"app": {"version": "1.0.0"}}})
        result = merge_locks(base, ours, base, REG)
        self.assertTrue(any(v["type"] == "root_version_mismatch"
                            for v in result.closure_violations))

    def test_orphan_is_warning_not_violation(self):
        base = lock(solo="1.0.0", extra="1.0.0", roots={"solo": "1.0.0"})
        result = merge_locks(base, base, base, REG)
        self.assertEqual(result.closure_violations, [])
        self.assertTrue(any(w["type"] == "orphan_package"
                            for w in result.warnings))
        self.assertEqual(result.status, "merged")

    def test_unprocessable_collects_everything(self):
        base = lock(app="1.0.0", web="2.4.0", util="1.4.0", roots={"app": "1.0.0"})
        ours = lock(app="1.1.0", web="2.5.0", util="1.4.0", roots={"app": "1.1.0"})
        theirs = lock(app="1.0.0", web="3.0.0", util="2.0.0", roots={"app": "1.0.0"})
        result = merge_locks(base, ours, theirs, REG)
        types = {item["type"] for item in result.unprocessable}
        self.assertIn("merge_conflict", types)
        self.assertIn("closure_violation", types)
        self.assertEqual(result.status, "needs_manual")


if __name__ == "__main__":
    unittest.main()
