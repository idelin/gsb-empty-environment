"""Pure segment recomputation from raw records.

This module is intentionally side-effect free: the live store calls it after
every edit and the test suite calls it directly to cross-check both query
paths against the raw event stream.
"""

from __future__ import annotations

from collections import defaultdict
from dataclasses import dataclass
from typing import Iterable, Optional

from .model import Record, Resolver, default_rank


@dataclass(frozen=True, slots=True)
class Run:
    """A run of one state before segment ids are assigned."""

    state: str
    start: int
    end: Optional[int]
    sources: tuple[Record, ...]

    @property
    def key(self) -> tuple[str, int, Optional[int], tuple[Record, ...]]:
        return self.state, self.start, self.end, self.sources


@dataclass(frozen=True, slots=True)
class Conflict:
    """Two or more different states observed at the same timestamp."""

    time: int
    winner: Record
    losers: tuple[Record, ...]

    @property
    def all_records(self) -> tuple[Record, ...]:
        return (self.winner, *self.losers)


def source_order_key(record: Record) -> tuple[int, str]:
    return record.seq, record.source


def build_runs(
    records: Iterable[Record], resolver: Resolver | None = None
) -> tuple[Run, ...]:
    """Compress raw records into maximal same-state runs.

    * Records are grouped by integer time.
    * Within one timestamp the record ranked highest by ``resolver`` decides
      the active state; records asserting other states are shadowed.
    * Consecutive timestamps with the same winner state merge into one run, so
      repeated/middle records stay in ``sources`` rather than causing a split.
    * Each run is the half-open interval ``[start, next_run.start)``; the last
      run has ``end=None``.
    """
    rank = resolver or default_rank
    by_time: dict[int, list[Record]] = defaultdict(list)
    for record in records:
        by_time[record.time].append(record)
    if not by_time:
        return ()

    runs: list[Run] = []
    for time in sorted(by_time):
        group = by_time[time]
        winner = max(group, key=rank)
        evidence = tuple(
            sorted(
                (record for record in group if record.state == winner.state),
                key=source_order_key,
            )
        )
        if runs and runs[-1].state == winner.state:
            previous = runs[-1]
            merged_sources = tuple(
                sorted(previous.sources + evidence, key=source_order_key)
            )
            runs[-1] = Run(previous.state, previous.start, None, merged_sources)
        else:
            runs.append(Run(winner.state, time, None, evidence))

    finalized: list[Run] = []
    for index, run in enumerate(runs):
        end: Optional[int]
        if index + 1 < len(runs):
            end = runs[index + 1].start
        else:
            end = None
        finalized.append(Run(run.state, run.start, end, run.sources))
    return tuple(finalized)


def find_conflicts(
    records: Iterable[Record], resolver: Resolver | None = None
) -> tuple[Conflict, ...]:
    """Report every timestamp whose records assert more than one state."""
    rank = resolver or default_rank
    by_time: dict[int, list[Record]] = defaultdict(list)
    for record in records:
        by_time[record.time].append(record)

    conflicts: list[Conflict] = []
    for time in sorted(by_time):
        group = by_time[time]
        states = {record.state for record in group}
        if len(states) <= 1:
            continue
        winner = max(group, key=rank)
        losers = tuple(
            sorted(
                (record for record in group if record.state != winner.state),
                key=source_order_key,
            )
        )
        conflicts.append(Conflict(time, winner, losers))
    return tuple(conflicts)
