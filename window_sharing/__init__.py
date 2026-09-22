"""Window-definition sort sharing planner and local executor.

Pure Python implementation (no SQL engine).  It decides which window
definitions can physically reuse the same ordered input stream, while keeping
their (possibly different) ROWS frames independent.
"""

from .model import CURRENT_ROW, Frame, FrameBound, OrderKey, WindowSpec
from .planner import (
    SharePlan,
    build_share_plan,
    naive_sort_count,
    physical_sort_count,
    render_plan,
)
from .engine import SortCounter, WindowResult, execute_windows, partition_key_counts

__all__ = [
    "CURRENT_ROW",
    "Frame",
    "FrameBound",
    "OrderKey",
    "WindowSpec",
    "SharePlan",
    "build_share_plan",
    "naive_sort_count",
    "physical_sort_count",
    "render_plan",
    "SortCounter",
    "WindowResult",
    "execute_windows",
    "partition_key_counts",
]
