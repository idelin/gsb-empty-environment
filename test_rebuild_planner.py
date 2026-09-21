import io
import json
import unittest
from contextlib import redirect_stdout
from pathlib import Path
from tempfile import TemporaryDirectory

from rebuild_planner import (
    Constraint,
    PlanError,
    Version,
    build_report,
    load_input,
    main,
    make_plan,
    verify_plan,
)


def node(node_id, package="pkg", version="1.0.0", cost=10, digest=None,
         dependencies=None, available=True):
    return {
        "id": node_id,
        "package": package,
        "version": version,
        "cost": cost,
        "digest": digest or f"digest-{node_id}",
        "dependencies": dependencies or [],
        "available": available,
    }


def scenario():
    return {
        "nodes": [
            node("base", "base", "1.0.0", cost=100, digest="d-base"),
            node("mid", "mid", "1.2.0", cost=200, digest="d-mid",
                 dependencies=["base"]),
            node("a", "a", "3.0.0", cost=50, digest="d-a",
                 dependencies=["mid", "base"]),
            node("b", "b", "2.1.0", cost=70, digest="d-b",
                 dependencies=["mid"]),
        ],
        "cache": {"base": "d-base"},
        "failed": ["base", "mid", "a", "b"],
        "targets": ["a", "b"],
    }


class VersionTests(unittest.TestCase):
    def test_parse_and_format(self):
        version = Version.parse("1.2.3-rc.1+build.42")
        self.assertEqual((version.major, version.minor, version.patch), (1, 2, 3))
        self.assertEqual(version.prerelease, ("rc", "1"))
        self.assertEqual(str(version), "1.2.3-rc.1+build.42")
        self.assertTrue(version.is_prerelease)

    def test_invalid_versions(self):
        for bad in ("1.2", "1.2.3.4", "v1.2.3", "01.2.3", "1.2.3-",
                    "1.2.3-01", "1.2.3-rc_1", ""):
            with self.subTest(bad=bad):
                with self.assertRaises(PlanError):
                    Version.parse(bad)

    def test_semver_prerelease_ordering(self):
        ordered = [
            "1.0.0-alpha",
            "1.0.0-alpha.1",
            "1.0.0-alpha.beta",
            "1.0.0-beta",
            "1.0.0-beta.2",
            "1.0.0-beta.11",
            "1.0.0-rc.1",
            "1.0.0",
        ]
        versions = [Version.parse(text) for text in ordered]
        self.assertEqual(versions, sorted(versions))


class ConstraintTests(unittest.TestCase):
    def test_caret_ranges(self):
        constraint = Constraint.parse("^1.2.3")
        self.assertTrue(constraint.matches(Version.parse("1.2.3")))
        self.assertTrue(constraint.matches(Version.parse("1.9.0")))
        self.assertFalse(constraint.matches(Version.parse("2.0.0")))
        self.assertFalse(constraint.matches(Version.parse("1.2.2")))

        zero = Constraint.parse("^0.2.3")
        self.assertTrue(zero.matches(Version.parse("0.2.9")))
        self.assertFalse(zero.matches(Version.parse("0.3.0")))

    def test_tilde_ranges(self):
        constraint = Constraint.parse("~1.2.3")
        self.assertTrue(constraint.matches(Version.parse("1.2.9")))
        self.assertFalse(constraint.matches(Version.parse("1.3.0")))

    def test_comparison_operators(self):
        self.assertTrue(Constraint.parse(">=2.0.0").matches(Version.parse("2.1.0")))
        self.assertFalse(Constraint.parse("<2.0.0").matches(Version.parse("2.0.0")))
        self.assertTrue(Constraint.parse(">1.0.0").matches(Version.parse("1.0.1")))
        self.assertTrue(Constraint.parse("<=1.0.0").matches(Version.parse("1.0.0")))

    def test_explicit_prerelease_match(self):
        exact = Constraint.parse("=2.0.0-rc.1")
        self.assertTrue(exact.matches(Version.parse("2.0.0-rc.1")))
        self.assertFalse(exact.matches(Version.parse("2.0.0-rc.2")))

    def test_prereleases_hidden_without_flag(self):
        stable = Constraint.parse(">=1.0.0")
        self.assertFalse(stable.matches(Version.parse("2.0.0-beta")))
        allowed = Constraint(">=", Version.parse("1.0.0"), True)
        self.assertTrue(allowed.matches(Version.parse("2.0.0-beta")))


class PlannerTests(unittest.TestCase):
    def test_shared_dependency_cost_counted_once(self):
        build_input = load_input(scenario())
        plan = make_plan(build_input)
        # base restored (0), mid built once (200), a (50), b (70).
        self.assertEqual(plan.total_cost, 320)
        self.assertEqual(plan.built, ("mid", "a", "b"))
        self.assertEqual(plan.restored, ("base",))
        step_ids = [step.node_id for step in plan.steps]
        self.assertLess(step_ids.index("base"), step_ids.index("mid"))
        self.assertLess(step_ids.index("mid"), step_ids.index("a"))
        # Naively summing per target would charge mid/base twice (620).
        self.assertNotEqual(plan.total_cost, 620)
        verify_plan(plan, build_input)

    def test_cache_digest_mismatch_forces_rebuild(self):
        data = scenario()
        data["cache"]["mid"] = "stale-digest"
        build_input = load_input(data)
        plan = make_plan(build_input)
        mid_step = next(step for step in plan.steps if step.node_id == "mid")
        self.assertEqual(mid_step.action, "build")
        self.assertEqual(mid_step.cost, 200)
        verify_plan(plan, build_input)

    def test_local_artifact_has_zero_cost_and_no_action(self):
        data = scenario()
        data["failed"] = []
        build_input = load_input(data)
        plan = make_plan(build_input)
        self.assertEqual(plan.total_cost, 0)
        self.assertEqual(set(step.action for step in plan.steps), {"local"})
        verify_plan(plan, build_input)

    def test_highest_version_selected_and_prerelease_excluded(self):
        data = scenario()
        data["nodes"].append(
            node("a-400", "a", "4.0.0-alpha", cost=10, digest="d-a-alpha",
                 dependencies=["base"])
        )
        data["targets"] = [{"package": "a", "constraint": ">=3.0.0"}]
        build_input = load_input(data)
        plan = make_plan(build_input)
        self.assertEqual(plan.targets, ("a",))
        self.assertNotIn("a-400", [step.node_id for step in plan.steps])

        data["targets"] = [
            {"package": "a", "constraint": ">=4.0.0-alpha",
             "allow_prerelease": True}
        ]
        plan = make_plan(load_input(data))
        self.assertEqual(plan.targets, ("a-400",))

    def test_dependency_cycle_rejected(self):
        data = scenario()
        data["nodes"][1]["dependencies"] = ["base", "a"]
        with self.assertRaisesRegex(PlanError, "dependency cycle"):
            make_plan(load_input(data))

    def test_unknown_dependency_rejected(self):
        data = scenario()
        data["nodes"][0]["dependencies"] = ["ghost"]
        with self.assertRaisesRegex(PlanError, "unknown node"):
            load_input(data)

    def test_unsatisfiable_target_rejected(self):
        data = scenario()
        data["targets"] = ["a@^9.0.0"]
        with self.assertRaisesRegex(PlanError, "no node satisfies target"):
            make_plan(load_input(data))

    def test_verifier_rejects_out_of_order_and_wrong_action(self):
        build_input = load_input(scenario())
        plan = make_plan(build_input)
        shuffled = tuple(reversed(plan.steps))
        bad = type(plan)(
            shuffled, plan.targets, plan.total_cost, plan.restored,
            plan.built, plan.local
        )
        with self.assertRaisesRegex(PlanError, "before dependencies"):
            verify_plan(bad, build_input)

    def test_report_verifies_every_node(self):
        report = build_report(load_input(scenario()))
        self.assertTrue(all(item["ok"] for item in report["verification"]))
        actions = {item["node_id"]: item for item in report["verification"]}
        self.assertIn("dependencies_available", actions["a"])
        self.assertEqual(actions["a"]["dependencies_available"], ["mid", "base"])

    def test_cli_json_and_human_output(self):
        with TemporaryDirectory() as directory:
            path = Path(directory) / "input.json"
            path.write_text(json.dumps(scenario()), encoding="utf-8")
            buffer = io.StringIO()
            with redirect_stdout(buffer):
                code = main([str(path), "--json"])
            self.assertEqual(code, 0)
            self.assertEqual(json.loads(buffer.getvalue())["total_cost"], 320)

            with redirect_stdout(buffer):
                self.assertEqual(main([str(path)]), 0)
            self.assertIn("Total cost:  320", buffer.getvalue())

            with redirect_stdout(buffer):
                self.assertEqual(main([str(Path(directory) / "missing.json")]), 2)


if __name__ == "__main__":
    unittest.main()
