"""Fold an out-of-order, possibly duplicated event log into build state.

The fold is deterministic and independent of ingestion order: duplicate
``event_id`` values are collapsed, events are replayed in
``(attempt, seq, event_id)`` order, only the latest attempt of each node can
decide its state, and a ``succeeded`` event is not enough on its own -- every
required artifact (matching package/version) plus the commit evidence must be
present, otherwise the node stays ``PENDING``.
"""

from __future__ import annotations

from dataclasses import dataclass, field
from enum import Enum

from .models import Artifact, BuildInput, Event


class NodeState(str, Enum):
    SUCCESS = "SUCCESS"
    FAILED = "FAILED"
    PENDING = "PENDING"


@dataclass(frozen=True)
class NodeStatus:
    node: str
    state: NodeState
    attempt: int | None
    missing_artifacts: tuple[str, ...] = ()
    missing_commit: bool = False
    reason: str | None = None
    observed_attempts: tuple[int, ...] = ()


@dataclass
class Anomaly:
    scope: str
    message: str
    event_id: str | None = None


@dataclass
class NodeFold:
    latest_attempt: int
    submitted: set[int] = field(default_factory=set)
    submitted_event_ids: set[str] = field(default_factory=set)
    artifacts: dict[str, Artifact] = field(default_factory=dict)
    commits: set[str] = field(default_factory=set)
    terminal_seq: dict[str, int] = field(default_factory=dict)  # kind -> seq
    failure_reason: str | None = None
    observed_attempts: set[int] = field(default_factory=set)


class BuildTracker:
    def __init__(self, build_input: BuildInput):
        self.build_input = build_input
        self._events: dict[str, Event] = {}
        self.duplicate_event_ids: list[str] = []
        self.orphan_events: list[Event] = []
        self.anomalies: list[Anomaly] = []

    # ------------------------------------------------------------------ ingest
    def ingest(self, events) -> None:
        """Accept events in any order; repeated ``event_id`` is idempotent."""
        for event in events:
            existing = self._events.get(event.event_id)
            if existing is not None:
                self.duplicate_event_ids.append(event.event_id)
                continue
            self._events[event.event_id] = event

    @property
    def ingested_count(self) -> int:
        return len(self._events)

    # ------------------------------------------------------------------- fold
    def reconstruct(self) -> dict[str, NodeStatus]:
        self.anomalies = []
        self.orphan_events = []
        per_node: dict[str, list[Event]] = {node: [] for node in self.build_input.nodes}
        for event in self._events.values():
            if event.node not in per_node:
                self.orphan_events.append(event)
                self.anomalies.append(
                    Anomaly(
                        scope=event.node,
                        message=(
                            f"event {event.event_id!r} references undeclared node "
                            f"{event.node!r}; ignored"
                        ),
                        event_id=event.event_id,
                    )
                )
                continue
            per_node[event.node].append(event)

        statuses: dict[str, NodeStatus] = {}
        for node, events in per_node.items():
            statuses[node] = self._fold_node(node, events)
        return statuses

    def _fold_node(self, node: str, events: list[Event]) -> NodeStatus:
        spec = self.build_input.nodes[node]
        ordered = sorted(events, key=lambda e: (e.attempt, e.seq, e.event_id))
        observed = sorted({e.attempt for e in ordered})
        if not ordered:
            return NodeStatus(node=node, state=NodeState.PENDING, attempt=None,
                              missing_artifacts=spec.required_artifacts,
                              missing_commit=spec.require_commit)

        latest = max(e.attempt for e in ordered)
        fold = NodeFold(latest_attempt=latest, observed_attempts=set(observed))

        for event in ordered:
            fold.observed_attempts.add(event.attempt)
            # Events from superseded attempts can never decide the new attempt.
            if event.attempt < latest:
                continue
            self._apply_event(node, spec, event, fold)

        return self._decide(node, spec, fold)

    def _apply_event(self, node, spec, event: Event, fold: NodeFold) -> None:
        if event.kind == "submitted":
            # Re-submission of the same attempt is idempotent: never resets.
            if event.attempt in fold.submitted:
                self.anomalies.append(
                    Anomaly(
                        scope=node,
                        message=(
                            f"event {event.event_id!r}: duplicate submission for attempt "
                            f"{event.attempt} (already submitted by "
                            f"{sorted(fold.submitted_event_ids)!r}); state kept"
                        ),
                        event_id=event.event_id,
                    )
                )
            fold.submitted.add(event.attempt)
            fold.submitted_event_ids.add(event.event_id)
            return

        for artifact in event.artifacts:
            self._accept_artifact(node, spec, event, artifact, fold)

        if event.commit:
            if fold.commits and event.commit not in fold.commits:
                self.anomalies.append(
                    Anomaly(
                        scope=node,
                        message=(
                            f"event {event.event_id!r}: conflicting commit "
                            f"{event.commit!r} (already have {sorted(fold.commits)!r}); "
                            "keeping the first commit"
                        ),
                        event_id=event.event_id,
                    )
                )
            else:
                fold.commits.add(event.commit)

        if event.kind in ("succeeded", "failed"):
            prior_seq = fold.terminal_seq.get(event.kind)
            if prior_seq is not None and prior_seq != event.seq:
                # Same terminal kind at another seq: later seq wins; not an error.
                pass
            other = "failed" if event.kind == "succeeded" else "succeeded"
            if other in fold.terminal_seq and fold.terminal_seq[other] == event.seq:
                self.anomalies.append(
                    Anomaly(
                        scope=node,
                        message=(
                            f"event {event.event_id!r}: 'succeeded' and 'failed' share "
                            f"seq {event.seq}; terminal state is ambiguous"
                        ),
                        event_id=event.event_id,
                    )
                )
            fold.terminal_seq[event.kind] = event.seq
            if event.kind == "failed":
                # Events replay in ascending seq; the last failure wins.
                fold.failure_reason = event.reason

    def _accept_artifact(self, node, spec, event: Event, artifact: Artifact,
                         fold: NodeFold) -> None:
        label = f"artifact {artifact.name!r}"
        if artifact.name not in spec.required_artifacts:
            self.anomalies.append(
                Anomaly(
                    scope=node,
                    message=(
                        f"event {event.event_id!r}: {label} is not in required artifacts "
                        f"{list(spec.required_artifacts)!r}; not accepted as evidence"
                    ),
                    event_id=event.event_id,
                )
            )
            return
        if artifact.package != spec.package or artifact.version != spec.version:
            self.anomalies.append(
                Anomaly(
                    scope=node,
                    message=(
                        f"event {event.event_id!r}: {label} is "
                        f"{artifact.package}@{artifact.version} but build input requires "
                        f"{spec.package}@{spec.version}; not accepted as evidence"
                    ),
                    event_id=event.event_id,
                )
            )
            return
        existing = fold.artifacts.get(artifact.name)
        if existing is not None and existing.sha256 != artifact.sha256:
            self.anomalies.append(
                Anomaly(
                    scope=node,
                    message=(
                        f"event {event.event_id!r}: {label} sha256 {artifact.sha256!r} "
                        f"conflicts with previously written {existing.sha256!r}; "
                        "keeping the first evidence"
                    ),
                    event_id=event.event_id,
                )
            )
            return
        fold.artifacts[artifact.name] = artifact

    def _decide(self, node, spec, fold: NodeFold) -> NodeStatus:
        success_seq = fold.terminal_seq.get("succeeded")
        failure_seq = fold.terminal_seq.get("failed")
        ambiguous = success_seq is not None and success_seq == failure_seq

        if ambiguous:
            kind = NodeState.PENDING
            reason = "ambiguous terminal events at the same seq"
        elif failure_seq is not None and (
            success_seq is None or failure_seq > success_seq
        ):
            kind = NodeState.FAILED
            reason = fold.failure_reason
        elif success_seq is not None and (
            failure_seq is None or success_seq > failure_seq
        ):
            missing = tuple(
                name for name in spec.required_artifacts if name not in fold.artifacts
            )
            missing_commit = spec.require_commit and not fold.commits
            if missing or missing_commit:
                # Success claimed but evidence incomplete: undecided, not success.
                kind = NodeState.PENDING
                reason = "succeeded event without complete required evidence"
                return NodeStatus(
                    node=node,
                    state=kind,
                    attempt=fold.latest_attempt,
                    missing_artifacts=missing,
                    missing_commit=missing_commit,
                    reason=reason,
                    observed_attempts=tuple(sorted(fold.observed_attempts)),
                )
            kind = NodeState.SUCCESS
            reason = None
        else:
            kind = NodeState.PENDING
            reason = None

        if kind == NodeState.PENDING:
            missing = tuple(
                name for name in spec.required_artifacts if name not in fold.artifacts
            )
            missing_commit = spec.require_commit and not fold.commits
        else:
            missing = ()
            missing_commit = False
        return NodeStatus(
            node=node,
            state=kind,
            attempt=fold.latest_attempt,
            missing_artifacts=missing,
            missing_commit=missing_commit,
            reason=reason,
            observed_attempts=tuple(sorted(fold.observed_attempts)),
        )

    # ------------------------------------------------------------------ report
    def as_report(self, statuses: dict[str, NodeStatus]) -> dict:
        def node_payload(status: NodeStatus) -> dict:
            return {
                "state": status.state.value,
                "attempt": status.attempt,
                "observed_attempts": list(status.observed_attempts),
                "missing_artifacts": list(status.missing_artifacts),
                "missing_commit": status.missing_commit,
                "reason": status.reason,
            }

        return {
            "nodes": {node: node_payload(status) for node, status in statuses.items()},
            "unresolved_nodes": sorted(
                node for node, status in statuses.items()
                if status.state == NodeState.PENDING
            ),
            "counts": {
                "success": sum(s.state == NodeState.SUCCESS for s in statuses.values()),
                "failed": sum(s.state == NodeState.FAILED for s in statuses.values()),
                "pending": sum(s.state == NodeState.PENDING for s in statuses.values()),
            },
            "duplicate_event_ids": list(self.duplicate_event_ids),
            "orphan_events": [e.event_id for e in self.orphan_events],
            "anomalies": [
                {"scope": a.scope, "event_id": a.event_id, "message": a.message}
                for a in self.anomalies
            ],
        }
