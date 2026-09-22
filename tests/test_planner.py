import unittest

from stageplan.model import catalog_from_data
from stageplan.planner import HARD, SOFT, plan


def package(name, version="1.0.0", depends_on=None, produces=None, needs_artifacts=None):
    entry = {"name": name, "version": version}
    if depends_on is not None:
        entry["depends_on"] = depends_on
    if produces is not None:
        entry["produces"] = produces
    if needs_artifacts is not None:
        entry["needs_artifacts"] = needs_artifacts
    return entry


class PlannerTests(unittest.TestCase):
    def test_soft_cycle_becomes_one_cocompile_group(self):
        catalog = catalog_from_data({"packages": [
            package("a", depends_on=["b"]),
            package("b", depends_on=["a"]),
        ]})
        report = plan(catalog)
        self.assertTrue(report.ok)
        self.assertEqual(len(report.stages), 1)
        group = report.stages[0]
        self.assertEqual(group.stage, 1)
        self.assertEqual(group.members, ("a", "b"))
        self.assertTrue(all(edge.kind == SOFT for edge in group.internal_edges))

    def test_hard_artifacts_assign_strict_stages(self):
        catalog = catalog_from_data({"packages": [
            package("gen", produces=["art"]),
            package("mid", produces=["art2"], needs_artifacts=["art"]),
            package("app", needs_artifacts=["art2"]),
        ]})
        report = plan(catalog)
        self.assertTrue(report.ok)
        by_stage = {group.stage: group.members for group in report.stages}
        self.assertEqual(by_stage[1], ("gen",))
        self.assertEqual(by_stage[2], ("mid",))
        self.assertEqual(by_stage[3], ("app",))

    def test_independent_groups_share_a_stage(self):
        catalog = catalog_from_data({"packages": [
            package("g1", produces=["a1"]),
            package("g2", produces=["a2"]),
            package("u1", needs_artifacts=["a1"]),
            package("u2", needs_artifacts=["a2"]),
        ]})
        report = plan(catalog)
        self.assertTrue(report.ok)
        stages = {}
        for group in report.stages:
            stages.setdefault(group.stage, set()).update(group.members)
        self.assertEqual(stages[1], {"g1", "g2"})
        self.assertEqual(stages[2], {"u1", "u2"})

    def test_generator_artifact_cycle_is_refused_with_evidence(self):
        catalog = catalog_from_data({"packages": [
            package("gen-a", produces=["art-a"], needs_artifacts=["art-b"]),
            package("gen-b", produces=["art-b"], needs_artifacts=["art-a"]),
        ]})
        report = plan(catalog)
        self.assertFalse(report.ok)
        self.assertEqual(len(report.blocked), 1)
        blocked = report.blocked[0]
        self.assertIn("CROSS_STAGE_CYCLE", blocked.codes)
        self.assertEqual(len(blocked.cycles), 1)
        cycle = blocked.cycles[0]
        self.assertTrue(all(edge.kind == HARD for edge in cycle.edges))
        self.assertTrue(cycle.render().startswith("gen-a") or cycle.render().startswith("gen-b"))
        # Edges chain together and the last edge returns to the first node.
        edges = cycle.edges
        for earlier, later in zip(edges, edges[1:]):
            self.assertEqual(earlier.target, later.source)
        self.assertEqual(edges[-1].target, edges[0].source)

    def test_hard_self_loop_is_refused(self):
        catalog = catalog_from_data({"packages": [
            package("self", produces=["art"], needs_artifacts=["art"]),
        ]})
        report = plan(catalog)
        self.assertFalse(report.ok)
        blocked = report.blocked[0]
        self.assertEqual(blocked.members, ("self",))
        self.assertEqual(blocked.cycles[0].edges[0].source, "self")
        self.assertEqual(blocked.cycles[0].edges[0].target, "self")

    def test_downstream_of_blocked_cycle_is_isolated_but_unrelated_groups_remain(self):
        catalog = catalog_from_data({"packages": [
            package("gen-a", produces=["art-a"], needs_artifacts=["art-b"]),
            package("gen-b", produces=["art-b"], needs_artifacts=["art-a"]),
            package("consumer", needs_artifacts=["art-a"]),
            package("safe-base", produces=["safe-art"]),
            package("safe-app", needs_artifacts=["safe-art"]),
        ]})
        report = plan(catalog)
        blocked_ids = {group.scc_id for group in report.blocked}
        executable_members = {m for group in report.stages for m in group.members}
        self.assertEqual(executable_members, {"safe-base", "safe-app"})
        consumers = [g for g in report.blocked if "consumer" in g.members]
        self.assertEqual(len(consumers), 1)
        self.assertEqual(consumers[0].codes, ("UPSTREAM_BLOCKED",))
        self.assertTrue(consumers[0].reasons[0].startswith("cannot build: upstream group"))

    def test_unknown_dependency_and_version_mismatch_block_package(self):
        catalog = catalog_from_data({"packages": [
            package("base", version="1.0.0"),
            package("picky", depends_on=[{"name": "base", "version": "^2.0.0"}]),
            package("ghost", depends_on=["missing"]),
        ]})
        report = plan(catalog)
        reasons = {r for b in report.blocked for r in b.reasons}
        self.assertTrue(any("does not satisfy ^2.0.0" in r for r in reasons))
        self.assertTrue(any("unknown dependency 'missing'" in r for r in reasons))
        executable = {m for group in report.stages for m in group.members}
        self.assertEqual(executable, {"base"})

    def test_unknown_artifact_blocks_consumer_only(self):
        catalog = catalog_from_data({"packages": [
            package("lucky"),
            package("needy", needs_artifacts=["nope"]),
        ]})
        report = plan(catalog)
        executable = {m for group in report.stages for m in group.members}
        self.assertEqual(executable, {"lucky"})
        blocked = next(b for b in report.blocked if b.members == ("needy",))
        self.assertIn("needs unknown artifact 'nope'", blocked.reasons[0])

    def test_target_limits_scope_to_closure(self):
        catalog = catalog_from_data({"packages": [
            package("base", produces=["art"]),
            package("app", needs_artifacts=["art"]),
            package("unrelated", produces=["u"]),
            package("other", needs_artifacts=["u"]),
        ]})
        report = plan(catalog, ["app"])
        members = {m for group in report.stages for m in group.members}
        self.assertEqual(members, {"base", "app"})
        self.assertEqual(report.in_scope, ("app", "base"))

    def test_malformed_package_is_excluded_but_others_plan(self):
        catalog = catalog_from_data({"packages": [
            {"name": "broken", "version": "nope"},
            package("fine"),
        ]})
        report = plan(catalog)
        self.assertIn("broken", report.package_errors)
        self.assertEqual(
            {m for group in report.stages for m in group.members}, {"fine"}
        )
        self.assertFalse(report.ok)


if __name__ == "__main__":
    unittest.main()
