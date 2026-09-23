"""Downstream: applies corrections along the version DAG.

Rules implemented here:
- missing intermediate versions -> pause (park), never misapply;
- gap waiting and conflict detection are separate components;
- applying a revision, its acknowledgement and the checkpoint advance happen
  in one atomic commit;
- same id re-delivered -> no double apply; same id different content ->
  conflict; dependency cycles -> rejected (with cascade to dependents);
- corrections carry deltas, so an old-base correction can never overwrite a
  newer result, and checkpoints only move forward.
"""
from __future__ import annotations

from dataclasses import dataclass, field

from .conflict_detector import ConflictDetector
from .gap_tracker import GapTracker
from .models import GENESIS_VERSION, Correction, version_id_for
from .store import AtomicStore

APPLIED = "applied"
DUPLICATE = "duplicate"
WAITING = "waiting"
CONFLICT = "conflict"
REJECTED = "rejected"


@dataclass
class ReceiveResult:
    status: str
    revision_id: str
    detail: str = ""
    resumed: list[str] = field(default_factory=list)


def _initial_state() -> dict:
    return {
        "value": {},
        "versions": {GENESIS_VERSION: {"revision_id": None, "parents": []}},
        "applied": {},     # revision id -> fingerprint
        "acks": {},        # revision id -> {version_id, checkpoints}
        "checkpoints": {},  # source -> highest applied commit seq
        "conflicts": [],
        "rejected": {},    # revision id -> reason
    }


class Downstream:
    def __init__(self, downstream_id: str, store: AtomicStore | None = None):
        self.downstream_id = downstream_id
        self.store = store or AtomicStore(initial=_initial_state())
        self.gaps = GapTracker()
        self.conflict_detector = ConflictDetector()

    # ---- read views -----------------------------------------------------
    def value(self) -> dict[str, int]:
        return dict(self.store.state["value"])

    def checkpoints(self) -> dict[str, int]:
        return dict(self.store.state["checkpoints"])

    def applied(self) -> set[str]:
        return set(self.store.state["applied"])

    def rejected(self) -> dict[str, str]:
        return dict(self.store.state["rejected"])

    def conflicts(self) -> list[dict]:
        return list(self.store.state["conflicts"])

    def acked_versions(self) -> list[str]:
        return sorted(self.store.state["versions"])

    def waiting(self) -> list[str]:
        return sorted(rid for rid, _ in self.gaps.items())

    # ---- main entry ------------------------------------------------------
    def receive(self, correction: Correction) -> ReceiveResult:
        rid = correction.revision_id
        fingerprint = correction.fingerprint()
        state = self.store.state

        if rid in state["applied"]:
            if state["applied"][rid] == fingerprint:
                return ReceiveResult(DUPLICATE, rid, "identical revision already applied")
            self._record_conflict(correction, "same revision id, different content")
            return ReceiveResult(CONFLICT, rid, "fingerprint mismatch")
        if rid in state["rejected"]:
            return ReceiveResult(REJECTED, rid, state["rejected"][rid])

        missing = GapTracker.missing_parents(correction, set(state["versions"]))
        if missing:
            rejected_versions = {version_id_for(r) for r in state["rejected"]}
            if any(p in rejected_versions for p in missing):
                reason = "depends on a rejected revision"
                self._reject(rid, reason)
                return ReceiveResult(REJECTED, rid, reason)
            self.gaps.park(correction)
            for cycle in self.conflict_detector.find_cycles(
                self.gaps.waiting_corrections()
            ):
                for member in cycle:
                    self._reject(member, "cyclic dependency")
            return ReceiveResult(WAITING, rid, f"missing parents: {missing}")

        resumed = self._apply_and_drain(correction)
        return ReceiveResult(APPLIED, rid, resumed=resumed)

    # ---- internals -------------------------------------------------------
    def _apply_and_drain(self, correction: Correction) -> list[str]:
        self._apply(correction)
        resumed: list[str] = []
        while True:
            state = self.store.state
            known = set(state["versions"])
            rejected_versions = {version_id_for(r) for r in state["rejected"]}
            progressed = False
            for rid, parked in sorted(self.gaps.items()):
                missing = GapTracker.missing_parents(parked, known)
                if not missing:
                    self.gaps.unpark(rid)
                    self._apply(parked)
                    resumed.append(rid)
                    progressed = True
                elif any(p in rejected_versions for p in missing):
                    self.gaps.unpark(rid)
                    self._reject(rid, "depends on a rejected revision")
                    progressed = True
            if not progressed:
                return resumed

    def _apply(self, correction: Correction) -> None:
        rid = correction.revision_id
        fingerprint = correction.fingerprint()

        def mutate(state: dict) -> None:
            vid = correction.version_id
            state["versions"][vid] = {
                "revision_id": rid,
                "parents": list(correction.parents),
            }
            state["applied"][rid] = fingerprint
            for table, delta in correction.deltas.items():
                state["value"][table] = state["value"].get(table, 0) + delta
            # revision application + acknowledgement + checkpoint: one commit
            state["acks"][rid] = {
                "version_id": vid,
                "checkpoints": dict(correction.checkpoints),
            }
            for source, seq in correction.checkpoints.items():
                state["checkpoints"][source] = max(
                    state["checkpoints"].get(source, 0), seq
                )

        self.store.commit(mutate)

    def _record_conflict(self, correction: Correction, reason: str) -> None:
        def mutate(state: dict) -> None:
            state["conflicts"].append(
                {
                    "revision_id": correction.revision_id,
                    "reason": reason,
                    "fingerprint": correction.fingerprint(),
                }
            )

        self.store.commit(mutate)

    def _reject(self, revision_id: str, reason: str) -> None:
        self.gaps.unpark(revision_id)

        def mutate(state: dict) -> None:
            state["rejected"][revision_id] = reason

        self.store.commit(mutate)
