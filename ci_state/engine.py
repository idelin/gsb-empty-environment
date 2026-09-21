"""Build-state reconstruction engine.

The engine folds an unordered, potentially duplicated stream of build events
into per-attempt evidence and derives node states WITHOUT guessing:

* ``SUCCESS``  - started + succeeded + every required artifact both written
                 and committed. Complete evidence is mandatory; a success
                 event alone is never enough.
* ``FAILED``   - a failure event was received for the attempt.
* ``PENDING``  - anything else (never started, still running, success claimed
                 but an artifact write/commit is missing, ...). The report
                 lists exactly which evidence is missing.

Attempts never interfere destructively: the highest attempt number is
authoritative for a node, so a late event for an old attempt (e.g. an old
failure arriving after a newer success) cannot overwrite the newer state.

Applying the same event twice (matched by ``id``) is a no-op, so processing
is idempotent and order-independent.
"""

from __future__ import annotations

from dataclasses import dataclass, field
from enum import Enum
from typing import Dict, FrozenSet, List, Optional

from .events import (
    ARTIFACT_COMMITTED,
    ARTIFACT_WRITTEN,
    NODE_FAILED,
    NODE_STARTED,
    NODE_SUCCEEDED,
    Event,
)


class NodeState(str, Enum):
    PENDING = "PENDING"
    SUCCESS = "SUCCESS"
    FAILED = "FAILED"


@dataclass
class AttemptState:
    """Evidence collected for one (node, attempt) pair."""

    node: str
    attempt: int
    started: bool = False
    succeeded: bool = False
    failed: bool = False
    fail_reason: Optional[str] = None
    written: Dict[str, str] = field(default_factory=dict)
    committed: Dict[str, str] = field(default_factory=dict)
    event_ids: List[str] = field(default_factory=list)

    def missing_evidence(self, required: FrozenSet[str]) -> List[str]:
        """Human-readable list of evidence still required for SUCCESS."""
        gaps: List[str] = []
        if not self.started:
            gaps.append("missing node.started event")
        if not self.succeeded:
            gaps.append("missing node.succeeded event")
        for name in sorted(required):
            if name not in self.written:
                gaps.append(f"artifact {name!r}: missing artifact.written event")
            if name not in self.committed:
                gaps.append(f"artifact {name!r}: missing artifact.committed event")
        return gaps


@dataclass
class NodeReport:
    node: str
    attempt: int
    state: NodeState
    missing: List[str]
    fail_reason: Optional[str]
    required: List[str]
    written: List[str]
    committed: List[str]
    event_ids: List[str]
    observed_attempts: List[int]

    def to_dict(self) -> dict:
        return {
            "node": self.node,
            "attempt": self.attempt,
            "state": self.state.value,
            "required_artifacts": self.required,
            "evidence": {
                "written": self.written,
                "committed": self.committed,
                "event_ids": self.event_ids,
            },
            "observed_attempts": self.observed_attempts,
            "missing": self.missing,
            "fail_reason": self.fail_reason,
        }


class BuildEngine:
    """Fold build events into evidence-based node states."""

    def __init__(self, nodes: Dict[str, FrozenSet[str]]) -> None:
        """``nodes`` maps node name to the set of required artifact names."""
        if not isinstance(nodes, dict) or not all(
            isinstance(name, str)
            and isinstance(arts, frozenset)
            and all(isinstance(a, str) for a in arts)
            for name, arts in nodes.items()
        ):
            raise ValueError("nodes must be {str: frozenset[str]}")
        self._required: Dict[str, FrozenSet[str]] = dict(nodes)
        self._attempts: Dict[tuple, AttemptState] = {}
        self._seen_ids: Dict[str, Event] = {}
        self.duplicate_events: List[str] = []

    @property
    def nodes(self) -> List[str]:
        return sorted(self._required)

    def apply(self, event: Event) -> bool:
        """Apply one event. Returns False for an identical duplicate.

        A duplicate id carrying a different payload raises ``ValueError``
        because the two contradictory copies cannot both be authoritative.
        """
        if event.node not in self._required:
            raise ValueError(f"event {event.id!r}: unknown node {event.node!r}")
        prior = self._seen_ids.get(event.id)
        if prior is not None:
            if prior != event:
                raise ValueError(
                    f"event id {event.id!r} reused with a different payload"
                )
            self.duplicate_events.append(event.id)
            return False
        self._seen_ids[event.id] = event

        state = self._attempts.setdefault(
            (event.node, event.attempt),
            AttemptState(node=event.node, attempt=event.attempt),
        )
        state.event_ids.append(event.id)
        if event.kind == NODE_STARTED:
            state.started = True
        elif event.kind == NODE_SUCCEEDED:
            state.succeeded = True
        elif event.kind == NODE_FAILED:
            state.failed = True
            if state.fail_reason is None:
                state.fail_reason = event.reason or "unspecified failure"
        elif event.kind == ARTIFACT_WRITTEN:
            for name in event.artifacts:
                state.written.setdefault(name, event.id)
        elif event.kind == ARTIFACT_COMMITTED:
            for name in event.artifacts:
                state.committed.setdefault(name, event.id)
        return True

    def apply_many(self, events) -> int:
        """Apply an iterable of events in ANY order; returns new events applied."""
        return sum(1 for event in events if self.apply(event))

    def _attempt_state(self, state: AttemptState) -> NodeState:
        required = self._required[state.node]
        if state.failed:
            return NodeState.FAILED
        if state.started and state.succeeded and not state.missing_evidence(required):
            return NodeState.SUCCESS
        return NodeState.PENDING

    def report_node(self, node: str) -> NodeReport:
        if node not in self._required:
            raise KeyError(node)
        attempts = sorted(
            (a for (n, _no), a in self._attempts.items() if n == node),
            key=lambda a: a.attempt,
        )
        observed = [a.attempt for a in attempts]
        required = self._required[node]
        if not attempts:
            return NodeReport(
                node=node,
                attempt=0,
                state=NodeState.PENDING,
                missing=["no events observed for node"],
                fail_reason=None,
                required=sorted(required),
                written=[],
                committed=[],
                event_ids=[],
                observed_attempts=[],
            )
        latest = attempts[-1]
        status = self._attempt_state(latest)
        missing: List[str] = []
        if status is NodeState.PENDING:
            missing = latest.missing_evidence(required)
        return NodeReport(
            node=node,
            attempt=latest.attempt,
            state=status,
            missing=missing,
            fail_reason=latest.fail_reason if status is NodeState.FAILED else None,
            required=sorted(required),
            written=sorted(latest.written),
            committed=sorted(latest.committed),
            event_ids=list(latest.event_ids),
            observed_attempts=observed,
        )

    def report(self) -> List[NodeReport]:
        return [self.report_node(node) for node in self.nodes]

    def undecided_nodes(self) -> List[NodeReport]:
        """Nodes without a proven terminal state - never guessed as success."""
        return [r for r in self.report() if r.state is NodeState.PENDING]
