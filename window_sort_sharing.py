"""Window sort-sharing planner and executor (pure Python, no SQL engine).

Multiple window definitions are grouped by partition/order compatibility so a
single physical sort can be reused. Frame boundaries stay per-window: windows
that share a sort still compute their own ROWS-frame results.

Sharing rules implemented here:
  * Same PARTITION BY keys and compatible ORDER BY keys share one sort.
  * Different ROWS frames may share the sort but never the results.
  * ASC vs DESC on the same column cannot be reused directly.
  * NULLS FIRST vs NULLS LAST differences (explicit or defaulted) prevent
    sharing; defaults follow the common SQL rule ASC->NULLS LAST,
    DESC->NULLS FIRST.

Relation semantics:
  * The relation is a local list of dict rows: a bag (multiset), so duplicate
    rows are kept and counted individually.
  * NULL is None. Rows with NULL partition keys group together (SQL behavior).
  * SUM/AVG/COUNT(col) ignore NULLs; SUM/AVG over an empty or all-NULL frame
    yield None; COUNT yields 0.
"""

from __future__ import annotations

from dataclasses import dataclass, field
from functools import cmp_to_key
from typing import Any, Sequence

Row = dict[str, Any]


# --------------------------------------------------------------------- frames

@dataclass(frozen=True)
class FrameBound:
    kind: str  # UNBOUNDED PRECEDING / PRECEDING / CURRENT ROW / FOLLOWING / UNBOUNDED FOLLOWING
    offset: int | None = None

    def describe(self) -> str:
        if self.offset is None:
            return self.kind
        return f"{self.offset} {self.kind}"


UNBOUNDED_PRECEDING = FrameBound("UNBOUNDED PRECEDING")
UNBOUNDED_FOLLOWING = FrameBound("UNBOUNDED FOLLOWING")
CURRENT_ROW = FrameBound("CURRENT ROW")


def preceding(n: int) -> FrameBound:
    return FrameBound("PRECEDING", n)


def following(n: int) -> FrameBound:
    return FrameBound("FOLLOWING", n)


@dataclass(frozen=True)
class Frame:
    """ROWS BETWEEN start AND end."""

    start: FrameBound = UNBOUNDED_PRECEDING
    end: FrameBound = CURRENT_ROW

    def describe(self) -> str:
        return f"ROWS BETWEEN {self.start.describe()} AND {self.end.describe()}"


# ----------------------------------------------------------------- order keys

@dataclass(frozen=True)
class OrderKey:
    column: str
    descending: bool = False
    nulls_first: bool | None = None  # None -> SQL default for the direction

    @property
    def effective_nulls_first(self) -> bool:
        if self.nulls_first is not None:
            return self.nulls_first
        return self.descending  # ASC -> NULLS LAST, DESC -> NULLS FIRST

    def normalized(self) -> tuple[str, bool, bool]:
        return (self.column, self.descending, self.effective_nulls_first)

    def describe(self) -> str:
        direction = "DESC" if self.descending else "ASC"
        nulls = "NULLS FIRST" if self.effective_nulls_first else "NULLS LAST"
        suffix = "" if self.nulls_first is not None else " (default)"
        return f"{self.column} {direction} {nulls}{suffix}"


# ---------------------------------------------------------------- window spec

@dataclass(frozen=True)
class WindowFunction:
    kind: str  # row_number / rank / dense_rank / sum / avg / count / count_star
    column: str | None = None

    def describe(self) -> str:
        if self.kind == "count_star":
            return "COUNT(*)"
        if self.column is None:
            return f"{self.kind.upper()}()"
        return f"{self.kind.upper()}({self.column})"


@dataclass(frozen=True)
class WindowSpec:
    name: str
    function: WindowFunction
    partition_by: tuple[str, ...] = ()
    order_by: tuple[OrderKey, ...] = ()
    frame: Frame = field(default_factory=Frame)

    def sort_key_signature(self) -> tuple[tuple[str, ...], tuple[tuple[str, bool, bool], ...]]:
        """Identity of the physical sort this window needs."""
        return (self.partition_by, tuple(k.normalized() for k in self.order_by))


# ------------------------------------------------------------------- planner

@dataclass
class SortGroup:
    partition_by: tuple[str, ...]
    order_by: tuple[OrderKey, ...]
    windows: list[WindowSpec] = field(default_factory=list)


class Plan:
    def __init__(self, groups: list[SortGroup], window_count: int) -> None:
        self.groups = groups
        self.window_count = window_count

    @property
    def sort_count(self) -> int:
        return len(self.groups)

    @property
    def naive_sort_count(self) -> int:
        return self.window_count

    def render(self) -> str:
        lines = [
            f"windows: {self.window_count}, "
            f"naive sorts: {self.naive_sort_count}, "
            f"shared sorts: {self.sort_count}",
        ]
        for idx, group in enumerate(self.groups, 1):
            part = ", ".join(group.partition_by) or "<whole table>"
            order = ", ".join(k.describe() for k in group.order_by) or "<unordered>"
            lines.append(f"sort #{idx}: PARTITION BY {part} | ORDER BY {order}")
            for spec in group.windows:
                lines.append(
                    f"    {spec.name}: {spec.function.describe()} "
                    f"OVER ({spec.frame.describe()})"
                )
        return "\n".join(lines)


def plan_windows(windows: Sequence[WindowSpec]) -> Plan:
    groups: dict[tuple, SortGroup] = {}
    order: list[tuple] = []
    for spec in windows:
        signature = spec.sort_key_signature()
        if signature not in groups:
            groups[signature] = SortGroup(spec.partition_by, spec.order_by)
            order.append(signature)
        groups[signature].windows.append(spec)
    return Plan([groups[sig] for sig in order], len(windows))


# ------------------------------------------------------------------ executor

def _make_cmp(keys: Sequence[tuple[str, bool, bool]]):
    """Build a comparator for (column, descending, nulls_first) keys.

    NULL placement is independent of direction: DESC must not flip it.
    """

    def cmp(ra: Row, rb: Row) -> int:
        for column, descending, nulls_first in keys:
            a, b = ra[column], rb[column]
            if a is None or b is None:
                if a is None and b is None:
                    continue
                if a is None:
                    return -1 if nulls_first else 1
                return 1 if nulls_first else -1
            if a != b:
                c = -1 if a < b else 1
                return -c if descending else c
        return 0

    return cmp


def _resolve_bound(bound: FrameBound, pos: int, size: int) -> int:
    if bound.kind == "UNBOUNDED PRECEDING":
        return 0
    if bound.kind == "UNBOUNDED FOLLOWING":
        return size - 1
    if bound.kind == "CURRENT ROW":
        return pos
    if bound.kind == "PRECEDING":
        return pos - (bound.offset or 0)
    if bound.kind == "FOLLOWING":
        return pos + (bound.offset or 0)
    raise ValueError(f"unknown frame bound: {bound.kind}")


def _frame_range(frame: Frame, pos: int, size: int) -> tuple[int, int] | None:
    """Inclusive [lo, hi] row range within the partition, or None if empty."""
    lo = _resolve_bound(frame.start, pos, size)
    hi = _resolve_bound(frame.end, pos, size)
    if hi < 0 or lo > size - 1 or lo > hi:
        return None
    return max(lo, 0), min(hi, size - 1)


def _peers(ra: Row, rb: Row, order_by: tuple[OrderKey, ...]) -> bool:
    for key in order_by:
        a, b = ra[key.column], rb[key.column]
        if a is None and b is None:
            continue
        if a != b:
            return False
    return True


def _aggregate(kind: str, values: list[Any]) -> Any:
    non_null = [v for v in values if v is not None]
    if kind == "sum":
        return sum(non_null) if non_null else None
    if kind == "avg":
        return sum(non_null) / len(non_null) if non_null else None
    if kind == "count":
        return len(non_null)
    if kind == "count_star":
        return len(values)
    raise ValueError(f"unknown aggregate: {kind}")


def _compute_partition(spec: WindowSpec, part_rows: list[Row]) -> list[Any]:
    n = len(part_rows)
    kind = spec.function.kind
    if kind == "row_number":
        return list(range(1, n + 1))
    if kind in ("rank", "dense_rank"):
        ranks: list[Any] = []
        rank, dense = 1, 1
        for i, row in enumerate(part_rows):
            if i > 0 and not _peers(part_rows[i - 1], row, spec.order_by):
                rank = i + 1
                dense += 1
            ranks.append(rank if kind == "rank" else dense)
        return ranks
    column = spec.function.column
    results = []
    for i in range(n):
        bounds = _frame_range(spec.frame, i, n)
        if bounds is None:
            values: list[Any] = []
        else:
            lo, hi = bounds
            values = [part_rows[j][column] if column else None for j in range(lo, hi + 1)]
        results.append(_aggregate(kind, values))
    return results


class ExecutionResult:
    def __init__(self, plan: Plan, results: dict[str, list[Any]], sorts_performed: int) -> None:
        self.plan = plan
        self.results = results  # window name -> values aligned with input row order
        self.sorts_performed = sorts_performed


def execute(plan: Plan, rows: Sequence[Row]) -> ExecutionResult:
    results: dict[str, list[Any]] = {}
    sorts_performed = 0
    for group in plan.groups:
        sort_keys = [(c, False, False) for c in group.partition_by]
        sort_keys += [k.normalized() for k in group.order_by]
        cmp = _make_cmp(sort_keys)
        order = sorted(range(len(rows)),
                       key=cmp_to_key(lambda i, j: cmp(rows[i], rows[j])))
        sorts_performed += 1  # one physical sort reused by every window below

        sorted_rows = [rows[i] for i in order]
        partitions: list[tuple[int, int]] = []  # [start, end) slices
        start = 0
        for i in range(1, len(order) + 1):
            boundary = i == len(order) or any(
                sorted_rows[i][c] != sorted_rows[start][c]
                and not (sorted_rows[i][c] is None and sorted_rows[start][c] is None)
                for c in group.partition_by
            )
            if boundary:
                partitions.append((start, i))
                start = i

        for spec in group.windows:
            values: list[Any] = [None] * len(rows)
            for lo, hi in partitions:
                part_rows = sorted_rows[lo:hi]
                part_values = _compute_partition(spec, part_rows)
                for offset, value in enumerate(part_values):
                    values[order[lo + offset]] = value
            results[spec.name] = values
    return ExecutionResult(plan, results, sorts_performed)


# ---------------------------------------------------------------------- demo

RELATION: list[Row] = [
    # dept is NULL for rows 3-4; rows 3 and 4 are exact duplicates (multiset);
    # salary/bonus contain NULLs.
    {"dept": "eng", "salary": 100, "bonus": 10},
    {"dept": "eng", "salary": 100, "bonus": None},
    {"dept": "eng", "salary": None, "bonus": 5},
    {"dept": None, "salary": 80, "bonus": 7},
    {"dept": None, "salary": 80, "bonus": 7},
    {"dept": "ops", "salary": 90, "bonus": None},
    {"dept": "ops", "salary": 95, "bonus": 3},
    {"dept": "eng", "salary": 120, "bonus": 20},
]

WINDOWS: list[WindowSpec] = [
    WindowSpec(
        name="w1_running_sum",
        function=WindowFunction("sum", "salary"),
        partition_by=("dept",),
        order_by=(OrderKey("salary"),),  # ASC, NULLS LAST by default
        frame=Frame(UNBOUNDED_PRECEDING, CURRENT_ROW),
    ),
    WindowSpec(
        name="w2_framed_sum",
        function=WindowFunction("sum", "salary"),
        partition_by=("dept",),
        order_by=(OrderKey("salary", nulls_first=False),),  # explicit == default
        frame=Frame(preceding(1), following(1)),  # different ROWS frame, same sort
    ),
    WindowSpec(
        name="w3_desc_sum",
        function=WindowFunction("sum", "salary"),
        partition_by=("dept",),
        order_by=(OrderKey("salary", descending=True),),  # DESC: cannot reuse ASC sort
        frame=Frame(UNBOUNDED_PRECEDING, CURRENT_ROW),
    ),
    WindowSpec(
        name="w4_nulls_first_rn",
        function=WindowFunction("row_number"),
        partition_by=("dept",),
        order_by=(OrderKey("salary", nulls_first=True),),  # NULLS FIRST: new sort
        frame=Frame(UNBOUNDED_PRECEDING, UNBOUNDED_FOLLOWING),
    ),
    WindowSpec(
        name="w5_rank",
        function=WindowFunction("rank"),
        partition_by=("dept",),
        order_by=(OrderKey("salary"),),  # shares the sort of w1/w2
        frame=Frame(UNBOUNDED_PRECEDING, UNBOUNDED_FOLLOWING),
    ),
]


def main() -> None:
    plan = plan_windows(WINDOWS)
    result = execute(plan, RELATION)

    print("== relation (bag semantics: duplicates kept, NULL shown as None) ==")
    for i, row in enumerate(RELATION):
        print(f"  [{i}] {row}")
    print()
    print("== shared plan ==")
    print(plan.render())
    print()
    print(f"actual sorts performed: {result.sorts_performed} "
          f"(naive would be {plan.naive_sort_count})")
    print()
    print("== results (aligned with input row order) ==")
    header = ["row", "dept", "salary"] + [spec.name for spec in WINDOWS]
    print("  " + " | ".join(h.rjust(15) for h in header))
    for i, row in enumerate(RELATION):
        cells = [str(i), str(row["dept"]), str(row["salary"])]
        cells += [str(result.results[spec.name][i]) for spec in WINDOWS]
        print("  " + " | ".join(c.rjust(15) for c in cells))


if __name__ == "__main__":
    main()
