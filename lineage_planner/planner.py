from __future__ import annotations

from typing import Any

from .model import (
    ActionKind,
    Artifact,
    ArtifactKind,
    Catalog,
    DeletionPlan,
    PlannedAction,
)


PRIVACY_NOTE = (
    "statistical recomputation is an operational experiment only; "
    "it is not a formal privacy or differential-privacy proof"
)


def resolve_subject(subject_ref: str, catalog: Catalog) -> tuple[str, dict[str, str]]:
    if not subject_ref:
        raise ValueError("subject reference is required")
    for subject in catalog.subjects:
        if subject.canonical_id == subject_ref or subject_ref in subject.aliases:
            return subject.canonical_id, {
                alias: subject.canonical_id for alias in subject.aliases
            }
    raise ValueError(f"unknown synthetic subject reference: {subject_ref}")


def topological_order(catalog: Catalog) -> list[Artifact]:
    ordered: list[Artifact] = []
    visited: set[str] = set()

    def visit(artifact: Artifact) -> None:
        if artifact.artifact_id in visited:
            return
        for source_id in artifact.source_ids:
            visit(catalog.artifact_by_id[source_id])
        visited.add(artifact.artifact_id)
        ordered.append(artifact)

    for artifact in catalog.artifacts:
        visit(artifact)
    return ordered


def _leaf_sources(artifact: Artifact, catalog: Catalog) -> set[str]:
    if artifact.kind is ArtifactKind.RAW:
        return {artifact.artifact_id}
    leaves: set[str] = set()
    for source_id in artifact.source_ids:
        leaves.update(_leaf_sources(catalog.artifact_by_id[source_id], catalog))
    return leaves


def _descendants(raw_ids: set[str], catalog: Catalog) -> dict[str, set[str]]:
    reverse: dict[str, set[str]] = {artifact.artifact_id: set() for artifact in catalog.artifacts}
    for artifact in catalog.artifacts:
        for source_id in artifact.source_ids:
            reverse[source_id].add(artifact.artifact_id)

    closure = {raw_id: set() for raw_id in raw_ids}
    for raw_id in raw_ids:
        stack = list(reverse[raw_id])
        while stack:
            descendant_id = stack.pop()
            if descendant_id in closure[raw_id]:
                continue
            closure[raw_id].add(descendant_id)
            stack.extend(reverse[descendant_id])
    return closure


def _project(
    artifact: Artifact,
    catalog: Catalog,
    removed_raw_ids: set[str],
    memo: dict[str, Any],
) -> Any:
    if artifact.artifact_id in memo:
        return memo[artifact.artifact_id]
    if artifact.kind is ArtifactKind.RAW:
        value = None if artifact.artifact_id in removed_raw_ids else artifact.value
        memo[artifact.artifact_id] = value
        return value

    values = [
        _project(catalog.artifact_by_id[source_id], catalog, removed_raw_ids, memo)
        for source_id in artifact.source_ids
    ]
    numeric = [value for value in values if value is not None]
    if artifact.formula == "count":
        projected: Any = len(numeric)
    elif artifact.formula == "sum":
        projected = sum(numeric)
    elif artifact.formula == "average":
        projected = sum(numeric) / len(numeric) if numeric else None
    else:
        raise ValueError(f"unsupported formula: {artifact.formula}")
    memo[artifact.artifact_id] = projected
    return projected


def _projected_values(
    catalog: Catalog, removed_raw_ids: set[str]
) -> tuple[dict[str, Any], dict[str, Any]]:
    before_memo: dict[str, Any] = {}
    after_memo: dict[str, Any] = {}
    before = {
        artifact.artifact_id: _project(artifact, catalog, set(), before_memo)
        for artifact in topological_order(catalog)
    }
    after = {
        artifact.artifact_id: _project(artifact, catalog, removed_raw_ids, after_memo)
        for artifact in topological_order(catalog)
    }
    return before, after


def build_deletion_plan(subject_ref: str, catalog: Catalog) -> DeletionPlan:
    """Build a read-only deletion/recomputation plan for one synthetic subject."""
    canonical_id, alias_map = resolve_subject(subject_ref, catalog)
    target_raw = {
        artifact.artifact_id
        for artifact in catalog.artifacts
        if artifact.kind is ArtifactKind.RAW and artifact.owner_subject_id == canonical_id
    }
    closure = _descendants(target_raw, catalog)
    before, after = _projected_values(catalog, target_raw)

    actions: list[PlannedAction] = []
    warnings = [
        PRIVACY_NOTE,
        "external retraction requests do not prove that third parties deleted published content",
    ]

    affected_derived = sorted(
        {
            descendant_id
            for descendants in closure.values()
            for descendant_id in descendants
        }
    )
    for artifact_id in affected_derived:
        artifact = catalog.artifact_by_id[artifact_id]
        leaves = _leaf_sources(artifact, catalog)
        target_leaves = leaves & target_raw
        retained_leaves = leaves - target_raw
        common = {
            "contributing_subject_records": len(target_leaves),
            "retained_other_records": len(retained_leaves),
            "lineage_leaf_records": sorted(leaves),
        }
        if not retained_leaves:
            if artifact.kind is ArtifactKind.CACHE:
                actions.append(
                    PlannedAction(
                        ActionKind.INVALIDATE,
                        artifact_id,
                        {
                            **common,
                            "reason": "all lineage records belong to the target subject",
                        },
                    )
                )
            else:
                actions.append(
                    PlannedAction(
                        ActionKind.REQUEST_RETRACTION,
                        artifact_id,
                        {
                            **common,
                            "external_ref": artifact.external_ref,
                            "reason": "no non-target records remain to form a corrected statistic",
                        },
                        external=True,
                    )
                )
            continue

        projected_before = before[artifact_id]
        projected_after = after[artifact_id]
        if artifact.kind is ArtifactKind.CACHE:
            actions.append(
                PlannedAction(
                    ActionKind.RECOMPUTE,
                    artifact_id,
                    {
                        **common,
                        "formula": artifact.formula,
                        "projected_before": projected_before,
                        "projected_after": projected_after,
                    },
                )
            )
        else:
            actions.extend(
                [
                    PlannedAction(
                        ActionKind.REQUEST_RETRACTION,
                        artifact_id,
                        {
                            **common,
                            "external_ref": artifact.external_ref,
                            "published_value_to_retract": artifact.value,
                            "lineage_projected_before": projected_before,
                            "reason": "published statistic contains target-subject input",
                        },
                        external=True,
                    ),
                    PlannedAction(
                        ActionKind.PUBLISH_CORRECTED,
                        artifact_id,
                        {
                            **common,
                            "external_ref": artifact.external_ref,
                            "formula": artifact.formula,
                            "projected_value": projected_after,
                            "requires_successful_retraction_ticket": True,
                        },
                        external=True,
                    ),
                ]
            )
        if artifact.formula == "average" and projected_after is None:
            warnings.append(f"{artifact_id} has no retained records for an average")

    for raw_id in sorted(target_raw):
        actions.append(
            PlannedAction(
                ActionKind.DELETE_RAW,
                raw_id,
                {"subject_id": canonical_id, "record_class": "synthetic_raw_record"},
            )
        )

    return DeletionPlan(
        requested_subject_ref=subject_ref,
        canonical_subject_id=canonical_id,
        explicit_alias_map=alias_map,
        lineage_closure={key: tuple(sorted(value)) for key, value in sorted(closure.items())},
        actions=tuple(actions),
        warnings=tuple(warnings),
        dry_run=True,
    )


def dry_run_plan(subject_ref: str, catalog: Catalog) -> DeletionPlan:
    """Return a plan without reading external systems or mutating the catalog."""
    return build_deletion_plan(subject_ref, catalog)


def _json_value(value: Any) -> Any:
    if isinstance(value, dict):
        return {key: _json_value(item) for key, item in value.items()}
    if isinstance(value, (list, tuple)):
        return [_json_value(item) for item in value]
    if hasattr(value, "value"):
        return value.value
    return value


def plan_to_dict(plan: DeletionPlan) -> dict[str, Any]:
    return _json_value(
        {
            "requested_subject_ref": plan.requested_subject_ref,
            "canonical_subject_id": plan.canonical_subject_id,
            "explicit_alias_map": dict(plan.explicit_alias_map),
            "lineage_closure": {
                artifact_id: list(sources)
                for artifact_id, sources in plan.lineage_closure.items()
            },
            "actions": [
                {
                    "action": action.action,
                    "artifact_id": action.artifact_id,
                    "parameters": dict(action.parameters),
                    "external": action.external,
                }
                for action in plan.actions
            ],
            "warnings": list(plan.warnings),
            "dry_run": plan.dry_run,
        }
    )
