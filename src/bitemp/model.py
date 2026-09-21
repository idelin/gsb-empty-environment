"""Core data model for the bitemporal version index.

Every record carries two time coordinates:

* valid time  (``valid_from``/``valid_to``) -- when the fact holds in the
  business world ("10 时发生了什么" 的时间轴).
* transaction time (``tx_from``) -- from which observation moment the record
  is visible as evidence ("当时我们知道什么" 的时间轴).

Valid intervals are left-closed, right-open: [valid_from, valid_to).
Visibility is left-closed as well: a record is visible at observation time
``t_tx`` iff ``tx_from <= t_tx``.
"""

from __future__ import annotations

import math
from dataclasses import dataclass
from enum import Enum
from typing import Any

INF = math.inf


class Kind(Enum):
    ASSERT = "assert"      # first assertion of a fact (also used for backfills)
    CORRECT = "correct"    # correction of previously recorded evidence
    RETRACT = "retract"    # tombstone: "as far as we know, no value holds here"


@dataclass(frozen=True)
class Version:
    """One immutable version of an object's value.

    ``seq`` is a per-object unique, monotonically assigned integer stored
    *inside* the record.  Together with ``tx_from`` it forms the explicit
    priority key, so conflict resolution never depends on arrival or
    traversal order.
    """

    object_id: str
    valid_from: float
    valid_to: float          # use model.INF for an open-ended interval
    tx_from: float
    seq: int
    kind: Kind
    value: Any = None

    def __post_init__(self) -> None:
        if not self.valid_from < self.valid_to:
            raise ValueError(
                f"empty valid interval [{self.valid_from}, {self.valid_to})"
            )
        if self.kind is Kind.RETRACT and self.value is not None:
            raise ValueError("a retraction must not carry a value")
        if self.kind is not Kind.RETRACT and self.value is None:
            raise ValueError("a non-retraction must carry a value")

    @property
    def priority(self) -> tuple[float, int]:
        """Explicit total order per object: later knowledge wins.

        Higher ``tx_from`` beats lower; ties (same ingestion moment) are
        broken by the stored ``seq``.  This is a total order over the
        versions of one object, independent of any traversal order.
        """
        return (self.tx_from, self.seq)

    def covers(self, t_valid: float) -> bool:
        return self.valid_from <= t_valid < self.valid_to

    @property
    def is_tombstone(self) -> bool:
        return self.kind is Kind.RETRACT

    def describe(self) -> str:
        body = "RETRACT" if self.is_tombstone else f"{self.kind.value}={self.value!r}"
        end = "inf" if self.valid_to == INF else f"{self.valid_to:g}"
        return (
            f"{self.object_id} valid=[{self.valid_from:g}, {end}) "
            f"tx>={self.tx_from:g} seq={self.seq} {body}"
        )
