"""Incremental view maintenance (IVM) over multisets with SQL-style NULLs.

Pipeline shape::

    BaseTable --deltas--> Filter / Project / Join / GroupBy --> View

* Base tables receive keyed changes: insert/delete of ``(row_id, row)``.
* Every operator emits *deltas*: ``(row_tuple, count_delta)`` pairs.
  A negative ``count_delta`` retracts rows from the downstream multiset.
* All relations are multisets (bags): each distinct tuple carries a
  multiplicity.  A multiplicity that would drop below zero raises
  :class:`NegativeMultiplicityError` -- negative multiplicities are errors.

NULL semantics (explicit, SQL-style):

=========  ============================================================
join       a join key containing NULL never matches (NULL <> NULL)
filter     three-valued predicate; only ``True`` keeps the row
group-by   NULL is an ordinary key: all NULLs land in one group
SUM        ignores NULLs; an all-NULL group sums to NULL
COUNT      COUNT(*) counts rows, COUNT(col) counts non-NULL values
=========  ============================================================

No SQL engine is used; operators are plain Python over dict-backed bags.
"""

from __future__ import annotations

__all__ = [
    "NULL", "UNKNOWN",
    "sql_eq", "sql_ne", "sql_lt", "sql_le", "sql_gt", "sql_ge",
    "sql_not", "sql_and", "sql_or",
    "IVMError", "NegativeMultiplicityError",
    "DuplicateRowIdError", "UnknownRowIdError",
    "BaseTable", "Filter", "Project", "Join", "GroupBy", "View",
]


# ----------------------------------------------------------------- sentinels

class _Null:
    """Singleton NULL marker (SQL unknown/missing value)."""

    _instance = None

    def __new__(cls) -> "_Null":
        if cls._instance is None:
            cls._instance = super().__new__(cls)
        return cls._instance

    def __repr__(self) -> str:
        return "NULL"

    def __bool__(self) -> bool:
        return False


class _Unknown:
    """Singleton UNKNOWN truth value for three-valued logic."""

    _instance = None

    def __new__(cls) -> "_Unknown":
        if cls._instance is None:
            cls._instance = super().__new__(cls)
        return cls._instance

    def __repr__(self) -> str:
        return "UNKNOWN"

    def __bool__(self) -> bool:
        raise TypeError("UNKNOWN has no boolean value (three-valued logic)")


NULL = _Null()
UNKNOWN = _Unknown()


# -------------------------------------------------------- three-valued logic

def _cmp(a, b, op):
    if a is NULL or b is NULL:
        return UNKNOWN
    return op(a, b)


def sql_eq(a, b):
    return _cmp(a, b, lambda x, y: x == y)


def sql_ne(a, b):
    return _cmp(a, b, lambda x, y: x != y)


def sql_lt(a, b):
    return _cmp(a, b, lambda x, y: x < y)


def sql_le(a, b):
    return _cmp(a, b, lambda x, y: x <= y)


def sql_gt(a, b):
    return _cmp(a, b, lambda x, y: x > y)


def sql_ge(a, b):
    return _cmp(a, b, lambda x, y: x >= y)


def sql_not(p):
    return UNKNOWN if p is UNKNOWN else not p


def sql_and(p, q):
    if p is False or q is False:
        return False
    if p is UNKNOWN or q is UNKNOWN:
        return UNKNOWN
    return True


def sql_or(p, q):
    if p is True or q is True:
        return True
    if p is UNKNOWN or q is UNKNOWN:
        return UNKNOWN
    return False


# --------------------------------------------------------------------- errors

class IVMError(Exception):
    """Base class for all engine errors."""


class NegativeMultiplicityError(IVMError):
    """A multiset multiplicity would drop below zero."""


class DuplicateRowIdError(IVMError):
    """Insert with a row id that already exists in a base table."""


class UnknownRowIdError(IVMError):
    """Delete of a row id that does not exist in a base table."""


# ---------------------------------------------------------------------- nodes

def _bag_add(bag: dict, row: tuple, count: int, owner: str) -> None:
    """Add ``count`` to ``bag[row]``; raise if the result is negative."""
    new = bag.get(row, 0) + count
    if new < 0:
        raise NegativeMultiplicityError(
            f"{owner}: multiplicity of {row!r} would become {new}")
    if new:
        bag[row] = new
    else:
        bag.pop(row, None)


class Node:
    """Pipeline node.  ``bag`` materialises this node's output multiset."""

    def __init__(self, name: str) -> None:
        self.name = name
        self.outputs: list[Node] = []
        self.bag: dict[tuple, int] = {}

    def connect(self, node: "Node") -> "Node":
        self.outputs.append(node)
        return node

    def receive(self, deltas: list, source: "Node") -> None:
        raise NotImplementedError

    def _emit(self, deltas: list) -> None:
        if not deltas:
            return
        for row, count in deltas:
            _bag_add(self.bag, row, count, self.name)
        for out in self.outputs:
            out.receive(deltas, self)


class BaseTable(Node):
    """A base relation: keyed rows on top of a tuple multiset."""

    def __init__(self, name: str, schema: tuple) -> None:
        super().__init__(name)
        self.schema = tuple(schema)
        self.rows: dict[object, tuple] = {}

    def insert(self, row_id, row) -> None:
        self.apply([(row_id, +1, row)])

    def delete(self, row_id) -> None:
        self.apply([(row_id, -1, None)])

    def apply(self, changes) -> None:
        """Apply a batch of ``(row_id, +1|-1, row|None)`` and emit deltas.

        The whole batch is validated before anything is emitted.
        """
        deltas = []
        staged = []
        seen = set()
        for row_id, sign, row in changes:
            if sign > 0:
                if row_id in self.rows or row_id in seen:
                    raise DuplicateRowIdError(
                        f"{self.name}: duplicate row id {row_id!r}")
                row = tuple(row)
                if len(row) != len(self.schema):
                    raise IVMError(
                        f"{self.name}: row {row!r} does not match "
                        f"schema {self.schema}")
                seen.add(row_id)
                staged.append((row_id, row))
                deltas.append((row, +1))
            else:
                if row_id not in self.rows:
                    raise UnknownRowIdError(
                        f"{self.name}: unknown row id {row_id!r}")
                staged.append((row_id, None))
                deltas.append((self.rows[row_id], -1))
        for row_id, row in staged:
            if row is None:
                del self.rows[row_id]
            else:
                self.rows[row_id] = row
        self._emit(deltas)


class Filter(Node):
    """Selection: keeps a row iff ``pred(row) is True``.

    The predicate is three-valued: ``False`` and ``UNKNOWN`` both drop the row.
    """

    def __init__(self, name: str, pred) -> None:
        super().__init__(name)
        self.pred = pred

    def receive(self, deltas, source) -> None:
        self._emit([(row, c) for row, c in deltas if self.pred(row) is True])


class Project(Node):
    """Projection without duplicate elimination: multiplicities add up."""

    def __init__(self, name: str, indexes) -> None:
        super().__init__(name)
        self.indexes = tuple(indexes)

    def receive(self, deltas, source) -> None:
        idx = self.indexes
        self._emit([(tuple(row[i] for i in idx), c) for row, c in deltas])


class Join(Node):
    """Inner equi-join of two inputs; output row = left_row + right_row.

    A delta row on one side is matched against the *current* multiset on the
    other side, so multiplicities multiply: an incoming row with multiplicity
    ``m`` joining rows of multiplicity ``n`` contributes ``m * n``.
    Keys containing NULL are stored but never match anything.
    """

    def __init__(self, name, left: Node, right: Node,
                 left_keys, right_keys) -> None:
        super().__init__(name)
        if len(left_keys) != len(right_keys):
            raise IVMError("join key arity mismatch")
        self.left_keys = tuple(left_keys)
        self.right_keys = tuple(right_keys)
        self.left_state: dict[tuple, dict[tuple, int]] = {}
        self.right_state: dict[tuple, dict[tuple, int]] = {}
        self.left = left
        self.right = right
        left.connect(self)
        right.connect(self)

    def receive(self, deltas, source) -> None:
        is_left = source is self.left
        own_keys = self.left_keys if is_left else self.right_keys
        own_state = self.left_state if is_left else self.right_state
        other_state = self.right_state if is_left else self.left_state
        out = []
        for row, count in deltas:
            key = tuple(row[i] for i in own_keys)
            if NULL not in key:  # NULL never matches, not even NULL
                for other_row, other_count in other_state.get(key, {}).items():
                    joined = row + other_row if is_left else other_row + row
                    out.append((joined, count * other_count))
            bucket = own_state.setdefault(key, {})
            _bag_add(bucket, row, count, self.name)
            if not bucket:
                del own_state[key]
        self._emit(out)


_MISSING = object()  # internal: no non-NULL value seen yet (for SUM)


class GroupBy(Node):
    """Grouping with COUNT / SUM aggregates.

    ``keys``: indexes of grouping columns.  ``aggs``: tuples of
    ``("count", None)`` -> COUNT(*), ``("count", i)`` -> COUNT of non-NULL
    values in column ``i``, ``("sum", i)`` -> SUM of column ``i`` ignoring
    NULLs (an all-NULL group sums to NULL).

    Output row = key tuple + aggregate values.  Every input delta retracts the
    group's old output row and inserts the new one; when the group's row count
    reaches zero the group (and its output row) disappears -- retracting the
    last supporting row emits a deletion.
    """

    def __init__(self, name: str, keys, aggs) -> None:
        super().__init__(name)
        self.keys = tuple(keys)
        self.aggs = tuple(aggs)
        self.groups: dict[tuple, dict] = {}

    def _new_state(self) -> dict:
        return {
            "n": 0,
            "acc": [0 if kind == "count" else _MISSING
                    for kind, _ in self.aggs],
            "nn": [0] * len(self.aggs),  # non-NULL contributors (for SUM)
        }

    def _apply_row(self, state: dict, row: tuple, count: int) -> None:
        state["n"] += count
        for k, (kind, idx) in enumerate(self.aggs):
            if kind == "count":
                if idx is None or row[idx] is not NULL:
                    state["acc"][k] += count
            elif kind == "sum":
                value = row[idx]
                if value is not NULL:
                    acc = state["acc"][k]
                    state["acc"][k] = (value * count if acc is _MISSING
                                       else acc + value * count)
                    state["nn"][k] += count
            else:
                raise IVMError(f"unknown aggregate {kind!r}")

    def _render(self, key: tuple, state: dict) -> tuple:
        vals = []
        for k, (kind, _) in enumerate(self.aggs):
            v = state["acc"][k]
            if kind == "sum" and (v is _MISSING or state["nn"][k] == 0):
                v = NULL
            vals.append(v)
        return key + tuple(vals)

    def receive(self, deltas, source) -> None:
        out = []
        for row, count in deltas:
            key = tuple(row[i] for i in self.keys)
            state = self.groups.get(key)
            if state is None:
                if count < 0:
                    raise NegativeMultiplicityError(
                        f"{self.name}: retraction of absent group {key!r}")
                state = self._new_state()
                self.groups[key] = state
            else:
                out.append((self._render(key, state), -1))
            if state["n"] + count < 0:
                raise NegativeMultiplicityError(
                    f"{self.name}: group {key!r} count would become "
                    f"{state['n'] + count}")
            self._apply_row(state, row, count)
            if state["n"] == 0:
                del self.groups[key]
            else:
                out.append((self._render(key, state), +1))
        self._emit(out)


class View(Node):
    """Sink: materialises the result multiset and records the delta stream."""

    def __init__(self, name: str) -> None:
        super().__init__(name)
        self.delta_log: list[tuple[tuple, int]] = []

    def receive(self, deltas, source) -> None:
        self.delta_log.extend(deltas)
        self._emit(deltas)

    def items(self) -> list:
        """Sorted ``(row, multiplicity)`` pairs of the current result."""
        return sorted(self.bag.items(), key=lambda kv: repr(kv[0]))
