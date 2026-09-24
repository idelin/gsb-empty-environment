import json
import unittest

from lockmerge import LockFile, Registry, merge_locks


def pkg(version, **deps):
    return {"version": version, "dependencies": deps}


def make_lock(**packages):
    return LockFile.from_json({"packages": packages})


def registry_data():
    return {
        "packages": {
            "web": {
                "1.0.0": {"dependencies": {"utils": ">=1.0.0,<2.0.0"}},
                "1.1.0": {"dependencies": {"utils": ">=1.2.0,<2.0.0"}},
            },
            "db": {
                "1.0.0": {"dependencies": {"utils": ">=1.0.0,<1.3.0"}},
                "1.2.0": {"dependencies": {"utils": ">=1.1.0"}},
            },
            "utils": {v: {} for v in ("1.0.0", "1.1.0", "1.2.0", "1.3.0")},
            "cache": {
                "1.0.0": {"dependencies": {"utils": ">=1.0.0"}},
                "1.1.0": {"dependencies": {"utils": ">=1.0.0"}},
            },
            "docs": {"1.0.0": {}},
        }
    }


def base_lock():
    return make_lock(
        web=pkg("1.0.0", utils=">=1.0.0,<2.0.0"),
        db=pkg("1.0.0", utils=">=1.0.0,<1.3.0"),
        utils=pkg("1.1.0"),
        cache=pkg("1.0.0", utils=">=1.0.0"),
    )


class IndependentChangeTests(unittest.TestCase):
    def test_independent_changes_auto_merge(self):
        base = make_lock(
            web=pkg("1.0.0", utils=">=1.0.0,<2.0.0"),
            utils=pkg("1.1.0"),
            cache=pkg("1.0.0", utils=">=1.0.0"),
        )
        ours = make_lock(
            web=pkg("1.0.0", utils=">=1.0.0,<2.0.0"),
            utils=pkg("1.1.0"),
            cache=pkg("1.1.0", utils=">=1.0.0"),
        )
        theirs = make_lock(
            web=pkg("1.0.0", utils=">=1.0.0,<2.0.0"),
            utils=pkg("1.2.0"),
            cache=pkg("1.0.0", utils=">=1.0.0"),
        )
        result = merge_locks(base, ours, theirs, Registry(registry_data()))
        self.assertTrue(result.ok, result.unresolved)
        self.assertEqual(str(result.lock.packages["cache"].version), "1.1.0")
        self.assertEqual(str(result.lock.packages["utils"].version), "1.2.0")
        self.assertEqual(result.provenance["cache"], "ours")
        self.assertEqual(result.provenance["utils"], "theirs")
        self.assertEqual(result.provenance["web"], "base")

    def test_identical_upgrade_kept_once(self):
        ours = make_lock(
            web=pkg("1.1.0", utils=">=1.2.0,<2.0.0"),
            db=pkg("1.0.0", utils=">=1.0.0,<1.3.0"),
            utils=pkg("1.2.0"),
            cache=pkg("1.0.0", utils=">=1.0.0"),
        )
        theirs = make_lock(
            web=pkg("1.1.0", utils=">=1.2.0,<2.0.0"),
            db=pkg("1.0.0", utils=">=1.0.0,<1.3.0"),
            utils=pkg("1.2.0"),
            cache=pkg("1.0.0", utils=">=1.0.0"),
        )
        result = merge_locks(base_lock(), ours, theirs, Registry(registry_data()))
        self.assertTrue(result.ok, result.unresolved)
        web_changes = [c for c in result.changes if c.package == "web"]
        self.assertEqual(len(web_changes), 1)
        self.assertEqual(web_changes[0].source, "both")
        self.assertEqual(result.provenance["web"], "both")
        self.assertEqual(str(result.lock.packages["web"].version), "1.1.0")


class SharedDependencyTests(unittest.TestCase):
    def test_shared_dependency_conflict_revalidated(self):
        # Both sides upgrade different roots; the shared dep "utils" cannot be
        # concatenated naively -- it must be re-resolved against the registry.
        ours = make_lock(
            web=pkg("1.1.0", utils=">=1.2.0,<2.0.0"),
            db=pkg("1.0.0", utils=">=1.0.0,<1.3.0"),
            utils=pkg("1.1.0"),
            cache=pkg("1.0.0", utils=">=1.0.0"),
        )
        theirs = make_lock(
            web=pkg("1.0.0", utils=">=1.0.0,<2.0.0"),
            db=pkg("1.2.0", utils=">=1.1.0"),
            utils=pkg("1.1.0"),
            cache=pkg("1.0.0", utils=">=1.0.0"),
        )
        result = merge_locks(base_lock(), ours, theirs, Registry(registry_data()))
        self.assertTrue(result.ok, result.unresolved)
        self.assertEqual(str(result.lock.packages["web"].version), "1.1.0")
        self.assertEqual(str(result.lock.packages["db"].version), "1.2.0")
        self.assertEqual(str(result.lock.packages["utils"].version), "1.3.0")
        self.assertEqual(result.provenance["web"], "ours")
        self.assertEqual(result.provenance["db"], "theirs")
        self.assertEqual(result.provenance["utils"], "re-resolved")
        self.assertEqual(result.provenance["cache"], "base")

    def test_shared_dependency_conflict_unresolvable(self):
        data = registry_data()
        data["packages"]["db"]["1.2.0"] = {"dependencies": {"utils": ">=1.0.0,<1.2.0"}}
        ours = make_lock(
            web=pkg("1.1.0", utils=">=1.2.0,<2.0.0"),
            db=pkg("1.0.0", utils=">=1.0.0,<1.3.0"),
            utils=pkg("1.1.0"),
        )
        theirs = make_lock(
            web=pkg("1.0.0", utils=">=1.0.0,<2.0.0"),
            db=pkg("1.2.0", utils=">=1.0.0,<1.2.0"),
            utils=pkg("1.1.0"),
        )
        base = make_lock(
            web=pkg("1.0.0", utils=">=1.0.0,<2.0.0"),
            db=pkg("1.0.0", utils=">=1.0.0,<1.3.0"),
            utils=pkg("1.1.0"),
        )
        result = merge_locks(base, ours, theirs, Registry(data))
        self.assertFalse(result.ok)
        reasons = {u.reason for u in result.unresolved}
        self.assertIn("shared-dependency-conflict", reasons)
        self.assertEqual({u.package for u in result.unresolved}, {"utils"})

    def test_reresolution_adds_new_dependency(self):
        data = registry_data()
        data["packages"]["utils"]["1.3.0"] = {"dependencies": {"helpers": ">=1.0.0"}}
        data["packages"]["helpers"] = {"1.0.0": {}, "1.1.0": {}}
        ours = make_lock(
            web=pkg("1.1.0", utils=">=1.2.0,<2.0.0"),
            db=pkg("1.0.0", utils=">=1.0.0,<1.3.0"),
            utils=pkg("1.1.0"),
        )
        theirs = make_lock(
            web=pkg("1.0.0", utils=">=1.0.0,<2.0.0"),
            db=pkg("1.2.0", utils=">=1.1.0"),
            utils=pkg("1.1.0"),
        )
        base = make_lock(
            web=pkg("1.0.0", utils=">=1.0.0,<2.0.0"),
            db=pkg("1.0.0", utils=">=1.0.0,<1.3.0"),
            utils=pkg("1.1.0"),
        )
        result = merge_locks(base, ours, theirs, Registry(data))
        self.assertTrue(result.ok, result.unresolved)
        self.assertEqual(str(result.lock.packages["utils"].version), "1.3.0")
        self.assertEqual(str(result.lock.packages["helpers"].version), "1.1.0")
        self.assertEqual(result.provenance["helpers"], "re-resolved")


class DeletionTests(unittest.TestCase):
    def test_delete_of_referenced_package_rejected(self):
        theirs = make_lock(
            web=pkg("1.0.0", utils=">=1.0.0,<2.0.0"),
            db=pkg("1.0.0", utils=">=1.0.0,<1.3.0"),
            cache=pkg("1.0.0", utils=">=1.0.0"),
        )
        result = merge_locks(base_lock(), base_lock(), theirs, Registry(registry_data()))
        self.assertFalse(result.ok)
        dangling = [u for u in result.unresolved if u.reason == "dangling-reference"]
        self.assertEqual({u.package for u in dangling}, {"utils"})

    def test_delete_of_unreferenced_package_ok(self):
        base = make_lock(
            web=pkg("1.0.0", utils=">=1.0.0,<2.0.0"),
            utils=pkg("1.1.0"),
            docs=pkg("1.0.0"),
        )
        theirs = make_lock(
            web=pkg("1.0.0", utils=">=1.0.0,<2.0.0"),
            utils=pkg("1.1.0"),
        )
        result = merge_locks(base, base, theirs, Registry(registry_data()))
        self.assertTrue(result.ok, result.unresolved)
        self.assertNotIn("docs", result.lock.packages)
        removal = [c for c in result.changes if c.package == "docs"]
        self.assertEqual(removal[0].source, "theirs")

    def test_delete_vs_modify_conflict(self):
        base = make_lock(
            utils=pkg("1.1.0"),
            cache=pkg("1.0.0", utils=">=1.0.0"),
        )
        ours = make_lock(utils=pkg("1.1.0"))
        theirs = make_lock(
            utils=pkg("1.1.0"),
            cache=pkg("1.1.0", utils=">=1.0.0"),
        )
        result = merge_locks(base, ours, theirs, Registry(registry_data()))
        self.assertFalse(result.ok)
        conflicts = [u for u in result.unresolved if u.reason == "pin-conflict"]
        self.assertEqual({u.package for u in conflicts}, {"cache"})


class PinConflictTests(unittest.TestCase):
    def test_both_sides_change_same_package_differently(self):
        base = make_lock(
            web=pkg("1.0.0", utils=">=1.0.0,<2.0.0"),
            utils=pkg("1.1.0"),
        )
        ours = make_lock(
            web=pkg("1.0.0", utils=">=1.0.0,<2.0.0"),
            utils=pkg("1.2.0"),
        )
        theirs = make_lock(
            web=pkg("1.0.0", utils=">=1.0.0,<2.0.0"),
            utils=pkg("1.3.0"),
        )
        result = merge_locks(base, ours, theirs, Registry(registry_data()))
        self.assertFalse(result.ok)
        conflicts = [u for u in result.unresolved if u.reason == "pin-conflict"]
        self.assertEqual({u.package for u in conflicts}, {"utils"})
        # The conflicting pin stays at the base version for manual resolution.
        self.assertEqual(str(result.lock.packages["utils"].version), "1.1.0")


class PrereleaseTests(unittest.TestCase):
    def test_prerelease_pin_merges(self):
        data = {"packages": {"app": {"1.0.0": {}, "2.0.0-rc.1": {}, "2.0.0": {}}}}
        base = make_lock(app=pkg("1.0.0"))
        ours = make_lock(app=pkg("2.0.0-rc.1"))
        theirs = make_lock(app=pkg("1.0.0"))
        result = merge_locks(base, ours, theirs, Registry(data))
        self.assertTrue(result.ok, result.unresolved)
        self.assertEqual(str(result.lock.packages["app"].version), "2.0.0-rc.1")
        self.assertEqual(result.provenance["app"], "ours")


class ReportTests(unittest.TestCase):
    def test_report_is_json_serializable(self):
        result = merge_locks(base_lock(), base_lock(), base_lock(), Registry(registry_data()))
        report = result.to_report()
        text = json.dumps(report)
        self.assertEqual(report["status"], "ok")
        self.assertIn("provenance", report)
        self.assertIn("unresolved", report)
        self.assertIn("merged", report)
        self.assertIn("web", text)


if __name__ == "__main__":
    unittest.main()
