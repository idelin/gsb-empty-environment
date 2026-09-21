"""Reconstruct build/terminal state from a stream of events.

Design notes
------------

* Events can be ingested in *any* order; state is derived, never mutated by
  arrival order.  Replaying the same events again is a no-op.
* Each ``(node, attempt)`` pair collects its own evidence.
* A ``failed`` event is terminal evidence for that attempt.
* A ``submitted`` event is only a terminal *success* when the attempt also
  has ``started`` plus every required artifact (each with a single,
  consistent sha256).  Otherwise the node stays ``pending`` and the missing
  evidence is reported -- success is never guessed.
* Each node's state is decided by its highest attempt number.  A late
  ``failed`` for an old attempt therefore cannot overwrite a newer attempt's
  success.
"""

from __future__ import annotations

from collections import defaultdict
from collections.abc import Iterable
from dataclasses import dataclass, field

from .events import (
    ARTIFACTS_WRITTEN,
    FAILED,
    STARTED,
    SUBMITTED,
    Event,
)
from .model import BuildDefinition, NodeSpec

SUCCESS = "success"
FAILED_STATE = "failed"
PENDING = "pending"
TERMINAL_STATES = frozenset({SUCCESS, FAILED_STATE})


@dataclass
class MissingEvidence:
    kind: str
    detail: str


@dataclass
class AttemptState:
    attempt: int
    state: str
    missing: tuple[MissingEvidence, ...] = ()
    failure_reason: str = ""


@dataclass
class NodeStatus:
    node: str
    state: str
    selected_attempt: int | None
    attempts: tuple[AttemptState, ...]
    missing: tuple[MissingEvidence, ...] = ()

    @property
    def is_terminal(self) -> bool:
        return self.state in TERMINAL_STATES


@dataclass
class BuildSnapshot:
    nodes: tuple[NodeStatus, ...]
    warnings: tuple[str, ...]

    @property
    def pending_nodes(self) -> tuple[NodeStatus, ...]:
        return tuple(node for node in self.nodes if node.state == PENDING)

    @property
    def failed_nodes(self) -> tuple[NodeStatus, ...]:
        return tuple(node for node in self.nodes if node.state == FAILED_STATE)

    @property
    def succeeded_nodes(self) -> tuple[NodeStatus, ...]:
        return tuple(node for node in self.nodes if node.state == SUCCESS)

    @property
    def is_complete(self) -> bool:
        return all(node.is_terminal for node in self.nodes)

    @property
    def is_success(self) -> bool:
        return self.is_complete and all(node.state == SUCCESS for node in self.nodes)


@dataclass
class _AttemptEvidence:
    started: bool = False
    submitted: bool = False
    failed: bool = False
    failure_reason: str = ""
    artifact_hashes: dict[str, set[str]] = field(default_factory=dict)


class BuildTracker:
    """Collects events and rebuilds node state on demand."""

    def __init__(self, definition: BuildDefinition):
        self.definition = definition
        self._events: dict[tuple[str, str, int, str], Event] = {}
        self.warnings: list[str] = []

    def ingest(self, event: Event) -> bool:
        """Add one event.

        Returns ``True`` if it was new, ``False`` if the identical event was
        already present (idempotent replay).  A repeated ``event_id`` with a
        different payload is rejected as a conflicting duplicate.
        """
        known_nodes = self.definition.node_map
        if event.node not in known_nodes:
            self.warnings.append(
                f"event {event.event_id!r} references unknown node "
                f"{event.node!r}; ignored"
            )
            return False

        key = event.identity
        existing = self._events.get(key)
        if existing is not None:
            if existing != event:
                self.warnings.append(
                    f"duplicate event_id {event.event_id!r} with conflicting "
                    f"payload; keeping the first occurrence"
                )
            return False
        self._events[key] = event
        return True

    def ingest_many(self, events: Iterable[Event]) -> int:
        return sum(1 for event in events if self.ingest(event))

    def snapshot(self) -> BuildSnapshot:
        by_node: dict[str, dict[int, _AttemptEvidence]] = defaultdict(
            lambda: defaultdict(_AttemptEvidence)
        )
        for event in self._events.values():
            evidence = by_node[event.node][event.attempt]
            if event.kind == STARTED:
                evidence.started = True
            elif event.kind == SUBMITTED:
                evidence.submitted = True
            elif event.kind == FAILED:
                evidence.failed = True
                evidence.failure_reason = event.reason
            elif event.kind == ARTIFACTS_WRITTEN:
                for artifact in event.artifacts:
                    evidence.artifact_hashes.setdefault(artifact.name, set()).add(
                        artifact.sha256
                    )

        extra_warnings: list[str] = []
        statuses = []
        for spec in self.definition.nodes:
            statuses.append(
                self._evaluate_node(spec, by_node.get(spec.name, {}), extra_warnings)
            )
        return BuildSnapshot(
            nodes=tuple(statuses),
            warnings=tuple(self.warnings) + tuple(extra_warnings),
        )

    def _evaluate_node(
        self,
        spec: NodeSpec,
        attempts: dict[int, _AttemptEvidence],
        extra_warnings: list[str],
    ) -> NodeStatus:
        if not attempts:
            return NodeStatus(
                node=spec.name,
                state=PENDING,
                selected_attempt=None,
                attempts=(),
                missing=(MissingEvidence("no_events", "no events received for node"),),
            )

        attempt_states = tuple(
            self._evaluate_attempt(number, evidence, spec, extra_warnings)
            for number, evidence in sorted(attempts.items())
        )
        latest = attempt_states[-1]
        return NodeStatus(
            node=spec.name,
            state=latest.state,
            selected_attempt=latest.attempt,
            attempts=attempt_states,
            missing=latest.missing,
        )

    def _evaluate_attempt(
        self,
        number: int,
        evidence: _AttemptEvidence,
        spec: NodeSpec,
        extra_warnings: list[str],
    ) -> AttemptState:
        if evidence.submitted and evidence.failed:
            return AttemptState(
                attempt=number,
                state=PENDING,
                missing=(
                    MissingEvidence(
                        "contradiction",
                        "attempt is both submitted and failed; "
                        "cannot determine terminal state",
                    ),
                ),
            )
        if evidence.failed:
            return AttemptState(
                attempt=number, state=FAILED_STATE, failure_reason=evidence.failure_reason
            )

        missing: list[MissingEvidence] = []
        if not evidence.started:
            missing.append(MissingEvidence(STARTED, "missing started event"))
        for name in spec.required_artifacts:
            hashes = evidence.artifact_hashes.get(name)
            if not hashes:
                missing.append(
                    MissingEvidence("artifact", f"missing artifact evidence: {name}")
                )
            elif len(hashes) > 1:
                missing.append(
                    MissingEvidence(
                        "artifact_conflict",
                        f"artifact {name} has conflicting sha256 values: "
                        + ", ".join(sorted(hashes)),
                    )
                )
        for name in evidence.artifact_hashes:
            if name not in spec.required_artifacts:
                extra_warnings.append(
                    f"node {spec.name} attempt {number} produced artifact "
                    f"{name!r} that is not required; ignored for success"
                )
        if not evidence.submitted:
            missing.append(MissingEvidence(SUBMITTED, "missing submitted event"))

        if missing:
            return AttemptState(attempt=number, state=PENDING, missing=tuple(missing))
        return AttemptState(attempt=number, state=SUCCESS)
