"""Core record model and the deterministic resolution rule.

Two timelines are tracked on every record:

* valid time  -- when the fact was true in the real world ("10时发生了什么")
* transaction time -- when the fact was recorded into the system
  ("当时我们知道什么")

All records are immutable and append-only: corrections, retractions and
backfills are *new* records; nothing is ever updated or deleted in place.
"""

from __future__ import annotations

import enum
import math
from dataclasses import dataclass
from typing import Any

#: Sentinel for an open-ended valid interval: [valid_from, +inf).
OPEN_ENDED = math.inf


class Kind(enum.Enum):
    ASSERT = "assert"      # initial assertion of a value
    CORRECT = "correct"    # correction of previously asserted knowledge
    RETRACT = "retract"    # retroactive retraction: "there was no value here"


@dataclass(frozen=True)
class VersionRecord:
    """One immutable version of knowledge about an object.

    Intervals are left-closed, right-open: [valid_from, valid_to).

    ``recorded_at`` is the transaction time at which this record became
    visible.  A record is visible to a query with observation time
    ``as_of`` iff ``recorded_at <= as_of``.
    """

    record_id: int
    object_id: str
    valid_from: float
    valid_to: float          # exclusive; use OPEN_ENDED for open-ended
    recorded_at: float
    kind: Kind
    value: Any = None        # must be None for RETRACT
    priority: int = 0        # explicit tie-break, see resolution_key

    def __post_init__(self) -> None:
        if not self.valid_from < self.valid_to:
            raise ValueError(
                f"empty/invalid interval [{self.valid_from}, {self.valid_to})"
            )
        if self.kind is Kind.RETRACT and self.value is not None:
            raise ValueError("RETRACT records must carry value=None")

    def covers(self, valid_time: float) -> bool:
        return self.valid_from <= valid_time < self.valid_to

    def visible_at(self, as_of: float) -> bool:
        return self.recorded_at <= as_of


def resolution_key(rec: VersionRecord) -> tuple:
    """Deterministic priority order for overlapping records of one object.

    Among all records that cover the queried valid time and are visible at
    the queried observation time, the winner is the maximum under this key:

    1. ``recorded_at`` -- later knowledge supersedes earlier knowledge;
    2. ``priority``    -- explicit, caller-declared tie-break for records
       ingested at the same transaction time (e.g. one batch);
    3. ``record_id``   -- unique, monotonically assigned id as the final
       deterministic tie-break.

    The rule depends only on record contents, never on arrival or
    iteration order: inserting the same set of records in any order yields
    identical query results.
    """
    return (rec.recorded_at, rec.priority, rec.record_id)


def resolve(candidates: list[VersionRecord]) -> VersionRecord | None:
    """Pick the winning record; None if the winner is a retraction."""
    if not candidates:
        return None
    winner = max(candidates, key=resolution_key)
    if winner.kind is Kind.RETRACT:
        return None
    return winner
