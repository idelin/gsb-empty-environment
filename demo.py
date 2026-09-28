"""Synthetic end-to-end demo: dry-run plan, then execute.

All data below is synthetic. The aggregate statistics shown are illustrative
only and are NOT formal privacy proofs.
"""

from lineage_planner import (
    Artifact,
    ArtifactKind,
    LineageGraph,
    Planner,
    execute_plan,
)


def build_graph() -> LineageGraph:
    g = LineageGraph()
    # Subject with two explicitly mapped aliases.
    g.add_subject("subj-alice", aliases=["alice@example.com", "alice-device-7"])
    g.add_subject("subj-bob", aliases=["bob@example.com"])

    # Original records (sole-source).
    g.add_artifact(Artifact("raw:alice-events", ArtifactKind.ORIGINAL,
                            sources=("alice@example.com",)))
    g.add_artifact(Artifact("raw:bob-events", ArtifactKind.ORIGINAL,
                            sources=("subj-bob",)))

    # Cache derived only from Alice.
    g.add_artifact(Artifact("cache:alice-session", ArtifactKind.CACHE,
                            sources=("raw:alice-events",)))

    # Shared derived aggregate with per-subject contributions (recomputable).
    g.add_artifact(Artifact(
        "derived:daily-active", ArtifactKind.DERIVED,
        sources=("raw:alice-events", "raw:bob-events"),
        contributions={"subj-alice": 5, "subj-bob": 3},
        aggregator=sum,
    ))

    # Shared derived artifact WITHOUT recompute info -> manual review.
    g.add_artifact(Artifact("derived:joint-embedding", ArtifactKind.DERIVED,
                            sources=("raw:alice-events", "raw:bob-events")))

    # Published statistic: cannot be retracted externally.
    g.add_artifact(Artifact("published:weekly-report", ArtifactKind.PUBLISHED,
                            sources=("derived:daily-active",)))
    return g


def main() -> None:
    graph = build_graph()
    store = {
        "raw:alice-events": ["click", "scroll"],
        "raw:bob-events": ["click"],
        "cache:alice-session": {"ttl": 3600},
        "derived:daily-active": 8,
        "derived:joint-embedding": [[0.1, 0.2]],
        "published:weekly-report": "DAU~8 (synthetic)",
    }

    planner = Planner(graph)

    print("=== DRY RUN (read-only; store untouched) ===")
    # Request erasure via an alias; resolution is explicit.
    plan = planner.plan_erasure("alice-device-7", republish_correction=True)
    print(f"subject: {plan.subject_id}  aliases: {plan.matched_aliases}")
    for step in plan.steps:
        print(f"  {step.action.value:<22} {step.artifact_id:<26} {step.reason}")
    for warning in plan.warnings:
        print(f"  WARNING: {warning}")
    print(f"store keys after dry run: {sorted(store)}")

    print("\n=== EXECUTE ===")
    report = execute_plan(plan, graph, store)
    for step in report.applied:
        print(f"  applied: {step.action.value} {step.artifact_id}")
    for note in report.notes:
        print(f"  note: {note}")
    print(f"store keys after execute: {sorted(store)}")
    print(f"derived:daily-active recomputed to: {store['derived:daily-active']}")


if __name__ == "__main__":
    main()
