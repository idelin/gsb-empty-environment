"""Relational plan nodes with backward column-requirement analysis.

``analyze`` walks the plan top-down (from output back to scans), threading
each node's required output columns down to its children. Every expression
(filter predicate, join key, sort key, projection, aggregate argument,
group key) contributes *implicit* dependencies: columns the plan needs even
though they never appear in the query output. A Scan then keeps only the
columns it is asked for and records pruning evidence.

Execution uses the pruned plans, but two semantic guarantees hold:
- Sort keys not present in the output are still requested from the child,
  used for ordering, and dropped only *after* the sort.
- COUNT(*) needs no columns at all: a scan may emit zero-width tuples,
  one per row, so row multiplicity (and thus the count) survives pruning.
"""
from __future__ import annotations

import functools
import itertools
from dataclasses import dataclass, field

from .algebra import ColRef, Expr, Table

_node_ids = itertools.count(1)


def _merge(into: dict, col, reason: str) -> None:
    into.setdefault(col, set()).add(reason)


# ---------------------------------------------------------------------------
# Evidence
# ---------------------------------------------------------------------------

@dataclass
class ScanEvidence:
    node: str
    table: str
    alias: str
    kept: list[tuple[str, list[str]]] = field(default_factory=list)
    pruned: list[str] = field(default_factory=list)


class Evidence:
    """Pruning evidence: per-scan kept/pruned columns plus semantic notes."""

    def __init__(self) -> None:
        self.scans: list[ScanEvidence] = []
        self.notes: list[str] = []

    def render(self) -> str:
        lines = ["== column pruning evidence =="]
        for scan in self.scans:
            lines.append(f"[{scan.node}] scan {scan.table} AS {scan.alias}")
            if scan.kept:
                for col, reasons in scan.kept:
                    lines.append(
                        f"  KEEP  {scan.alias}.{col:<10} <- {'; '.join(reasons)}"
                    )
            else:
                lines.append("  KEEP  (no columns - row multiplicity only)")
            for col in scan.pruned:
                lines.append(
                    f"  PRUNE {scan.alias}.{col:<10} (unreferenced by output, "
                    f"filter, join, sort, or aggregate)"
                )
        if self.notes:
            lines.append("notes:")
            lines.extend(f"  - {note}" for note in self.notes)
        return "\n".join(lines)


# ---------------------------------------------------------------------------
# Plan nodes
# ---------------------------------------------------------------------------

class Node:
    def __init__(self) -> None:
        self.id = f"n{next(_node_ids)}"
        self.emit: list = []  # schema entries this node outputs (post-pruning)

    @property
    def schema(self) -> list:
        raise NotImplementedError

    def analyze(self, required: dict, ev: Evidence) -> None:
        raise NotImplementedError

    def execute(self) -> list[tuple]:
        raise NotImplementedError


class Scan(Node):
    def __init__(self, table: Table, alias: str | None = None):
        super().__init__()
        self.table = table
        self.alias = alias or table.name

    @property
    def schema(self) -> list[ColRef]:
        return [ColRef(self.alias, c) for c in self.table.columns]

    def analyze(self, required: dict, ev: Evidence) -> None:
        self.emit = [c for c in self.schema if c in required]
        kept = [(c.name, sorted(required[c])) for c in self.emit]
        pruned = [c.name for c in self.schema if c not in required]
        ev.scans.append(
            ScanEvidence(self.id, self.table.name, self.alias, kept, pruned)
        )
        self._idx = [self.table.columns.index(c.name) for c in self.emit]

    def execute(self) -> list[tuple]:
        # Zero kept columns -> one empty tuple per row: multiplicity survives.
        return [tuple(row[i] for i in self._idx) for row in self.table.rows]


class Filter(Node):
    def __init__(self, child: Node, predicate: Expr):
        super().__init__()
        self.child = child
        self.predicate = predicate

    @property
    def schema(self) -> list:
        return self.child.schema

    def analyze(self, required: dict, ev: Evidence) -> None:
        child_req = {c: set(r) for c, r in required.items()}
        for col in self.predicate.columns():
            _merge(child_req, col, f"filter predicate at {self.id}")
        self.child.analyze(child_req, ev)
        self.emit = [c for c in self.schema if c in required]

    def execute(self) -> list[tuple]:
        rows = self.child.execute()
        child_schema = self.child.emit
        pos = [child_schema.index(c) for c in self.emit]
        out = []
        for row in rows:
            ctx = dict(zip(child_schema, row))
            if self.predicate.eval(ctx) is True:  # UNKNOWN/False rejected
                out.append(tuple(row[i] for i in pos))
        return out


class Join(Node):
    """Equi-join on one key expression per side; NULL keys never match."""

    def __init__(self, left: Node, right: Node, left_on: Expr, right_on: Expr):
        super().__init__()
        overlap = set(left.schema) & set(right.schema)
        if overlap:
            raise ValueError(
                f"ambiguous columns across join inputs: {overlap}; "
                f"qualify same-named columns by distinct source aliases"
            )
        self.left = left
        self.right = right
        self.left_on = left_on
        self.right_on = right_on

    @property
    def schema(self) -> list:
        return self.left.schema + self.right.schema

    def analyze(self, required: dict, ev: Evidence) -> None:
        left_req, right_req = {}, {}
        left_schema = set(self.left.schema)
        for col, reasons in required.items():
            if col in left_schema:
                left_req[col] = set(reasons)
            else:
                right_req[col] = set(reasons)
        for col in self.left_on.columns():
            _merge(left_req, col, f"join key at {self.id}")
        for col in self.right_on.columns():
            _merge(right_req, col, f"join key at {self.id}")
        self.left.analyze(left_req, ev)
        self.right.analyze(right_req, ev)
        self.emit = [c for c in self.schema if c in required]

    def execute(self) -> list[tuple]:
        left_rows = self.left.execute()
        right_rows = self.right.execute()
        left_schema, right_schema = self.left.emit, self.right.emit
        buckets: dict = {}
        for row in right_rows:
            key = self.right_on.eval(dict(zip(right_schema, row)))
            if key is None:
                continue  # NULL never joins
            buckets.setdefault(key, []).append(row)
        combined = left_schema + right_schema
        pos = [combined.index(c) for c in self.emit]
        out = []
        for lrow in left_rows:
            key = self.left_on.eval(dict(zip(left_schema, lrow)))
            if key is None:
                continue
            for rrow in buckets.get(key, ()):  # bag: all pairs emitted
                joined = lrow + rrow
                out.append(tuple(joined[i] for i in pos))
        return out


class Project(Node):
    def __init__(self, child: Node, items: list[tuple[Expr, str]]):
        super().__init__()
        names = [name for _, name in items]
        if len(set(names)) != len(names):
            raise ValueError(f"duplicate project output names: {names}")
        self.child = child
        self.items = items

    @property
    def schema(self) -> list[str]:
        return [name for _, name in self.items]

    def analyze(self, required: dict, ev: Evidence) -> None:
        child_req: dict = {}
        self._items = []
        for expr, name in self.items:
            if name not in required:
                continue
            self._items.append((expr, name))
            for col in expr.columns():
                _merge(child_req, col, f"project '{name}' at {self.id}")
        self.child.analyze(child_req, ev)
        self.emit = [name for _, name in self._items]

    def execute(self) -> list[tuple]:
        rows = self.child.execute()
        child_schema = self.child.emit
        return [
            tuple(expr.eval(dict(zip(child_schema, row))) for expr, _ in self._items)
            for row in rows
        ]


def _cmp_nulls_first(a, b) -> int:
    """Total order: NULL sorts before every non-NULL value (ASC)."""
    if a is None and b is None:
        return 0
    if a is None:
        return -1
    if b is None:
        return 1
    return (a > b) - (a < b)


class Sort(Node):
    """Sort keys are requested from the child even when absent from the
    output; they are dropped only after ordering is applied."""

    def __init__(self, child: Node, keys: list[tuple[Expr, bool]]):
        super().__init__()
        self.child = child
        self.keys = keys  # (expr, descending)

    @property
    def schema(self) -> list:
        return self.child.schema

    def analyze(self, required: dict, ev: Evidence) -> None:
        child_req = {c: set(r) for c, r in required.items()}
        retained = []
        for expr, _desc in self.keys:
            for col in expr.columns():
                if col not in required:
                    retained.append(col)
                _merge(child_req, col, f"sort key at {self.id} (dropped after sort)")
        self.child.analyze(child_req, ev)
        self.emit = [c for c in self.schema if c in required]
        if retained:
            cols = ", ".join(str(c) for c in retained)
            ev.notes.append(
                f"{self.id}: sort key column(s) {cols} retained through sort, "
                f"dropped afterwards (not in query output)"
            )

    def execute(self) -> list[tuple]:
        rows = self.child.execute()
        child_schema = self.child.emit

        def compare(a, b):
            for expr, desc in self.keys:
                ka = expr.eval(dict(zip(child_schema, a)))
                kb = expr.eval(dict(zip(child_schema, b)))
                c = _cmp_nulls_first(ka, kb)
                if c:
                    return -c if desc else c
            return 0

        rows = sorted(rows, key=functools.cmp_to_key(compare))  # stable
        pos = [child_schema.index(c) for c in self.emit]
        return [tuple(row[i] for i in pos) for row in rows]


def _apply_agg(func: str, arg: Expr | None, members: list[tuple], schema) -> object:
    if func == "count" and arg is None:
        return len(members)  # COUNT(*): counts rows, needs no column
    values = [v for v in (arg.eval(dict(zip(schema, row))) for row in members) if v is not None]
    if func == "count":
        return len(values)  # COUNT(col) ignores NULLs
    if not values:
        return None  # SUM/AVG/MIN/MAX of empty/all-NULL set -> NULL
    if func == "sum":
        return sum(values)
    if func == "avg":
        return sum(values) / len(values)
    if func == "min":
        return min(values)
    if func == "max":
        return max(values)
    raise ValueError(f"unknown aggregate {func!r}")


class Aggregate(Node):
    """Grouping + aggregates. NULL group keys group together. With no
    group-by, exactly one row is produced even for empty input."""

    def __init__(self, child: Node, group_by: list[tuple[Expr, str]] = (),
                 aggs: list[tuple[str, Expr | None, str]] = ()):
        super().__init__()
        self.child = child
        self.group_by = list(group_by)
        self.aggs = list(aggs)

    @property
    def schema(self) -> list[str]:
        return [n for _, n in self.group_by] + [n for _, _, n in self.aggs]

    def analyze(self, required: dict, ev: Evidence) -> None:
        child_req: dict = {}
        self._group_pos = []
        for i, (expr, name) in enumerate(self.group_by):
            if name not in required:
                continue
            self._group_pos.append(i)
            for col in expr.columns():
                _merge(child_req, col, f"group key '{name}' at {self.id}")
        self._aggs = []
        for func, arg, name in self.aggs:
            if name not in required:
                continue
            func = func.lower()
            self._aggs.append((func, arg, name))
            if arg is None:
                ev.notes.append(
                    f"{self.id}: COUNT(*) needs no columns; row multiplicity "
                    f"preserved via zero-width tuples"
                )
            else:
                for col in arg.columns():
                    _merge(child_req, col, f"aggregate {func} at {self.id}")
        self.child.analyze(child_req, ev)
        self.emit = ([self.group_by[i][1] for i in self._group_pos]
                     + [name for _, _, name in self._aggs])

    def execute(self) -> list[tuple]:
        rows = self.child.execute()
        child_schema = self.child.emit
        groups: dict[tuple, list[tuple]] = {}
        order: list[tuple] = []
        for row in rows:
            ctx = dict(zip(child_schema, row))
            key = tuple(expr.eval(ctx) for expr, _ in self.group_by)
            if key not in groups:
                groups[key] = []
                order.append(key)
            groups[key].append(row)
        if not self.group_by and not order:
            order.append(())  # grand total over empty input
            groups[()] = []
        out = []
        for key in order:
            members = groups[key]
            values = [key[i] for i in self._group_pos]
            for func, arg, _name in self._aggs:
                values.append(_apply_agg(func, arg, members, child_schema))
            out.append(tuple(values))
        return out


def run(root: Node) -> tuple[list, list[tuple], Evidence]:
    """Analyze (backward from the output) then execute the pruned plan."""
    ev = Evidence()
    required = {c: {"query output"} for c in root.schema}
    root.analyze(required, ev)
    return list(root.emit), root.execute(), ev
