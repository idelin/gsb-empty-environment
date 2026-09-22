"""Cost model and the materialize-vs-recompute decision for shared subplans.

For a subplan S with k consumers (k reads of its result):

* recompute cost      = k * compute(S)
* materialize cost    = compute(S) + write(S) + k * read(S)

If the memory footprint of S exceeds the configured quota, write/read are
charged at (more expensive) spill-to-disk rates instead of memory rates.

Rules:
* a single consumer never justifies materialization (no reuse possible);
* otherwise pick the cheaper strategy and record a human-readable
  rationale with the numbers that drove the choice.
"""
from __future__ import annotations

from dataclasses import dataclass

RECOMPUTE = "recompute"
MATERIALIZE_MEMORY = "materialize-memory"
MATERIALIZE_SPILL = "materialize-spill"


@dataclass(frozen=True)
class CostModel:
    cpu_per_row: float = 1.0          # produce one row of any operator
    write_per_row: float = 0.5        # write one row into memory catalog
    read_per_row: float = 0.2         # read one row from memory catalog
    mem_bytes_per_row: int = 64       # estimated footprint per row
    mem_quota_bytes: int = 1 << 20    # above this a view must spill
    spill_write_per_row: float = 1.0  # write one row to disk
    spill_read_per_row: float = 0.8   # read one row back from disk


@dataclass(frozen=True)
class Decision:
    strategy: str            # RECOMPUTE | MATERIALIZE_MEMORY | MATERIALIZE_SPILL
    consumers: int
    est_rows: int
    recompute_cost: float
    materialize_cost: float
    memory_bytes: int
    spilled: bool
    rationale: str

    @property
    def materialized(self):
        return self.strategy != RECOMPUTE


def decide(consumers, est_rows, compute_cost, model=CostModel()):
    """Choose a strategy for one shared subplan and explain why."""
    recompute = consumers * compute_cost
    mem_bytes = est_rows * model.mem_bytes_per_row
    spilled = mem_bytes > model.mem_quota_bytes
    if spilled:
        write = est_rows * model.spill_write_per_row
        read = consumers * est_rows * model.spill_read_per_row
    else:
        write = est_rows * model.write_per_row
        read = consumers * est_rows * model.read_per_row
    materialize = compute_cost + write + read

    if consumers <= 1:
        rationale = (
            f"single consumer: no reuse possible; materializing would only add "
            f"{write + read:.1f} write/read overhead on top of "
            f"{compute_cost:.1f} compute -> recompute"
        )
        return Decision(RECOMPUTE, consumers, est_rows, recompute, materialize,
                        mem_bytes, spilled, rationale)

    if materialize < recompute:
        strategy = MATERIALIZE_SPILL if spilled else MATERIALIZE_MEMORY
        rationale = (
            f"{consumers} consumers: compute once + share "
            f"({materialize:.1f} = {compute_cost:.1f} compute + {write:.1f} write "
            f"+ {read:.1f} reads) beats recomputing {consumers}x "
            f"({recompute:.1f})"
        )
        if spilled:
            rationale += (
                f"; footprint {mem_bytes}B exceeds memory quota "
                f"{model.mem_quota_bytes}B -> spill to disk"
            )
        else:
            rationale += (
                f"; footprint {mem_bytes}B fits memory quota "
                f"{model.mem_quota_bytes}B -> keep in memory"
            )
        return Decision(strategy, consumers, est_rows, recompute, materialize,
                        mem_bytes, spilled, rationale)

    rationale = (
        f"{consumers} consumers, but recomputing {consumers}x "
        f"({recompute:.1f}) is still cheaper than materialize + share "
        f"({materialize:.1f}) -> recompute"
    )
    return Decision(RECOMPUTE, consumers, est_rows, recompute, materialize,
                    mem_bytes, spilled, rationale)
