"""A minimal idempotent downstream consumer used by the demo and tests."""
from __future__ import annotations


class DownstreamSink:
    """Applies published results and compensating deltas exactly once.

    Deduplication is keyed on the deterministic ``result_id``: applying the
    same record twice (e.g. a re-delivery after a crash between publish and
    ack) is a no-op, so deltas are idempotent.
    """

    def __init__(self) -> None:
        self.records: dict[str, dict] = {}
        self.total: float = 0.0
        self.apply_count: int = 0  # counts every delivery attempt, incl. dupes

    def apply(self, record: dict) -> bool:
        """Returns True if the record was new, False if it was a duplicate."""
        self.apply_count += 1
        result_id = record["result_id"]
        if result_id in self.records:
            return False
        self.records[result_id] = record
        if record["kind"] == "publish":
            self.total += record["value"]
        else:  # compensation
            self.total += record["delta"]
        return True
