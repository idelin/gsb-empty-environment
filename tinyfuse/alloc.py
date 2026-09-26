from __future__ import annotations

from dataclasses import dataclass, field
from itertools import count
from typing import Any


@dataclass(frozen=True)
class Allocation:
    allocation_id: int
    size: int
    dtype: str
    reason: str


@dataclass
class AllocationStats:
    allocations: list[Allocation] = field(default_factory=list)
    elements_allocated: int = 0

    def record(self, size: int, dtype: str, reason: str) -> int:
        allocation_id = len(self.allocations)
        self.allocations.append(Allocation(allocation_id, size, dtype, reason))
        self.elements_allocated += size
        return allocation_id

    def total(self) -> int:
        return len(self.allocations)

    def by_reason(self) -> dict[str, int]:
        result: dict[str, int] = {}
        for allocation in self.allocations:
            result[allocation.reason] = result.get(allocation.reason, 0) + 1
        return result


class Allocator:
    def __init__(self) -> None:
        self._ids = count()

    def new_storage_id(self) -> int:
        return next(self._ids)


allocator = Allocator()


def measure(fn: Any) -> tuple[Any, AllocationStats]:
    import tinyfuse.tensor as tensor_module

    stats = AllocationStats()
    previous = tensor_module.current_stats
    tensor_module.current_stats = stats
    try:
        value = fn()
    finally:
        tensor_module.current_stats = previous
    return value, stats
