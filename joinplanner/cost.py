"""Cost model.

The cost of a join is deliberately *not* a single selectivity product.  It
combines:
  - intermediate row counts (build/probe/output cardinalities),
  - memory footprint of the hash table (build_rows * row_width),
  - spill (partitioned) cost when the build side exceeds the memory budget.
"""
from __future__ import annotations

from dataclasses import dataclass

from .relations import INNER


@dataclass(frozen=True)
class CostModel:
    memory_bytes: int = 64 * 1024 * 1024
    row_width: int = 64
    cpu_per_row: float = 1.0
    spill_per_row: float = 6.0      # write+read of both sides, amortized
    mem_pressure: float = 1e-4      # cost per byte of resident hash table


@dataclass(frozen=True)
class JoinEstimate:
    out_rows: float
    build_rows: float
    mem_bytes: float
    spilled: bool
    total: float


def estimate_join(model: CostModel, left_rows: float, right_rows: float,
                  left_ndv: float, right_ndv: float,
                  join_type: str) -> JoinEstimate:
    denom = max(left_ndv, right_ndv, 1.0)
    inner_out = left_rows * right_rows / denom
    # A LEFT OUTER join emits at least one row per left row.
    out = inner_out if join_type == INNER else max(left_rows, inner_out)
    build_mem = right_rows * model.row_width
    spilled = build_mem > model.memory_bytes
    total = model.cpu_per_row * (left_rows + right_rows + out)
    total += model.mem_pressure * min(build_mem, model.memory_bytes)
    if spilled:
        total += model.spill_per_row * (left_rows + right_rows)
    return JoinEstimate(out, right_rows, build_mem, spilled, total)
