"""Cluster model: weighted partitions, execution slots, placement constraints.

- Slots have fixed byte capacities.
- Partitions have an exact integer weight (bytes) and an optional
  affinity set (the only slots they may run on).
- Anti-affinity pairs name partitions that may never share a slot.
- A partition is never split: if its whole weight fits none of its
  allowed slots, it is reported as unmet demand, not broken up.
"""

from __future__ import annotations

from dataclasses import dataclass, field
from typing import Optional


@dataclass(frozen=True)
class Slot:
    sid: str
    capacity: int

    def __post_init__(self) -> None:
        if not isinstance(self.capacity, int) or isinstance(self.capacity, bool):
            raise TypeError("slot capacity must be an exact integer")
        if self.capacity < 0:
            raise ValueError("slot capacity must be non-negative")


@dataclass(frozen=True)
class Partition:
    pid: str
    weight: int
    allowed_slots: Optional[frozenset] = None  # None == any slot

    def __post_init__(self) -> None:
        if not isinstance(self.weight, int) or isinstance(self.weight, bool):
            raise TypeError("partition weight must be an exact integer")
        if self.weight < 0:
            raise ValueError("partition weight must be non-negative")
        if self.allowed_slots is not None:
            object.__setattr__(self, "allowed_slots", frozenset(self.allowed_slots))


@dataclass
class Cluster:
    slots: dict
    partitions: dict
    anti_affinity: frozenset = field(default_factory=frozenset)
    current: dict = field(default_factory=dict)  # pid -> sid (start placement)

    def __post_init__(self) -> None:
        self.slots = dict(self.slots)
        self.partitions = dict(self.partitions)
        pairs = set()
        for pair in self.anti_affinity:
            pair = frozenset(pair)
            if len(pair) != 2:
                raise ValueError("anti-affinity constraint must be a pair")
            a, b = tuple(pair)
            if a not in self.partitions or b not in self.partitions:
                raise ValueError(f"anti-affinity references unknown partition: {pair}")
            pairs.add(frozenset((a, b)))
        self.anti_affinity = frozenset(pairs)
        current = dict(self.current)
        for pid, sid in current.items():
            if pid not in self.partitions:
                raise ValueError(f"current placement references unknown partition {pid}")
            if sid not in self.slots:
                raise ValueError(f"current placement references unknown slot {sid}")
        self.current = current

    def allowed_slot_ids(self, pid: str) -> set:
        allowed = self.partitions[pid].allowed_slots
        return set(self.slots) if allowed is None else set(allowed) & set(self.slots)

    def conflict_partners(self, pid: str) -> set:
        out = set()
        for pair in self.anti_affinity:
            if pid in pair:
                out.update(pair - {pid})
        return out

    def loads_for(self, assignment: dict) -> dict:
        """Exact per-slot byte load for an assignment dict pid -> sid."""
        loads = {sid: 0 for sid in self.slots}
        for pid, sid in assignment.items():
            if pid not in self.partitions:
                raise KeyError(f"unknown partition {pid}")
            if sid not in self.slots:
                raise KeyError(f"unknown slot {sid}")
            loads[sid] += self.partitions[pid].weight
        return loads
