"""Decomposable distributed aggregation with SQL NULL and multiset semantics.

Aggregates are evaluated in two phases:

* local phase  -- each shard folds its rows into a small intermediate state
* global phase -- shard states are merged and finalized into one result

Decomposition rules:

* COUNT(*) / COUNT(col)  -> integer count
* SUM                    -> running total, ``None`` until a non-NULL value
* AVG                    -> (sum, count) pair; never average shard averages
* MIN / MAX              -> best value so far, ``None`` until a non-NULL value
* DISTINCT aggregates    -> carry the set of distinct non-NULL values

NULL semantics (SQL style):

* NULLs are ignored by every aggregate except COUNT(*)
* SUM/AVG/MIN/MAX over zero non-NULL values return NULL (None)
* COUNT over zero matching values returns 0
* an empty shard contributes only identity state, so it never
  contributes to an AVG denominator

Multiset semantics: duplicate rows are real rows and are counted/summed
once per occurrence; only DISTINCT aggregates deduplicate.

Tables are synthesized locally as plain Python data; no SQL engine is used.
"""

from __future__ import annotations

from typing import Any, Iterable, Sequence

NULL = None  # SQL NULL is represented by None


class Table:
    """A relational table as an in-memory multiset of rows."""

    def __init__(self, columns: Sequence[str], rows: Iterable[Sequence[Any]]):
        self.columns = tuple(columns)
        self.rows = [tuple(row) for row in rows]
        if len(self.columns) != len(set(self.columns)):
            raise ValueError("duplicate column names")
        for row in self.rows:
            if len(row) != len(self.columns):
                raise ValueError("row width does not match columns")

    def col(self, name: str) -> int:
        return self.columns.index(name)

    def shard(self, n: int) -> list["Table"]:
        """Split rows round-robin into n shards (some may be empty)."""
        if n < 1:
            raise ValueError("need at least one shard")
        buckets: list[list[tuple]] = [[] for _ in range(n)]
        for i, row in enumerate(self.rows):
            buckets[i % n].append(row)
        return [Table(self.columns, b) for b in buckets]

    def __len__(self) -> int:
        return len(self.rows)


class Aggregate:
    """Base class: a decomposable aggregate over one column.

    Subclasses define the local fold (``init``/``add``), the global
    combine (``merge``) and the final projection (``finalize``).
    ``column=None`` means the aggregate does not read a column (COUNT(*)).
    """

    kind = "agg"

    def __init__(self, column: str | None = None, distinct: bool = False):
        self.column = column
        self.distinct = distinct

    def init(self) -> Any:
        raise NotImplementedError

    def add(self, state: Any, value: Any) -> Any:
        raise NotImplementedError

    def merge(self, left: Any, right: Any) -> Any:
        raise NotImplementedError

    def finalize(self, state: Any) -> Any:
        raise NotImplementedError

    def label(self) -> str:
        inner = "*" if self.column is None else (
            ("DISTINCT " if self.distinct else "") + self.column
        )
        return f"{self.kind.upper()}({inner})"


class CountStar(Aggregate):
    kind = "count"

    def __init__(self):
        super().__init__(column=None)

    def init(self) -> int:
        return 0

    def add(self, state: int, value: Any) -> int:
        return state + 1  # COUNT(*) counts rows, NULLs included

    def merge(self, left: int, right: int) -> int:
        return left + right

    def finalize(self, state: int) -> int:
        return state


class Count(Aggregate):
    kind = "count"

    def init(self):
        return frozenset() if self.distinct else 0

    def add(self, state, value):
        if value is None:
            return state
        return state | {value} if self.distinct else state + 1

    def merge(self, left, right):
        return left | right if self.distinct else left + right

    def finalize(self, state):
        return len(state) if self.distinct else state


class Sum(Aggregate):
    kind = "sum"

    def init(self):
        # None = "no non-NULL value seen yet"; all-NULL input sums to NULL
        return frozenset() if self.distinct else None

    def add(self, state, value):
        if value is None:
            return state
        if self.distinct:
            return state | {value}
        return value if state is None else state + value

    def merge(self, left, right):
        if self.distinct:
            return left | right
        if left is None:
            return right
        if right is None:
            return left
        return left + right

    def finalize(self, state):
        if self.distinct:
            return sum(state) if state else None
        return state


class Avg(Aggregate):
    kind = "avg"

    def init(self):
        # (sum, count): an empty shard contributes (0, 0), i.e. nothing
        # to the numerator *or* the denominator of the global average
        return frozenset() if self.distinct else (0, 0)

    def add(self, state, value):
        if value is None:
            return state
        if self.distinct:
            return state | {value}
        return (state[0] + value, state[1] + 1)

    def merge(self, left, right):
        if self.distinct:
            return left | right
        return (left[0] + right[0], left[1] + right[1])

    def finalize(self, state):
        if self.distinct:
            return sum(state) / len(state) if state else None
        total, count = state
        return total / count if count else None


class _MinMax(Aggregate):
    def init(self):
        return None  # no non-NULL value seen yet

    def add(self, state, value):
        if value is None:
            return state
        return value if state is None else self._pick(state, value)

    def merge(self, left, right):
        if left is None:
            return right
        if right is None:
            return left
        return self._pick(left, right)

    def finalize(self, state):
        return state


class Min(_MinMax):
    kind = "min"

    @staticmethod
    def _pick(a, b):
        return a if a <= b else b


class Max(_MinMax):
    kind = "max"

    @staticmethod
    def _pick(a, b):
        return a if a >= b else b


def local_aggregate(table: Table, aggregates: Sequence[Aggregate]) -> list:
    """Local phase: fold one shard's rows into intermediate states."""
    states = [agg.init() for agg in aggregates]
    indexes = [
        None if agg.column is None else table.col(agg.column)
        for agg in aggregates
    ]
    for row in table.rows:
        for i, agg in enumerate(aggregates):
            value = None if indexes[i] is None else row[indexes[i]]
            states[i] = agg.add(states[i], value)
    return states


def merge_states(shard_states: Iterable[list], aggregates: Sequence[Aggregate]) -> list:
    """Global phase, step 1: merge all shard states into one state per aggregate."""
    merged = [agg.init() for agg in aggregates]
    for states in shard_states:
        for i, agg in enumerate(aggregates):
            merged[i] = agg.merge(merged[i], states[i])
    return merged


def distributed_aggregate(
    shards: Sequence[Table], aggregates: Sequence[Aggregate]
) -> list:
    """Full two-phase evaluation: local fold, global merge, finalize."""
    merged = merge_states(
        (local_aggregate(shard, aggregates) for shard in shards), aggregates
    )
    return [agg.finalize(state) for agg, state in zip(aggregates, merged)]


def direct_aggregate(table: Table, aggregates: Sequence[Aggregate]) -> list:
    """Single-phase reference evaluation over the whole table."""
    return distributed_aggregate([table], aggregates)
