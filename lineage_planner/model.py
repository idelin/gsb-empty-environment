from __future__ import annotations

from dataclasses import dataclass, field
from enum import Enum
from types import MappingProxyType
from typing import Any, Mapping


class ArtifactKind(str, Enum):
    RAW = "raw_record"
    CACHE = "cache"
    PUBLISHED = "published_stat"


class ActionKind(str, Enum):
    DELETE_RAW = "delete_raw_record"
    INVALIDATE = "invalidate_sole_cache"
    RECOMPUTE = "recompute_shared_derived"
    REQUEST_RETRACTION = "request_external_retraction"
    PUBLISH_CORRECTED = "publish_corrected_stat"


def _frozen_mapping(value: Mapping[str, Any] | None) -> Mapping[str, Any]:
    return MappingProxyType(dict(value or {}))


@dataclass(frozen=True)
class Subject:
    canonical_id: str
    aliases: tuple[str, ...] = ()
    synthetic: bool = True

    def __post_init__(self) -> None:
        if not self.canonical_id:
            raise ValueError("subject canonical_id is required")
        seen: set[str] = set()
        for alias in self.aliases:
            if not alias:
                raise ValueError("subject aliases must be non-empty")
            if alias in seen:
                raise ValueError(f"duplicate alias for {self.canonical_id}: {alias}")
            seen.add(alias)


@dataclass(frozen=True)
class Artifact:
    artifact_id: str
    kind: ArtifactKind
    owner_subject_id: str | None
    synthetic: bool = True
    source_ids: tuple[str, ...] = ()
    formula: str | None = None
    parameters: Mapping[str, Any] = field(default_factory=dict)
    external_ref: str | None = None
    value: Any = None

    def __post_init__(self) -> None:
        if not self.artifact_id:
            raise ValueError("artifact_id is required")
        if self.kind not in ArtifactKind:
            raise ValueError(f"unknown artifact kind: {self.kind}")
        if self.kind is ArtifactKind.RAW and self.owner_subject_id is None:
            raise ValueError(f"raw artifact {self.artifact_id} requires an owner")
        if self.kind is ArtifactKind.RAW and self.source_ids:
            raise ValueError(f"raw artifact {self.artifact_id} must not have lineage sources")
        if self.kind is ArtifactKind.RAW and self.formula is not None:
            raise ValueError(f"raw artifact {self.artifact_id} must not have a formula")
        if self.kind is not ArtifactKind.RAW and self.owner_subject_id is not None:
            raise ValueError(
                f"derived artifact {self.artifact_id} must not claim a single owner"
            )
        if self.kind is not ArtifactKind.RAW and not self.source_ids:
            raise ValueError(f"derived artifact {self.artifact_id} requires sources")
        if self.kind is not ArtifactKind.RAW and not self.formula:
            raise ValueError(f"derived artifact {self.artifact_id} requires a formula")
        if self.kind is ArtifactKind.PUBLISHED and not self.external_ref:
            raise ValueError(f"published artifact {self.artifact_id} requires external_ref")
        if self.kind is not ArtifactKind.PUBLISHED and self.external_ref is not None:
            raise ValueError("only published artifacts may have external_ref")
        object.__setattr__(self, "source_ids", tuple(self.source_ids))
        object.__setattr__(self, "parameters", _frozen_mapping(self.parameters))


@dataclass(frozen=True)
class Catalog:
    subjects: tuple[Subject, ...]
    artifacts: tuple[Artifact, ...]

    def __post_init__(self) -> None:
        object.__setattr__(self, "subjects", tuple(self.subjects))
        object.__setattr__(self, "artifacts", tuple(self.artifacts))
        self.validate()

    @property
    def subject_by_id(self) -> Mapping[str, Subject]:
        return MappingProxyType({s.canonical_id: s for s in self.subjects})

    @property
    def artifact_by_id(self) -> Mapping[str, Artifact]:
        return MappingProxyType({a.artifact_id: a for a in self.artifacts})

    def validate(self) -> None:
        if not self.subjects:
            raise ValueError("catalog requires at least one synthetic subject")
        subject_ids = [s.canonical_id for s in self.subjects]
        if len(subject_ids) != len(set(subject_ids)):
            raise ValueError("duplicate subject canonical ids")
        aliases: dict[str, str] = {}
        for subject in self.subjects:
            if not subject.synthetic:
                raise ValueError(f"subject {subject.canonical_id} is not marked synthetic")
            for alias in subject.aliases:
                if alias in subject_ids:
                    raise ValueError(f"alias {alias} cannot collide with a canonical subject id")
                if alias in aliases:
                    raise ValueError(f"alias {alias} maps to multiple subjects")
                aliases[alias] = subject.canonical_id

        external_refs = [
            artifact.external_ref
            for artifact in self.artifacts
            if artifact.external_ref is not None
        ]
        if len(external_refs) != len(set(external_refs)):
            raise ValueError("duplicate published external references")

        artifact_ids = [a.artifact_id for a in self.artifacts]
        if len(artifact_ids) != len(set(artifact_ids)):
            raise ValueError("duplicate artifact ids")
        for artifact in self.artifacts:
            if not artifact.synthetic:
                raise ValueError(f"artifact {artifact.artifact_id} is not marked synthetic")
            if artifact.owner_subject_id and artifact.owner_subject_id not in subject_ids:
                raise ValueError(
                    f"artifact {artifact.artifact_id} has unknown owner "
                    f"{artifact.owner_subject_id}"
                )

        by_id = {a.artifact_id: a for a in self.artifacts}
        for artifact in self.artifacts:
            for source_id in artifact.source_ids:
                if source_id not in by_id:
                    raise ValueError(
                        f"artifact {artifact.artifact_id} references missing source {source_id}"
                    )
            if artifact.formula and artifact.formula not in {"count", "sum", "average"}:
                raise ValueError(f"unsupported formula {artifact.formula}")
            if artifact.formula == "count":
                for source_id in artifact.source_ids:
                    if by_id[source_id].kind is not ArtifactKind.RAW:
                        raise ValueError("count formula may only count raw records")

        self._assert_acyclic()

    def _assert_acyclic(self) -> None:
        visiting: set[str] = set()
        visited: set[str] = set()

        def visit(artifact_id: str) -> None:
            if artifact_id in visited:
                return
            if artifact_id in visiting:
                raise ValueError(f"lineage cycle at {artifact_id}")
            visiting.add(artifact_id)
            artifact = self.artifact_by_id[artifact_id]
            for source_id in artifact.source_ids:
                visit(source_id)
            visiting.remove(artifact_id)
            visited.add(artifact_id)

        for artifact in self.artifacts:
            visit(artifact.artifact_id)


@dataclass(frozen=True)
class PlannedAction:
    action: ActionKind
    artifact_id: str
    parameters: Mapping[str, Any] = field(default_factory=dict)
    external: bool = False

    def __post_init__(self) -> None:
        object.__setattr__(self, "parameters", _frozen_mapping(self.parameters))


@dataclass(frozen=True)
class DeletionPlan:
    requested_subject_ref: str
    canonical_subject_id: str
    explicit_alias_map: Mapping[str, str]
    lineage_closure: Mapping[str, tuple[str, ...]]
    actions: tuple[PlannedAction, ...]
    warnings: tuple[str, ...]
    dry_run: bool

    def __post_init__(self) -> None:
        object.__setattr__(
            self,
            "explicit_alias_map",
            MappingProxyType(dict(self.explicit_alias_map)),
        )
        object.__setattr__(
            self,
            "lineage_closure",
            MappingProxyType(
                {key: tuple(value) for key, value in self.lineage_closure.items()}
            ),
        )
        object.__setattr__(self, "actions", tuple(self.actions))
        object.__setattr__(self, "warnings", tuple(self.warnings))

    @property
    def external_action_count(self) -> int:
        return sum(1 for action in self.actions if action.external)
