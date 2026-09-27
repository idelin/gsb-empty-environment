from __future__ import annotations

from .model import Artifact, ArtifactKind, Catalog, Subject


def sample_catalog() -> Catalog:
    """Return an explicitly synthetic in-memory catalog for documentation/tests."""
    subjects = (
        Subject(
            canonical_id="alice",
            aliases=("A-001", "alice@example.test", "synthetic-alice"),
        ),
        Subject(canonical_id="bob", aliases=("B-007",)),
        Subject(canonical_id="carol", aliases=("C-003",)),
    )
    artifacts = (
        Artifact("alice_click_1", ArtifactKind.RAW, "alice", value=1),
        Artifact("alice_click_2", ArtifactKind.RAW, "alice", value=2),
        Artifact("bob_click_1", ArtifactKind.RAW, "bob", value=3),
        Artifact("carol_click_1", ArtifactKind.RAW, "carol", value=4),
        Artifact(
            "alice_profile_cache",
            ArtifactKind.CACHE,
            None,
            source_ids=("alice_click_1", "alice_click_2"),
            formula="sum",
            value=3,
        ),
        Artifact(
            "total_click_cache",
            ArtifactKind.CACHE,
            None,
            source_ids=(
                "alice_click_1",
                "alice_click_2",
                "bob_click_1",
                "carol_click_1",
            ),
            formula="sum",
            value=10,
        ),
        Artifact(
            "average_click_published",
            ArtifactKind.PUBLISHED,
            None,
            source_ids=(
                "alice_click_1",
                "alice_click_2",
                "bob_click_1",
                "carol_click_1",
            ),
            formula="average",
            external_ref="synthetic-report://experiment-42/average-clicks",
            value=2.5,
        ),
        Artifact(
            "total_click_published",
            ArtifactKind.PUBLISHED,
            None,
            source_ids=("total_click_cache",),
            formula="sum",
            external_ref="synthetic-report://experiment-42/total-clicks",
            value=10,
        ),
    )
    return Catalog(subjects=subjects, artifacts=artifacts)
