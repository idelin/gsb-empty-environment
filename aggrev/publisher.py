"""Publisher side: owns the aggregated result and the revision DAG.

Multiple upstreams publish corrections against the versions they last saw, so
the version history is a DAG (branches when upstreams publish concurrently,
merges when a correction names several parents). Publication is atomic and
idempotent: re-publishing the same id with the same content is a no-op, with
different content it is a conflict.
"""
from __future__ import annotations

from .models import GENESIS_VERSION, Correction
from .store import AtomicStore


class ConflictError(Exception):
    pass


class UnknownParentsError(Exception):
    pass


def _initial_state() -> dict:
    return {
        "base": {},          # folded-in value of garbage-collected revisions
        "value": {},         # current aggregate per table
        "versions": {GENESIS_VERSION: {"revision_id": None, "parents": []}},
        "retired_versions": [],
        "revisions": {},
        "retired_revisions": {},
        "checkpoints": {},
        "acks": {},
    }


class Publisher:
    def __init__(self, store: AtomicStore | None = None):
        self.store = store or AtomicStore(initial=_initial_state())

    def value(self) -> dict[str, int]:
        return dict(self.store.state["value"])

    def base(self) -> dict[str, int]:
        return dict(self.store.state["base"])

    def checkpoints(self) -> dict[str, int]:
        return dict(self.store.state["checkpoints"])

    def heads(self) -> list[str]:
        versions = self.store.state["versions"]
        referenced = {p for v in versions.values() for p in v["parents"]}
        return sorted(vid for vid in versions if vid not in referenced)

    def publish(self, correction: Correction) -> str:
        fingerprint = correction.fingerprint()

        def mutate(state: dict) -> str:
            live = state["revisions"].get(correction.revision_id)
            existing = live["fingerprint"] if live else state["retired_revisions"].get(
                correction.revision_id
            )
            if existing is not None:
                if existing != fingerprint:
                    raise ConflictError(
                        f"{correction.revision_id}: same id, different content"
                    )
                return "duplicate"
            known = set(state["versions"]) | set(state["retired_versions"])
            missing = [p for p in correction.parents if p not in known]
            if missing:
                raise UnknownParentsError(
                    f"{correction.revision_id}: unknown parents {missing}"
                )
            vid = correction.version_id
            state["versions"][vid] = {
                "revision_id": correction.revision_id,
                "parents": list(correction.parents),
            }
            state["revisions"][correction.revision_id] = {
                "correction": correction.to_dict(),
                "version_id": vid,
                "fingerprint": fingerprint,
            }
            for table, delta in correction.deltas.items():
                state["value"][table] = state["value"].get(table, 0) + delta
            for source, seq in correction.checkpoints.items():
                state["checkpoints"][source] = max(
                    state["checkpoints"].get(source, 0), seq
                )
            return "published"

        return self.store.commit(mutate)

    def ack(self, downstream_id: str, version_ids, checkpoints: dict) -> None:
        """Record a downstream's confirmation; this is the only basis GC may use."""

        def mutate(state: dict) -> None:
            state["acks"][downstream_id] = {
                "versions": sorted(version_ids),
                "checkpoints": dict(checkpoints),
            }

        self.store.commit(mutate)
