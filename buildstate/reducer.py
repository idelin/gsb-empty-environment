"""Fold an unordered, possibly duplicated event stream into node states.

Design rules
------------
* State is a pure function of the *set* of deduplicated events, so arrival
  order never matters and re-submitted events are idempotent.
* A node is judged by its highest attempt only: an old attempt's failure can
  never override a newer attempt's success (and vice versa).
* SUCCEEDED is terminal and requires complete evidence for that attempt:
  the success event, every required artifact, and at least one commit.
  A success event with missing evidence yields PENDING, never a guess.
* A failure and a success for the *same* attempt are contradictory; the
  attempt is conservatively treated as FAILED.
"""
from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any, Iterable

from .events import Event
from .nodes import NodeSpec

PENDING = "PENDING"
SUCCEEDED = "SUCCEEDED"
FAILED = "FAILED"


@dataclass
class _AttemptEvidence:
    started: bool = False
    artifacts: dict[str, str | None] = field(default_factory=dict)
    commits: set[str] = field(default_factory=set)
    succeeded: bool = False
    failed: bool = False
    fail_reasons: list[str] = field(default_factory=list)


@dataclass(frozen=True)
class NodeStatus:
    node_id: str
    status: str
    attempt: int | None
    reason: str
    evidence: dict[str, Any]
    missing: tuple[str, ...] = ()

    def to_dict(self) -> dict[str, Any]:
        return {
            "status": self.status,
            "attempt": self.attempt,
            "reason": self.reason,
            "evidence": self.evidence,
            "missing": list(self.missing),
        }


class BuildState:
    """Mutable accumulator; apply events, then query per-node status."""

    def __init__(self, specs: Iterable[NodeSpec]):
        self._specs = {spec.node_id: spec for spec in specs}
        self._attempts: dict[str, dict[int, _AttemptEvidence]] = {
            node_id: {} for node_id in self._specs
        }
        self._seen_event_ids: set[str] = set()
        self.duplicate_event_ids: list[str] = []
        self.warnings: list[str] = []

    def apply(self, event: Event) -> bool:
        """Apply one event.  Returns False for duplicates/unknown nodes."""
        if event.event_id in self._seen_event_ids:
            self.duplicate_event_ids.append(event.event_id)
            return False
        self._seen_event_ids.add(event.event_id)

        if event.node not in self._specs:
            self.warnings.append(
                f"event {event.event_id}: unknown node {event.node!r}; ignored"
            )
            return False

        attempt = self._attempts[event.node].setdefault(
            event.attempt, _AttemptEvidence()
        )
        if event.type == "node_started":
            attempt.started = True
        elif event.type == "artifact_written":
            artifact = event.payload["artifact"]
            digest = event.payload.get("sha256")
            if artifact in attempt.artifacts and attempt.artifacts[artifact] != digest:
                self.warnings.append(
                    f"event {event.event_id}: conflicting digest for "
                    f"{artifact!r} on {event.node} attempt {event.attempt}"
                )
            attempt.artifacts.setdefault(artifact, digest)
        elif event.type == "committed":
            attempt.commits.add(event.payload["commit"])
        elif event.type == "build_succeeded":
            attempt.succeeded = True
        elif event.type == "build_failed":
            attempt.failed = True
            reason = event.payload.get("reason")
            if isinstance(reason, str) and reason:
                attempt.fail_reasons.append(reason)
        return True

    def apply_all(self, events: Iterable[Event]) -> None:
        for event in events:
            self.apply(event)

    def node_status(self, node_id: str) -> NodeStatus:
        spec = self._specs[node_id]
        attempts = self._attempts[node_id]
        if not attempts:
            return NodeStatus(
                node_id=node_id,
                status=PENDING,
                attempt=None,
                reason="no events received",
                evidence={},
                missing=spec.required_artifacts,
            )

        latest = max(attempts)
        ev = attempts[latest]
        base_evidence: dict[str, Any] = {
            "started": ev.started,
            "artifacts": dict(sorted(ev.artifacts.items())),
            "commits": sorted(ev.commits),
        }

        if ev.failed:
            reason = "; ".join(ev.fail_reasons) or "build failed"
            if ev.succeeded:
                reason = f"contradictory success+failure for attempt {latest}; {reason}"
            return NodeStatus(
                node_id=node_id,
                status=FAILED,
                attempt=latest,
                reason=reason,
                evidence=base_evidence,
            )

        if ev.succeeded:
            missing = [a for a in spec.required_artifacts if a not in ev.artifacts]
            if not ev.commits:
                missing = missing + ["<commit>"]
            if missing:
                return NodeStatus(
                    node_id=node_id,
                    status=PENDING,
                    attempt=latest,
                    reason="success claimed but evidence incomplete",
                    evidence=base_evidence,
                    missing=tuple(missing),
                )
            return NodeStatus(
                node_id=node_id,
                status=SUCCEEDED,
                attempt=latest,
                reason="success with complete evidence",
                evidence=base_evidence,
            )

        return NodeStatus(
            node_id=node_id,
            status=PENDING,
            attempt=latest,
            reason="attempt in progress; no terminal event",
            evidence=base_evidence,
            missing=tuple(
                a for a in spec.required_artifacts if a not in ev.artifacts
            ),
        )

    def report(self) -> dict[str, Any]:
        statuses = {node_id: self.node_status(node_id) for node_id in self._specs}
        counts = {SUCCEEDED: 0, FAILED: 0, PENDING: 0}
        for st in statuses.values():
            counts[st.status] += 1
        return {
            "summary": {"total": len(statuses), **{k.lower(): v for k, v in counts.items()}},
            "nodes": {nid: st.to_dict() for nid, st in sorted(statuses.items())},
            "pending": sorted(nid for nid, st in statuses.items() if st.status == PENDING),
            "duplicates_ignored": sorted(self.duplicate_event_ids),
            "warnings": list(self.warnings),
        }
