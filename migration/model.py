"""Problem model: weighted partitions, fixed slots, placement constraints."""
from __future__ import annotations

from dataclasses import dataclass

from .batch import Batch, Type


@dataclass(frozen=True)
class Partition:
    pid: str
    weight: int  # bytes, exact integer; a partition is never split

    def __post_init__(self):
        if isinstance(self.weight, bool) or not isinstance(self.weight, int):
            raise TypeError("weight must be an exact int")
        if self.weight < 0:
            raise ValueError("weight must be >= 0")


@dataclass(frozen=True)
class Slot:
    sid: str
    capacity: int  # bytes, exact integer

    def __post_init__(self):
        if isinstance(self.capacity, bool) or not isinstance(self.capacity, int):
            raise TypeError("capacity must be an exact int")
        if self.capacity < 0:
            raise ValueError("capacity must be >= 0")


def _pair(a: str, b: str) -> frozenset:
    if a == b:
        raise ValueError(f"self-referential constraint on {a!r}")
    return frozenset((a, b))


@dataclass(frozen=True)
class Problem:
    partitions: tuple
    slots: tuple
    initial: dict  # pid -> sid, must be complete and capacity-feasible
    budget: int  # max total bytes that may move this round
    affinity: frozenset = frozenset()  # pairs that must share a slot
    anti_affinity: frozenset = frozenset()  # pairs that must not share a slot

    def __post_init__(self):
        pids = [p.pid for p in self.partitions]
        if len(set(pids)) != len(pids):
            raise ValueError("duplicate partition id")
        sids = {s.sid for s in self.slots}
        if len(sids) != len(self.slots):
            raise ValueError("duplicate slot id")
        if set(self.initial) != set(pids):
            raise ValueError("initial assignment must cover every partition exactly")
        for pid, sid in self.initial.items():
            if sid not in sids:
                raise ValueError(f"initial slot {sid!r} unknown")
        if isinstance(self.budget, bool) or not isinstance(self.budget, int) or self.budget < 0:
            raise ValueError("budget must be a non-negative exact int")
        known = set(pids)
        for group in (self.affinity, self.anti_affinity):
            for pair in group:
                if len(pair) != 2 or not pair <= known:
                    raise ValueError(f"constraint references unknown partitions: {pair}")
        # Note: the initial placement may exceed capacity (e.g. a slot
        # shrank); the solver then reports the unmet amount instead of
        # pretending an oversized partition can be split.

    @property
    def weights(self) -> dict:
        return {p.pid: p.weight for p in self.partitions}

    @property
    def capacities(self) -> dict:
        return {s.sid: s.capacity for s in self.slots}


def slot_loads(assignment: dict, weights: dict, capacities: dict) -> dict:
    loads = {sid: 0 for sid in capacities}
    for pid, sid in assignment.items():
        loads[sid] += weights[pid]
    return loads


def _require(batch: Batch, name: str, type: Type) -> None:
    col = batch.column(name)
    if col.type is not type:
        raise TypeError(f"column {name!r} must be {type.value}, got {col.type.value}")


def _require_not_null(batch: Batch, name: str) -> None:
    for i in range(batch.num_rows):
        if batch.is_null(name, i):
            raise ValueError(f"column {name!r} has null at row {i}")


def problem_from_batches(partitions: Batch, slots: Batch, *, budget: int,
                         affinity: Batch | None = None,
                         anti_affinity: Batch | None = None) -> Problem:
    """Build a Problem from typed batches.

    partitions: columns pid STRING, weight INT64, slot STRING (initial home).
    slots: columns sid STRING, capacity INT64.
    affinity / anti_affinity: columns left STRING, right STRING.
    Nulls in required columns are rejected; NaN cannot exist outside FLOAT64.
    """
    for name, t in (("pid", Type.STRING), ("weight", Type.INT64), ("slot", Type.STRING)):
        _require(partitions, name, t)
        _require_not_null(partitions, name)
    for name, t in (("sid", Type.STRING), ("capacity", Type.INT64)):
        _require(slots, name, t)
        _require_not_null(slots, name)

    parts, initial = [], {}
    for row in partitions.rows():
        parts.append(Partition(row["pid"], row["weight"]))
        initial[row["pid"]] = row["slot"]
    slot_list = [Slot(r["sid"], r["capacity"]) for r in slots.rows()]

    def pairs(batch):
        if batch is None:
            return frozenset()
        for name in ("left", "right"):
            _require(batch, name, Type.STRING)
            _require_not_null(batch, name)
        return frozenset(_pair(r["left"], r["right"]) for r in batch.rows())

    return Problem(
        partitions=tuple(parts),
        slots=tuple(slot_list),
        initial=initial,
        budget=budget,
        affinity=pairs(affinity),
        anti_affinity=pairs(anti_affinity),
    )
