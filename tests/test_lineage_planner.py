"""Unit tests for the lineage erasure planner (synthetic data only)."""

import unittest

from lineage_planner import (
    Action,
    Artifact,
    ArtifactKind,
    LineageError,
    LineageGraph,
    Planner,
    UnknownSubjectError,
    execute_plan,
)


def base_graph() -> LineageGraph:
    g = LineageGraph()
    g.add_subject("alice", aliases=["alice@x.com", "alice-dev"])
    g.add_subject("bob", aliases=["bob@x.com"])
    g.add_artifact(Artifact("raw:alice", ArtifactKind.ORIGINAL, ("alice@x.com",)))
    g.add_artifact(Artifact("raw:bob", ArtifactKind.ORIGINAL, ("bob@x.com",)))
    g.add_artifact(Artifact("cache:alice", ArtifactKind.CACHE, ("raw:alice",)))
    g.add_artifact(Artifact(
        "derived:shared", ArtifactKind.DERIVED, ("raw:alice", "raw:bob"),
        contributions={"alice": 5, "bob": 3}, aggregator=sum,
    ))
    g.add_artifact(Artifact("derived:no-lineage", ArtifactKind.DERIVED,
                            ("raw:alice", "raw:bob")))
    g.add_artifact(Artifact("pub:report", ArtifactKind.PUBLISHED,
                            ("derived:shared",)))
    return g


class AliasMappingTests(unittest.TestCase):
    def test_alias_resolves_to_canonical_subject(self):
        plan = Planner(base_graph()).plan_erasure("alice-dev")
        self.assertEqual(plan.subject_id, "alice")
        self.assertIn("alice@x.com", plan.matched_aliases)

    def test_unmapped_identifier_fails_loudly(self):
        with self.assertRaises(UnknownSubjectError):
            Planner(base_graph()).plan_erasure("carol@x.com")

    def test_alias_collision_rejected(self):
        g = LineageGraph()
        g.add_subject("a", aliases=["x"])
        with self.assertRaises(LineageError):
            g.add_subject("b", aliases=["x"])

    def test_artifact_sources_normalize_aliases(self):
        g = base_graph()
        self.assertEqual(g.artifact("raw:alice").sources, ("alice",))


class PlanRuleTests(unittest.TestCase):
    def setUp(self):
        self.plan = Planner(base_graph()).plan_erasure("alice")

    def actions(self, artifact_id):
        return {s.action for s in self.plan.actions_for(artifact_id)}

    def test_sole_original_and_cache_are_deleted(self):
        self.assertEqual(self.actions("raw:alice"), {Action.DELETE})
        self.assertEqual(self.actions("cache:alice"), {Action.DELETE})

    def test_shared_artifact_is_recomputed_never_deleted(self):
        acts = self.actions("derived:shared")
        self.assertEqual(acts, {Action.RECOMPUTE})
        self.assertNotIn(Action.DELETE, acts)

    def test_shared_artifact_without_lineage_goes_to_manual_review(self):
        self.assertEqual(self.actions("derived:no-lineage"),
                         {Action.MANUAL_REVIEW})
        self.assertTrue(any("manual review" in w for w in self.plan.warnings))

    def test_published_is_flagged_unretractable_not_deleted(self):
        self.assertEqual(self.actions("pub:report"),
                         {Action.MARK_UNRETRACTABLE})
        self.assertTrue(any("cannot be retracted" in w
                            for w in self.plan.warnings))

    def test_optional_corrective_republication(self):
        plan = Planner(base_graph()).plan_erasure("alice",
                                                  republish_correction=True)
        acts = {s.action for s in plan.actions_for("pub:report")}
        self.assertIn(Action.REPUBLISH_CORRECTION, acts)
        self.assertIn(Action.MARK_UNRETRACTABLE, acts)

    def test_other_subjects_artifacts_untouched(self):
        self.assertEqual(self.plan.actions_for("raw:bob"), [])


class DryRunTests(unittest.TestCase):
    def test_planning_is_read_only(self):
        g = base_graph()
        store = {a.artifact_id: object() for a in g.artifacts()}
        before = dict(store)
        plan = Planner(g).plan_erasure("alice")
        self.assertTrue(plan.dry_run)
        self.assertEqual(store, before)
        self.assertEqual(g.artifact("derived:shared").contributions,
                         {"alice": 5, "bob": 3})


class ExecutionTests(unittest.TestCase):
    def test_execute_applies_deletes_and_recompute(self):
        g = base_graph()
        store = {
            "raw:alice": [1, 2], "raw:bob": [3], "cache:alice": "c",
            "derived:shared": 8, "derived:no-lineage": "x",
            "pub:report": "published",
        }
        plan = Planner(g).plan_erasure("alice")
        report = execute_plan(plan, g, store)

        self.assertNotIn("raw:alice", store)
        self.assertNotIn("cache:alice", store)
        self.assertEqual(store["derived:shared"], 3)  # bob's share only
        self.assertEqual(store["derived:no-lineage"], "x")  # manual review
        self.assertEqual(store["pub:report"], "published")  # never modified
        self.assertEqual(store["raw:bob"], [3])
        self.assertTrue(any("unretractable" in n for n in report.notes))

    def test_execute_without_correction_step_publishes_nothing(self):
        g = base_graph()
        plan = Planner(g).plan_erasure("alice")
        report = execute_plan(plan, g, {})
        self.assertFalse(any("re-publication" in n for n in report.notes))


if __name__ == "__main__":
    unittest.main()
