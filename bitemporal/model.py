"""Records, live state and visible fragments (bitemporal rectangles)."""

from __future__ import annotations

from dataclasses import dataclass

from .timeline import Instant

UPSERT = "upsert"
RETRACT = "retract"
REPRIORITIZE = "reprioritize"


@dataclass(frozen=True)
class Record:
    """One immutable entry in the append-only history log.

    Every entry carries the observation *version* at which it was recorded
    and, for upserts, the business (valid) interval ``[valid_from, valid_to)``.
    ``valid_to=None`` is an open end.  Nothing in the log is ever rewritten;
    retractions and priority changes are new entries that refer to their
    target, so history is never overwritten.
    """

    record_id: int
    version: int
    op: str
    key: str
    valid_from: Instant | None
    valid_to: Instant | None
    value: object = None
    priority: int = 0
    target: int | None = None


@dataclass(frozen=True)
class LiveRecord:
    """The current (latest-version) live state of one upsert record."""

    record_id: int
    valid_from: Instant | None
    valid_to: Instant | None
    value: object
    priority: int


@dataclass
class Fragment:
    """A canonical visible rectangle: valid interval x version interval.

    Both intervals are half-open ``[from, to)`` so boundaries never
    double-hit.  ``ver_to=None`` means the fragment is on the frontier
    (still visible at the latest version).  Only ``ver_to`` may ever be
    written, exactly once, when the fragment is closed -- that is what
    keeps earlier observation versions immutable.
    """

    key: str
    valid_from: Instant | None
    valid_to: Instant | None
    ver_from: int
    ver_to: int | None
    value: object
    priority: int
    record_id: int

    def identity(self):
        return (self.valid_from, self.valid_to, self.value, self.priority, self.record_id)


@dataclass(frozen=True)
class Competitor:
    """One record that covers the explained point, with its fate."""

    record_id: int
    priority: int
    recorded_version: int
    valid_from: Instant | None
    valid_to: Instant | None
    value: object
    status: str


@dataclass(frozen=True)
class Explanation:
    """Provenance for a point query: who won, who lost, and why."""

    key: str
    when: Instant
    as_of: int
    value: object
    fragment: Fragment | None
    competitors: tuple[Competitor, ...]


# -- boundary sort keys -----------------------------------------------------
# None on the "from" side is -inf, None on the "to" side is +inf.  The tuple
# encoding keeps every comparison exact (integers only).

def from_key(bound: Instant | None) -> tuple[int, int]:
    return (0, 0) if bound is None else (1, bound.ticks)


def to_key(bound: Instant | None) -> tuple[int, int]:
    return (2, 0) if bound is None else (1, bound.ticks)


def key_to_bound(key: tuple[int, int]) -> Instant | None:
    return None if key[0] in (0, 2) else Instant(key[1])
