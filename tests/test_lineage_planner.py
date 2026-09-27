from __future__ import annotations

import unittest
import json

from lineage_planner import (
    ActionKind,
    Artifact,
    ArtifactKind,
    Catalog,
    Subject,
    dry_run_plan,
    sample_catalog,
)
from lineage_planner.planner import PRIVACY_NOTE, plan_to_dict


class LineagePlannerTest(unittest.TestCase):
    def test_alias_resolves_to_canonical_subject_and_exposes_all_aliases(self) -> None:
        plan = dry_run_plan("A-001", sample_catalog())

        self.assertEqual(plan.canonical_subject_id, "alice")
        self.assertEqual(
            dict(plan.explicit_alias_map),
            {
                "A-001": "alice",
                "alice@example.test": "alice",
                "synthetic-alice": "alice",
            },
        )

    def test_shared_cache_is_recomputed_without_deleting_other_records(self) -> None:
        plan = dry_run_plan("alice", sample_catalog())
        actions = {action.artifact_id: action for action in plan.actions}

        shared_cache = actions["total_click_cache"]
        self.assertEqual(shared_cache.action, ActionKind.RECOMPUTE)
        self.assertEqual(shared_cache.parameters["projected_before"], 10)
        self.assertEqual(shared_cache.parameters["projected_after"], 7)
        self.assertEqual(shared_cache.parameters["retained_other_records"], 2)
        deleted = {
            action.artifact_id
            for action in plan.actions
            if action.action is ActionKind.DELETE_RAW
        }
        self.assertEqual(deleted, {"alice_click_1", "alice_click_2"})

    def test_derived_cache_is_invalidated_when_target_is_sole_subject(self) -> None:
        plan = dry_run_plan("alice", sample_catalog())
        actions = {action.artifact_id: action for action in plan.actions}

        sole_cache = actions["alice_profile_cache"]
        self.assertEqual(sole_cache.action, ActionKind.INVALIDATE)
        self.assertEqual(sole_cache.parameters["retained_other_records"], 0)

    def test_shared_published_stat_requests_retraction_then_corrected_publication(self) -> None:
        plan = dry_run_plan("alice", sample_catalog())
        actions = [action for action in plan.actions if action.artifact_id == "total_click_published"]

        self.assertEqual(
            [action.action for action in actions],
            [ActionKind.REQUEST_RETRACTION, ActionKind.PUBLISH_CORRECTED],
        )
        self.assertTrue(all(action.external for action in actions))
        self.assertEqual(actions[1].parameters["projected_value"], 7)

    def test_sole_subject_published_stat_only_requests_retraction(self) -> None:
        catalog = Catalog(
            subjects=(Subject("alice", aliases=("A-001",)), Subject("bob", aliases=("B-007",))),
            artifacts=(
                Artifact("alice_raw", ArtifactKind.RAW, "alice", value=1),
                Artifact(
                    "alice_only_published",
                    ArtifactKind.PUBLISHED,
                    None,
                    source_ids=("alice_raw",),
                    formula="sum",
                    external_ref="synthetic-report://alice-only",
                    value=1,
                ),
            ),
        )

        plan = dry_run_plan("A-001", catalog)
        actions = [
            action
            for action in plan.actions
            if action.artifact_id == "alice_only_published"
        ]

        self.assertEqual([action.action for action in actions], [ActionKind.REQUEST_RETRACTION])
        self.assertIn("third parties deleted published content", plan.warnings[1])

    def test_dry_run_is_read_only(self) -> None:
        catalog = sample_catalog()
        raw = catalog.artifact_by_id["alice_click_1"]

        plan = dry_run_plan("alice", catalog)

        self.assertTrue(plan.dry_run)
        self.assertIs(catalog.artifact_by_id["alice_click_1"], raw)
        self.assertEqual(catalog.artifact_by_id["total_click_cache"].value, 10)

    def test_statistical_experiment_is_not_claimed_as_privacy_proof(self) -> None:
        plan = dry_run_plan("alice", sample_catalog())

        self.assertIn(PRIVACY_NOTE, plan.warnings)

    def test_plan_is_json_serializable(self) -> None:
        plan = dry_run_plan("A-001", sample_catalog())

        encoded = json.dumps(plan_to_dict(plan), ensure_ascii=False)

        self.assertIn('"dry_run": true', encoded)

    def test_non_synthetic_subject_is_rejected(self) -> None:
        with self.assertRaisesRegex(ValueError, "not marked synthetic"):
            Catalog(
                subjects=(Subject("real-person", synthetic=False),),
                artifacts=(),
            )

    def test_non_synthetic_artifact_is_rejected(self) -> None:
        with self.assertRaisesRegex(ValueError, "not marked synthetic"):
            Catalog(
                subjects=(Subject("alice"),),
                artifacts=(
                    Artifact(
                        "real_raw",
                        ArtifactKind.RAW,
                        "alice",
                        synthetic=False,
                    ),
                ),
            )

    def test_alias_cannot_map_to_multiple_subjects(self) -> None:
        with self.assertRaisesRegex(ValueError, "maps to multiple subjects"):
            Catalog(
                subjects=(
                    Subject("alice", aliases=("shared-alias",)),
                    Subject("bob", aliases=("shared-alias",)),
                ),
                artifacts=(),
            )

    def test_alias_cannot_collide_with_canonical_subject_id(self) -> None:
        with self.assertRaisesRegex(ValueError, "cannot collide"):
            Catalog(
                subjects=(
                    Subject("alice", aliases=("bob",)),
                    Subject("bob"),
                ),
                artifacts=(),
            )

    def test_raw_record_cannot_have_lineage_sources(self) -> None:
        with self.assertRaisesRegex(ValueError, "must not have lineage sources"):
            Artifact("bad_raw", ArtifactKind.RAW, "alice", source_ids=("other_raw",))


if __name__ == "__main__":
    unittest.main()
