"""Top-level execution API."""

import json
import os
from dataclasses import dataclass

from .errors import Cancelled
from .interpreter import Interpreter
from .optimized import OptimizedExecutor
from .plan import explain
from .spill import SpillManager
from .stats import Stats


@dataclass
class Result:
    rows: list
    stats: Stats
    plan: str


def run_query(query, tables, outer_pages, *, mode="optimized",
              mem_budget=1 << 20, spill_dir=None, output_path=None,
              cancel_check=None, open_fn=open, partition_cache=2):
    """Run a query and return Result(rows, stats, plan).

    mode: "interpreter" (row-at-a-time reference) or "optimized"
    (decorrelated joins with spilling index).

    On any error (cancellation, disk failure, ...) all spill partitions and
    any partial output are removed, so no incomplete result can be observed.
    """
    stats = Stats()
    spill = SpillManager(spill_dir, open_fn) if mode == "optimized" else None
    try:
        if mode == "interpreter":
            executor = Interpreter(query, tables, stats, cancel_check)
        elif mode == "optimized":
            executor = OptimizedExecutor(
                query, tables, stats, spill, mem_budget,
                cancel_check=cancel_check, partition_cache=partition_cache,
            )
        else:
            raise ValueError(f"unknown mode: {mode}")
        rows = executor.run(outer_pages)
        if output_path is not None:
            _write_output(output_path, rows, open_fn)
        return Result(rows, stats, explain(query, mode))
    finally:
        if spill is not None:
            spill.cleanup()


def _write_output(output_path, rows, open_fn):
    """Atomic output: write to a temp file, then rename into place."""
    tmp_path = output_path + ".tmp"
    try:
        with open_fn(tmp_path, "w", encoding="utf-8") as fobj:
            for row in rows:
                fobj.write(json.dumps(list(row)) + "\n")
        os.replace(tmp_path, output_path)
    except BaseException:
        if os.path.exists(tmp_path):
            os.remove(tmp_path)
        raise


__all__ = ["Cancelled", "Result", "Stats", "run_query"]
