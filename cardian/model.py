"""Data model: statistics, predicates, queries, and SQL three-valued logic.

Statistics are *exact integer count constraints* over a finite integer domain:
  - exact row count per table,
  - a complete per-column histogram (values not listed have count 0),
  - an exact null count per column,
  - optional complete two-column joint counts (pairs not listed have count 0).
"""
from __future__ import annotations

import hashlib
import itertools
import json
from dataclasses import dataclass


def canonical_key(obj) -> str:
    """Stable fingerprint for a JSON-able object."""
    blob = json.dumps(obj, sort_keys=True, separators=(",", ":"))
    return hashlib.sha256(blob.encode("utf-8")).hexdigest()


def _as_int(value, ctx):
    if isinstance(value, bool) or not isinstance(value, int):
        raise ValueError(f"{ctx}: expected integer, got {value!r}")
    return value


def _key_int(token, ctx):
    """Parse a JSON object key back into an int."""
    if isinstance(token, bool):
        raise ValueError(f"{ctx}: bad value key {token!r}")
    if isinstance(token, int):
        return token
    try:
        return int(token)
    except (TypeError, ValueError):
        raise ValueError(f"{ctx}: bad value key {token!r}") from None


def _as_value(token, ctx):
    """A domain value or NULL (JSON null or the string \"null\")."""
    if token is None or token == "null":
        return None
    return _key_int(token, ctx)


# ---------------------------------------------------------------------------
# Statistics
# ---------------------------------------------------------------------------

@dataclass(frozen=True)
class Column:
    name: str
    domain: tuple          # sorted tuple of int values
    null_count: int
    histogram: dict        # int value -> exact count; missing values count as 0


@dataclass(frozen=True)
class JointCount:
    col_a: str
    col_b: str
    counts: dict           # (value_a, value_b) -> exact count; missing pairs are 0


@dataclass(frozen=True)
class TableStats:
    name: str
    row_count: int
    columns: tuple         # tuple[Column, ...] in declaration order
    joints: tuple          # tuple[JointCount, ...]

    @property
    def column_names(self):
        return [c.name for c in self.columns]

    def column(self, name):
        for c in self.columns:
            if c.name == name:
                return c
        raise ValueError(f"table {self.name!r} has no column {name!r}")

    def column_index(self):
        return {c.name: i for i, c in enumerate(self.columns)}

    def tuple_types(self):
        """All possible rows: cartesian product of (domain + NULL) per column."""
        doms = [list(c.domain) + [None] for c in self.columns]
        return list(itertools.product(*doms))


def _parse_column(name, raw, ctx):
    if not isinstance(raw, dict):
        raise ValueError(f"{ctx}: column spec must be an object")
    domain_raw = raw.get("domain")
    if not isinstance(domain_raw, list) or not domain_raw:
        raise ValueError(f"{ctx}: 'domain' must be a non-empty list of integers")
    domain = tuple(sorted({_as_int(v, f"{ctx}.domain") for v in domain_raw}))
    null_count = _as_int(raw.get("null_count", 0), f"{ctx}.null_count")
    if null_count < 0:
        raise ValueError(f"{ctx}.null_count: must be >= 0")
    histogram = {}
    for k, v in raw.get("histogram", {}).items():
        val = _key_int(k, f"{ctx}.histogram")
        if val not in domain:
            raise ValueError(f"{ctx}.histogram: value {val} not in domain")
        cnt = _as_int(v, f"{ctx}.histogram[{k!r}]")
        if cnt < 0:
            raise ValueError(f"{ctx}.histogram[{k!r}]: must be >= 0")
        histogram[val] = cnt
    return Column(name, domain, null_count, histogram)


def _parse_joint(raw, table_name, columns, ctx):
    cols = raw.get("columns")
    if not isinstance(cols, list) or len(cols) != 2 or cols[0] == cols[1]:
        raise ValueError(f"{ctx}: 'columns' must be two distinct column names")
    a, b = cols
    names = {c.name for c in columns}
    if a not in names or b not in names:
        raise ValueError(f"{ctx}: unknown column in {cols!r}")
    counts = {}
    dom = {c.name: set(c.domain) for c in columns}
    for k, v in raw.get("counts", {}).items():
        parts = str(k).split(",")
        if len(parts) != 2:
            raise ValueError(f"{ctx}: bad joint key {k!r} (expected 'va,vb')")
        pair = (_as_value(parts[0].strip(), ctx), _as_value(parts[1].strip(), ctx))
        for val, col in ((pair[0], a), (pair[1], b)):
            if val is not None and val not in dom[col]:
                raise ValueError(f"{ctx}: value {val} not in domain of {col!r}")
        cnt = _as_int(v, f"{ctx}.counts[{k!r}]")
        if cnt < 0:
            raise ValueError(f"{ctx}.counts[{k!r}]: must be >= 0")
        counts[pair] = cnt
    return JointCount(a, b, counts)


def parse_stats(stats_dict):
    """Parse the 'stats' JSON object into {table_name: TableStats}."""
    if not isinstance(stats_dict, dict) or not isinstance(stats_dict.get("tables"), dict):
        raise ValueError("stats must be an object with a 'tables' object")
    tables = {}
    for name, raw in stats_dict["tables"].items():
        ctx = f"tables.{name}"
        if not isinstance(raw, dict):
            raise ValueError(f"{ctx}: table spec must be an object")
        row_count = _as_int(raw.get("row_count"), f"{ctx}.row_count")
        if row_count < 0:
            raise ValueError(f"{ctx}.row_count: must be >= 0")
        cols_raw = raw.get("columns")
        if not isinstance(cols_raw, dict) or not cols_raw:
            raise ValueError(f"{ctx}: 'columns' must be a non-empty object")
        columns = tuple(
            _parse_column(cname, craw, f"{ctx}.columns.{cname}")
            for cname, craw in cols_raw.items()
        )
        joints = tuple(
            _parse_joint(jraw, name, columns, f"{ctx}.joint_counts[{i}]")
            for i, jraw in enumerate(raw.get("joint_counts", []))
        )
        tables[name] = TableStats(name, row_count, columns, joints)
    return tables


# ---------------------------------------------------------------------------
# Predicates (SQL three-valued logic: True / False / None)
# ---------------------------------------------------------------------------
# Predicate tuples:
#   ("true",)
#   ("eq", column, value)            value may be None (NULL)
#   ("range", column, lo, hi)        inclusive bounds; either may be None (NULL)
#   ("and", (sub, ...))  ("or", (sub, ...))  ("not", sub)

def parse_pred(node):
    if node is None:
        return ("true",)
    if not isinstance(node, dict) or len(node) != 1:
        raise ValueError(f"predicate must be a single-key object, got {node!r}")
    (op, arg), = node.items()
    if op == "eq":
        col, val = arg
        return ("eq", col, _as_value(val, "eq"))
    if op == "range":
        col, lo, hi = arg
        return ("range", col, _as_value(lo, "range"), _as_value(hi, "range"))
    if op == "and":
        return ("and", tuple(parse_pred(p) for p in arg))
    if op == "or":
        return ("or", tuple(parse_pred(p) for p in arg))
    if op == "not":
        return ("not", parse_pred(arg))
    raise ValueError(f"unknown predicate operator {op!r}")


def eval_pred(pred, col_index, row):
    """Three-valued evaluation; WHERE keeps only rows where this returns True."""
    op = pred[0]
    if op == "true":
        return True
    if op == "eq":
        _, col, val = pred
        x = row[col_index[col]]
        if x is None or val is None:
            return None
        return x == val
    if op == "range":
        _, col, lo, hi = pred
        x = row[col_index[col]]
        if x is None or lo is None or hi is None:
            return None
        return lo <= x <= hi
    if op == "and":
        result = True
        for sub in pred[1]:
            v = eval_pred(sub, col_index, row)
            if v is False:
                return False
            if v is None:
                result = None
        return result
    if op == "or":
        result = False
        for sub in pred[1]:
            v = eval_pred(sub, col_index, row)
            if v is True:
                return True
            if v is None:
                result = None
        return result
    if op == "not":
        v = eval_pred(pred[1], col_index, row)
        return None if v is None else (not v)
    raise ValueError(f"unknown predicate operator {op!r}")


# ---------------------------------------------------------------------------
# Queries
# ---------------------------------------------------------------------------

@dataclass(frozen=True)
class Select:
    table: str
    where: tuple


@dataclass(frozen=True)
class Join:
    left_table: str
    left_where: tuple
    right_table: str
    right_where: tuple
    left_key: str
    right_key: str


def parse_query(query_dict):
    if not isinstance(query_dict, dict):
        raise ValueError("query must be an object")
    if "select" in query_dict:
        s = query_dict["select"]
        return Select(s["table"], parse_pred(s.get("where")))
    if "join" in query_dict:
        j = query_dict["join"]
        on = j["on"]
        if not isinstance(on, list) or len(on) != 2:
            raise ValueError("join 'on' must be [left_key_column, right_key_column]")
        return Join(
            j["left"]["table"], parse_pred(j["left"].get("where")),
            j["right"]["table"], parse_pred(j["right"].get("where")),
            on[0], on[1],
        )
    raise ValueError("query must contain 'select' or 'join'")


def query_tables(query):
    if isinstance(query, Select):
        return [query.table]
    return [query.left_table, query.right_table]


def query_count(tables, query, rows_by_table):
    """Exact query cardinality on concrete relation instances (multisets)."""
    if isinstance(query, Select):
        table = tables[query.table]
        ci = table.column_index()
        return sum(
            1 for r in rows_by_table[query.table]
            if eval_pred(query.where, ci, r) is True
        )
    left = tables[query.left_table]
    right = tables[query.right_table]
    cil = left.column_index()
    cir = right.column_index()
    lk, rk = cil[query.left_key], cir[query.right_key]
    counts_l = {}
    for r in rows_by_table[query.left_table]:
        if eval_pred(query.left_where, cil, r) is True and r[lk] is not None:
            counts_l[r[lk]] = counts_l.get(r[lk], 0) + 1
    counts_r = {}
    for r in rows_by_table[query.right_table]:
        if eval_pred(query.right_where, cir, r) is True and r[rk] is not None:
            counts_r[r[rk]] = counts_r.get(r[rk], 0) + 1
    # Inner equi-join: NULL keys never match; multiplicity is the product.
    return sum(n * counts_r.get(k, 0) for k, n in counts_l.items())
