"""Lineage-based erasure / recompute planner (synthetic data only).

This module builds deletion or recompute plans from a subject-to-artifact
lineage graph. It distinguishes:

  - ORIGINAL  : original records ingested from a subject
  - CACHE     : regenerable intermediate artifacts
  - DERIVED   : derived artifacts (aggregates, features, models)
  - PUBLISHED : statistics already released externally

Scope and disclaimers
---------------------
* Only synthetic data is handled. Nothing here is a compliance product.
* Aggregate/statistical experiments produced by this tool are NOT formal
  privacy proofs (no differential-privacy guarantee is claimed or implied).
* Published artifacts cannot be "taken back" from external parties; the
  planner only flags them and may optionally plan a corrective re-publication.
* Shared artifacts (more than one contributing subject) are never deleted;
  they are recomputed without the erased subject's contribution, or flagged
  for manual review when the lineage is insufficient to recompute.
"""

from __future__ import annotations

import enum
from dataclasses import dataclass, field
from typing import Any, Callable, Dict, Iterable, List, Optional, Tuple


class ArtifactKind(enum.Enum):
    ORIGINAL = "original"
    CACHE = "cache"
    DERIVED = "derived"
    PUBLISHED = "published"


class Action(enum.Enum):
    DELETE = "delete"                            # physically remove the artifact
    RECOMPUTE = "recompute"                      # rebuild without the subject
    MARK_UNRETRACTABLE = "mark_unretractable"    # published: cannot recall externally
    REPUBLISH_CORRECTION = "republish_correction"  # optional new corrected release
    MANUAL_REVIEW = "manual_review"              # lineage insufficient to act safely


class UnknownSubjectError(KeyError):
    """Raised when a subject reference cannot be resolved via the alias map."""


class LineageError(ValueError):
    """Raised for malformed lineage declarations."""


@dataclass(frozen=True)
class Subject:
    """A data subject with an explicit alias map.

    Aliases must be declared up front; lookups of unmapped identifiers fail
    loudly instead of silently matching nothing.
    """

    subject_id: str
    aliases: Tuple[str, ...] = ()


@dataclass
class Artifact:
    """A data artifact in the lineage graph.

    sources: identifiers of subjects (or aliases) and/or upstream artifacts
             this artifact was derived from.
    contributions: optional per-subject payloads enabling exact recompute
                   (e.g. partial sums). Required for safe recompute of
                   shared artifacts.
    aggregator: combines remaining per-subject payloads on recompute.
    data: opaque current payload (synthetic).
    """

    artifact_id: str
    kind: ArtifactKind
    sources: Tuple[str, ...] = ()
    data: Any = None
    contributions: Optional[Dict[str, Any]] = None
    aggregator: Optional[Callable[[Iterable[Any]], Any]] = None


@dataclass(frozen=True)
class PlanStep:
    action: Action
    artifact_id: str
    reason: str
    details: Tuple[Tuple[str, str], ...] = ()


@dataclass
class Plan:
    """A read-only plan. Constructing it never mutates the store."""

    subject_id: str
    matched_aliases: Tuple[str, ...]
    steps: Tuple[PlanStep, ...]
    warnings: Tuple[str, ...] = ()
    dry_run: bool = True

    def actions_for(self, artifact_id: str) -> List[PlanStep]:
        return [s for s in self.steps if s.artifact_id == artifact_id]


@dataclass
class ExecutionReport:
    applied: List[PlanStep] = field(default_factory=list)
    notes: List[str] = field(default_factory=list)


class LineageGraph:
    """Subjects, artifacts, and derived-from edges."""

    def __init__(self) -> None:
        self._subjects: Dict[str, Subject] = {}
        self._alias_to_id: Dict[str, str] = {}
        self._artifacts: Dict[str, Artifact] = {}

    # -- registration ------------------------------------------------------

    def add_subject(self, subject_id: str, aliases: Iterable[str] = ()) -> None:
        aliases = tuple(aliases)
        if subject_id in self._alias_to_id:
            raise LineageError(f"subject id {subject_id!r} collides with an alias")
        for alias in aliases:
            if alias in self._alias_to_id or alias in self._subjects:
                raise LineageError(f"alias {alias!r} is already mapped")
        self._subjects[subject_id] = Subject(subject_id, aliases)
        for alias in aliases:
            self._alias_to_id[alias] = subject_id

    def resolve(self, ref: str) -> str:
        """Map an id or alias to the canonical subject id. Fails loudly."""
        if ref in self._subjects:
            return ref
        if ref in self._alias_to_id:
            return self._alias_to_id[ref]
        raise UnknownSubjectError(
            f"{ref!r} is not a known subject id or mapped alias; "
            "declare it explicitly via add_subject(..., aliases=[...])"
        )

    def add_artifact(self, artifact: Artifact) -> None:
        if artifact.artifact_id in self._artifacts:
            raise LineageError(f"duplicate artifact {artifact.artifact_id!r}")
        # Normalize subject sources through the alias map; artifact sources
        # must reference already-registered artifacts.
        normalized: List[str] = []
        for src in artifact.sources:
            if src in self._artifacts:
                normalized.append(src)
            else:
                normalized.append(self.resolve(src))
        artifact.sources = tuple(normalized)
        if artifact.contributions is not None:
            artifact.contributions = {
                self.resolve(k): v for k, v in artifact.contributions.items()
            }
        self._artifacts[artifact.artifact_id] = artifact

    # -- queries ------------------------------------------------------------

    def artifact(self, artifact_id: str) -> Artifact:
        return self._artifacts[artifact_id]

    def artifacts(self) -> List[Artifact]:
        return list(self._artifacts.values())

    def contributing_subjects(self, artifact_id: str) -> frozenset:
        """All subjects transitively contributing to an artifact."""
        seen: set = set()

        def walk(aid: str) -> None:
            art = self._artifacts[aid]
            for src in art.sources:
                if src in self._artifacts:
                    walk(src)
                else:
                    seen.add(src)

        walk(artifact_id)
        return frozenset(seen)

    def downstream_of(self, subject_id: str) -> List[str]:
        """Artifacts transitively derived from the subject, upstream first."""
        result: List[str] = []
        visited: set = set()

        def visit(aid: str) -> None:
            if aid in visited:
                return
            visited.add(aid)
            result.append(aid)
            for other in self._artifacts.values():
                if aid in other.sources:
                    visit(other.artifact_id)

        for art in self._artifacts.values():
            if subject_id in art.sources:
                visit(art.artifact_id)
        return result


class Planner:
    """Generates erasure plans from a lineage graph."""

    def __init__(self, graph: LineageGraph) -> None:
        self.graph = graph

    def plan_erasure(
        self,
        subject_ref: str,
        *,
        republish_correction: bool = False,
    ) -> Plan:
        """Build a read-only plan. Never mutates the graph or any store.

        subject_ref may be a canonical id or any declared alias.
        republish_correction adds a corrective re-publication step for
        PUBLISHED artifacts; it never implies external copies are retracted.
        """
        subject_id = self.graph.resolve(subject_ref)
        subject = self.graph._subjects[subject_id]
        steps: List[PlanStep] = []
        warnings: List[str] = []

        for aid in self.graph.downstream_of(subject_id):
            art = self.graph.artifact(aid)
            contributors = self.graph.contributing_subjects(aid)
            others = contributors - {subject_id}
            shared = bool(others)

            if art.kind is ArtifactKind.PUBLISHED:
                steps.append(PlanStep(
                    Action.MARK_UNRETRACTABLE, aid,
                    "already published; external copies may persist and cannot "
                    "be recalled",
                    (("contributors", ",".join(sorted(contributors))),),
                ))
                warnings.append(
                    f"{aid}: published statistic cannot be retracted from "
                    "external parties; only internal records are updated"
                )
                if republish_correction:
                    steps.append(PlanStep(
                        Action.REPUBLISH_CORRECTION, aid,
                        "issue a corrected re-publication computed without the "
                        "subject; this is a new release, not a retraction",
                    ))
                continue

            if not shared:
                steps.append(PlanStep(
                    Action.DELETE, aid,
                    f"{art.kind.value} artifact solely derived from subject "
                    f"{subject_id}",
                ))
                continue

            # Shared artifact: never delete (that would destroy other
            # subjects' data); recompute without the erased subject instead.
            if art.contributions is not None and art.aggregator is not None:
                steps.append(PlanStep(
                    Action.RECOMPUTE, aid,
                    f"shared artifact; recompute excluding subject "
                    f"{subject_id}, keeping data of {len(others)} other "
                    "subject(s)",
                    (("other_contributors", ",".join(sorted(others))),),
                ))
            else:
                steps.append(PlanStep(
                    Action.MANUAL_REVIEW, aid,
                    "shared artifact lacks per-subject contributions or an "
                    "aggregator; cannot recompute safely without risking "
                    "other subjects' data",
                    (("other_contributors", ",".join(sorted(others))),),
                ))
                warnings.append(
                    f"{aid}: manual review required; insufficient lineage "
                    "for safe recompute"
                )

        return Plan(
            subject_id=subject_id,
            matched_aliases=subject.aliases,
            steps=tuple(steps),
            warnings=tuple(warnings),
            dry_run=True,
        )


def execute_plan(
    plan: Plan,
    graph: LineageGraph,
    store: Dict[str, Any],
) -> ExecutionReport:
    """Apply a plan to an in-memory store of synthetic artifact payloads.

    The store maps artifact_id -> payload. DELETE removes the key; RECOMPUTE
    rebuilds the payload from the remaining per-subject contributions; all
    other actions only record notes. Published artifacts are never modified.
    """
    report = ExecutionReport()
    for step in plan.steps:
        art = graph.artifact(step.artifact_id)
        if step.action is Action.DELETE:
            store.pop(step.artifact_id, None)
            report.applied.append(step)
        elif step.action is Action.RECOMPUTE:
            remaining = {
                sid: payload
                for sid, payload in (art.contributions or {}).items()
                if sid != plan.subject_id
            }
            store[step.artifact_id] = art.aggregator(remaining.values())
            art.contributions = remaining
            report.applied.append(step)
        elif step.action is Action.MARK_UNRETRACTABLE:
            report.notes.append(
                f"{step.artifact_id}: flagged as published/unretractable; "
                "no external recall is claimed"
            )
        elif step.action is Action.REPUBLISH_CORRECTION:
            report.notes.append(
                f"{step.artifact_id}: corrective re-publication planned as a "
                "new release; external copies of the original may persist"
            )
        elif step.action is Action.MANUAL_REVIEW:
            report.notes.append(
                f"{step.artifact_id}: left untouched pending manual review"
            )
    return report
