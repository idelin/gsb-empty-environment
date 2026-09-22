"""Relation-oriented execution of the supported mini-SQL subset.

Correlated subqueries are *decorrelated before evaluation*:

  * EXISTS (...)            -> hash semi-join, so outer rows are never copied
                               even when several inner rows match.
  * (SELECT agg ...)        -> group-join on the correlation keys; groups with
                               no match receive the empty-group aggregate value
                               (COUNT -> 0, SUM -> NULL, ...).

All relations are multisets (Python ``list`` of ``tuple`` rows); NULL is the
Python ``None`` value and comparisons follow SQL three-valued logic.
"""
from __future__ import annotations

from dataclasses import dataclass

from . import parser as ast
from .errors import (
    MiniSQLError,
    SQLSemanticError,
    SQLSyntaxError,
    UnsupportedSQLError,
)
from .parser import parse

_AGG_FUNCS = frozenset({"count", "sum", "avg", "min", "max"})


# --------------------------------------------------------------------------- #
# Catalog data structures
# --------------------------------------------------------------------------- #
@dataclass(frozen=True)
class Table:
    """A base relation: named columns plus a multiset of tuple rows."""
    name: str
    columns: tuple[str, ...]
    rows: list[tuple]

    def __post_init__(self) -> None:
        for row in self.rows:
            if len(row) != len(self.columns):
                raise ValueError(
                    "table %r has a row of width %d but %d columns declared"
                    % (self.name, len(row), len(self.columns))
                )


class Database:
    """Catalog of named base tables."""

    def __init__(self, tables: list[Table] | dict[str, Table] | None = None):
        self.tables: dict[str, Table] = {}
        if tables is not None:
            items = tables.values() if isinstance(tables, dict) else tables
            for table in items:
                self.add(table)

    def add(self, table: Table) -> None:
        key = table.name.lower()
        if key in self.tables:
            raise ValueError("duplicate table name %r" % table.name)
        self.tables[key] = table

    def get(self, name: str) -> Table:
        try:
            return self.tables[name.lower()]
        except KeyError:
            raise SQLSemanticError("unknown table %r" % name) from None


@dataclass(frozen=True)
class Result:
    columns: tuple[str, ...]
    rows: list[tuple]


# --------------------------------------------------------------------------- #
# Three-valued-logic scalar evaluation
# --------------------------------------------------------------------------- #
def as_bool(value: object) -> bool:
    """SQL WHERE truth: only TRUE passes; FALSE and NULL do not."""
    return value is True


def _arith(op: str, left: object, right: object) -> object:
    if left is None or right is None:
        return None
    if isinstance(left, bool) or isinstance(right, bool):
        raise SQLSemanticError("arithmetic on a boolean value is not allowed")
    if isinstance(left, (int, float)) and isinstance(right, (int, float)):
        if op == "+":
            return left + right
        if op == "-":
            return left - right
        if op == "*":
            return left * right
        if op == "/":
            if right == 0:
                return None
            quotient = left / right
            return int(quotient) if isinstance(left, int) and isinstance(right, int) and float(quotient).is_integer() else quotient
    raise SQLSemanticError(
        "operator %s cannot combine %r and %r" % (op, left, right)
    )


def _compare(op: str, left: object, right: object) -> object:
    if left is None or right is None:
        return None
    num = isinstance(left, (int, float)) and not isinstance(left, bool)
    num = num and isinstance(right, (int, float)) and not isinstance(right, bool)
    both_str = isinstance(left, str) and isinstance(right, str)
    both_bool = isinstance(left, bool) and isinstance(right, bool)
    if not (num or both_str or both_bool):
        raise SQLSemanticError(
            "values %r and %r are not comparable" % (left, right)
        )
    if op == "=":
        return left == right
    if op == "<>":
        return left != right
    if op == "<":
        return left < right
    if op == "<=":
        return left <= right
    if op == ">":
        return left > right
    if op == ">=":
        return left >= right
    raise AssertionError(op)


# --------------------------------------------------------------------------- #
# Scope chain: one frame per query nesting level (innermost last)
# --------------------------------------------------------------------------- #
class _BoundColumn:
    __slots__ = ("table", "column", "depth", "index")

    def __init__(self, table, column, depth, index):
        self.table = table
        self.column = column
        self.depth = depth
        self.index = index

    def eval(self, stack: list[tuple]) -> object:
        return stack[self.depth][self.index]


@dataclass(frozen=True)
class Scope:
    columns: tuple[tuple[str | None, str], ...]  # (table-alias-or-None, col)
    width: int


def _column_index(scope: Scope, table: str | None, name: str) -> int:
    if table is None:
        matches = [idx for idx, (_, col) in enumerate(scope.columns) if col == name]
        if not matches:
            raise SQLSemanticError("unknown column %r" % name)
        if len(matches) > 1:
            raise SQLSemanticError("ambiguous column %r" % name)
        return matches[0]
    table = table.lower()
    matches = [idx for idx, (tab, col) in enumerate(scope.columns)
               if tab is not None and tab.lower() == table and col == name]
    if not matches:
        aliases = {tab.lower() for tab, _ in scope.columns if tab is not None}
        if table in aliases:
            raise SQLSemanticError("table %r has no column %r" % (table, name))
        raise SQLSemanticError("unknown table alias %r" % table)
    return matches[0]


# --------------------------------------------------------------------------- #
# Bound (physical) expression tree
# --------------------------------------------------------------------------- #
class _Node:
    __slots__ = ("refs",)

    def eval(self, stack, agg_values=None):  # pragma: no cover - abstract
        raise NotImplementedError


class _BLit(_Node):
    __slots__ = ("value",)

    def __init__(self, value):
        self.value = value
        self.refs = ()

    def eval(self, stack, agg_values=None):
        return self.value


class _BCol(_Node):
    __slots__ = ("table", "column", "depth", "index")

    def __init__(self, table, column, depth, index):
        self.table = table
        self.column = column
        self.depth = depth
        self.index = index
        self.refs = (self,)

    def eval(self, stack, agg_values=None):
        return stack[self.depth][self.index]


class _BUnary(_Node):
    __slots__ = ("op", "operand", "refs")

    def __init__(self, op, operand):
        self.op = op
        self.operand = operand
        self.refs = operand.refs

    def eval(self, stack, agg_values=None):
        value = self.operand.eval(stack, agg_values)
        if self.op == "NOT":
            if value is None:
                return None
            return not value
        if self.op == "-":
            if value is None:
                return None
            return -value
        return value


class _BBinary(_Node):
    __slots__ = ("op", "left", "right", "refs")

    def __init__(self, op, left, right):
        self.op = op
        self.left = left
        self.right = right
        self.refs = left.refs + right.refs

    def eval(self, stack, agg_values=None):
        op = self.op
        left = self.left.eval(stack, agg_values)
        right = self.right.eval(stack, agg_values)
        if op in ("AND", "OR"):
            if op == "AND":
                if left is False or right is False:
                    return False
                if left is None or right is None:
                    return None
                return True
            if left is True or right is True:
                return True
            if left is None or right is None:
                return None
            return False
        if op in ("+", "-", "*", "/"):
            return _arith(op, left, right)
        return _compare(op, left, right)


class _BIsNull(_Node):
    __slots__ = ("operand", "negated", "refs")

    def __init__(self, operand, negated):
        self.operand = operand
        self.negated = negated
        self.refs = operand.refs

    def eval(self, stack, agg_values=None):
        value = self.operand.eval(stack, agg_values)
        result = value is None
        return not result if self.negated else result


class _BAgg(_Node):
    __slots__ = ("func", "arg", "star", "node_id", "refs")

    def __init__(self, func, arg, star):
        self.func = func
        self.arg = arg
        self.star = star
        self.node_id = id(self)
        self.refs = ()  # aggregates are opaque to free-column validation

    def eval(self, stack, agg_values=None):
        if agg_values is None or self.node_id not in agg_values:
            raise SQLSemanticError("aggregate evaluated outside aggregation")
        return agg_values[self.node_id]


class _BSub(_Node):
    """Decorrelated subquery probe: hash set (EXISTS) or dict (scalar).

    Coordinates are expressed in the *outer* query row (stack position 0),
    because the inner level disappears once the probe is decorrelated.
    """
    __slots__ = ("key_positions", "keys", "values", "default", "negated",
                 "is_exists", "refs")

    def __init__(self, key_positions, keys, values, default, negated, is_exists):
        # key_positions: outer-row column indexes for each correlation key.
        self.key_positions = key_positions
        self.keys = keys
        self.values = values
        self.default = default
        self.negated = negated
        self.is_exists = is_exists
        self.refs = tuple(_BoundColumn(None, "corr%d" % i, 0, index)
                          for i, index in enumerate(key_positions))

    def eval(self, stack, agg_values=None):
        outer = stack[0]
        key = tuple(outer[index] for index in self.key_positions)
        if self.is_exists:
            hit = key in self.keys
            return (not hit) if self.negated else hit
        return self.values.get(key, self.default)


# --------------------------------------------------------------------------- #
# AST helpers
# --------------------------------------------------------------------------- #
def _walk(node):
    """Yield an AST node and every expression node it contains."""
    yield node
    if isinstance(node, ast.Select):
        for item in node.items:
            if isinstance(item, ast.Star):
                continue
            yield from _walk(item.expr)
        if node.where is not None:
            yield from _walk(node.where)
        for col in node.group_by:
            yield col
        return
    if isinstance(node, (ast.SelectItem, ast.Star)):
        return
    if isinstance(node, ast.Literal):
        return
    if isinstance(node, ast.Column):
        return
    if isinstance(node, ast.Unary):
        yield from _walk(node.operand)
        return
    if isinstance(node, ast.Binary):
        yield from _walk(node.left)
        yield from _walk(node.right)
        return
    if isinstance(node, ast.IsNull):
        yield from _walk(node.operand)
        return
    if isinstance(node, ast.Aggregate):
        # Aggregate argument expressions belong to the aggregate's own scope;
        # they are validated separately and must not leak into outer checks.
        return
    if isinstance(node, (ast.Exists, ast.ScalarSubquery)):
        # Do not descend: a subquery body is a separate query validated on its
        # own. The node itself still signals "contains a subquery".
        return
    raise AssertionError(type(node))


def _has_subquery(query: ast.Select) -> bool:
    for node in _walk(query):
        if isinstance(node, (ast.Exists, ast.ScalarSubquery)):
            # The query node itself is yielded first; its own SELECT is not a
            # subquery, only Exists/ScalarSubquery descendants are.
            if node is not query:
                return True
    return False


def _has_aggregate(query: ast.Select) -> set[int]:
    ids: set[int] = set()
    for node in _walk(query):
        if isinstance(node, ast.Aggregate):
            ids.add(id(node))
    return ids


def _split_and(node):
    if node is None:
        return []
    if isinstance(node, ast.Binary) and node.op == "AND":
        return _split_and(node.left) + _split_and(node.right)
    return [node]


# --------------------------------------------------------------------------- #
# Binder: AST -> bound tree over a scope chain (scopes[0] is innermost)
# --------------------------------------------------------------------------- #
class _Binder:
    def __init__(self, db, scopes, allow_aggregate=False, allow_subquery=True):
        self.db = db
        self.scopes = scopes
        self.allow_aggregate = allow_aggregate
        self.allow_subquery = allow_subquery
        self.aggregates: dict[int, _BAgg] = {}

    # -- name resolution --------------------------------------------------- #
    def resolve(self, column: ast.Column) -> _BCol:
        name = column.name
        if column.table is None:
            # nearest scope wins; ambiguity is only reported within one scope
            for depth, scope in enumerate(self.scopes):
                hits = [idx for idx, (_, col) in enumerate(scope.columns)
                        if col == name]
                if hits:
                    if len(hits) > 1:
                        raise SQLSemanticError("ambiguous column %r" % name)
                    return _BCol(None, name, depth, hits[0])
            raise SQLSemanticError("unknown column %r" % name)
        label = column.table.lower()
        for depth, scope in enumerate(self.scopes):
            labels = {tab.lower() for tab, _ in scope.columns if tab is not None}
            if label in labels:
                hits = [idx for idx, (tab, col) in enumerate(scope.columns)
                        if tab is not None and tab.lower() == label and col == name]
                if not hits:
                    raise SQLSemanticError(
                        "table %r has no column %r" % (label, name))
                return _BCol(label, name, depth, hits[0])
        raise SQLSemanticError("unknown table alias %r" % label)

    # -- expression binding ------------------------------------------------ #
    def bind(self, node: ast._Node if False else object):
        if isinstance(node, ast.Literal):
            return _BLit(node.value)
        if isinstance(node, ast.Column):
            return self.resolve(node)
        if isinstance(node, ast.Unary):
            return _BUnary(node.op, self.bind(node.operand))
        if isinstance(node, ast.Binary):
            return _BBinary(node.op, self.bind(node.left), self.bind(node.right))
        if isinstance(node, ast.IsNull):
            return _BIsNull(self.bind(node.operand), node.negated)
        if isinstance(node, ast.Aggregate):
            if not self.allow_aggregate:
                raise SQLSemanticError(
                    "aggregate %s() is not allowed here" % node.func.upper())
            if node.star:
                if node.func != "count":
                    raise UnsupportedSQLError("only COUNT(*) is supported")
                bound = _BAgg(node.func, None, True)
            else:
                inner = _Binder(self.db, self.scopes[:1],
                                allow_aggregate=False, allow_subquery=False)
                arg = inner.bind(node.arg)
                bound = _BAgg(node.func, arg, False)
            self.aggregates[bound.node_id] = bound
            return bound
        if isinstance(node, (ast.Exists, ast.ScalarSubquery)):
            if not self.allow_subquery:
                raise UnsupportedSQLError("subqueries are not allowed here")
            return _prepare_subquery(node, self.db, self.scopes)
        raise SQLSemanticError("cannot bind expression %r" % (node,))


# --------------------------------------------------------------------------- #
# Aggregate state management
# --------------------------------------------------------------------------- #
def _new_state(func: str):
    if func == "count":
        return [0]
    if func == "avg":
        return [0, 0]            # total, non-null count
    return [None]               # sum / min / max running value


def _update_state(func: str, state: list, value: object, star: bool = False) -> None:
    if func == "count":
        # COUNT(*) counts every row; COUNT(expr) skips NULL arguments.
        if star or value is not None:
            state[0] += 1
        return
    if value is None:
        return
    numeric = isinstance(value, (int, float)) and not isinstance(value, bool)
    if func in ("sum", "avg") and not numeric:
        raise SQLSemanticError("%s requires a numeric argument" % func.upper())
    if func == "sum":
        state[0] = value if state[0] is None else state[0] + value
    elif func == "avg":
        state[0] += value
        state[1] += 1
    elif func in ("min", "max"):
        if state[0] is None or (value < state[0] if func == "min"
                                else value > state[0]):
            state[0] = value
    else:
        raise AssertionError(func)


def _finalize_state(func: str, state: list) -> object:
    if func == "count":
        return state[0]
    if func in ("sum", "min", "max"):
        return state[0]
    if func == "avg":
        if state[1] == 0:
            return None
        value = state[0] / state[1]
        return int(value) if float(value).is_integer() else value
    raise AssertionError(func)


# --------------------------------------------------------------------------- #
# Building a query level's scope from its FROM list (cartesian product)
# --------------------------------------------------------------------------- #
def _build_scope(db: Database, froms: list[ast.TableRef]):
    labels: list[str] = []
    columns: list[tuple[str | None, str]] = []
    row_sources: list[tuple[Table, int]] = []
    seen_labels: set[str] = set()
    for ref in froms:
        table = db.get(ref.name)
        label = (ref.alias or table.name).lower()
        if label in seen_labels:
            raise SQLSemanticError("duplicate table alias %r" % label)
        seen_labels.add(label)
        labels.append(label)
        for col in table.columns:
            columns.append((label, col))
        row_sources.append((table, len(table.columns)))
    scope = Scope(tuple(columns), sum(width for _, width in row_sources))

    def product() -> list[tuple]:
        rows: list[tuple] = [()]
        for table, _ in row_sources:
            rows = [row + inner for row in rows for inner in table.rows]
        return rows

    return scope, product


def _expr_text(node: object) -> str:
    if isinstance(node, ast.Literal):
        return "NULL" if node.value is None else str(node.value)
    if isinstance(node, ast.Column):
        return node.name if node.table is None else "%s.%s" % (node.table, node.name)
    if isinstance(node, ast.Aggregate):
        return (node.func.upper() + "(" + ("*" if node.star
                else _expr_text(node.arg)) + ")")
    if isinstance(node, ast.Unary):
        return node.op + " " + _expr_text(node.operand)
    if isinstance(node, ast.Binary):
        return "%s %s %s" % (_expr_text(node.left), node.op, _expr_text(node.right))
    if isinstance(node, ast.IsNull):
        return _expr_text(node.operand) + (" IS NOT NULL" if node.negated
                                          else " IS NULL")
    if isinstance(node, ast.SelectItem):
        return _expr_text(node.expr)
    return "expr"


# --------------------------------------------------------------------------- #
# Subquery decorrelation
# --------------------------------------------------------------------------- #
def _pure_side_cols(bound, depth: int):
    """If `bound` is one column at the given depth and nothing else, return it."""
    refs = bound.refs
    if not refs:
        return None
    if any(col.depth != depth for col in refs):
        return "mixed"
    if len(refs) == 1 and isinstance(bound, _BCol):
        return refs[0]
    return "expression"


def _prepare_subquery(node, db: Database, outer_scopes: list[Scope]):
    query = node.query
    is_exists = isinstance(node, ast.Exists)
    negated = node.negated if is_exists else False

    # Reject everything beyond the explicitly supported shape.
    if _has_subquery(query):
        raise UnsupportedSQLError("nested subqueries are not supported")
    for subnode in _walk(query.where) if query.where is not None else []:
        if isinstance(subnode, ast.Aggregate):
            raise UnsupportedSQLError("aggregates are not allowed in subquery WHERE")
        if isinstance(subnode, (ast.Exists, ast.ScalarSubquery)):
            raise UnsupportedSQLError("subqueries are not allowed in subquery WHERE")
    if query.group_by:
        raise UnsupportedSQLError("GROUP BY inside a subquery is not supported")
    if is_exists and any(isinstance(n, ast.Aggregate) for n in _walk(query)):
        raise UnsupportedSQLError(
            "EXISTS subqueries may not contain aggregates")

    inner_scope, inner_product = _build_scope(db, query.froms)
    scopes = [inner_scope] + outer_scopes
    classifier = _Binder(db, scopes, allow_aggregate=False, allow_subquery=False)

    # Split WHERE into inner-local predicates and correlation equalities.
    locals_bound: list[_Node] = []
    pairs: list[tuple[_BoundColumn, _BoundColumn]] = []
    for conjunct in _split_and(query.where):
        bound = classifier.bind(conjunct)
        if not (isinstance(bound, _BBinary) and bound.op == "="):
            if any(ref.depth >= 1 for ref in bound.refs):
                raise UnsupportedSQLError(
                    "only AND-joined equality predicates are supported for "
                    "correlation; %s is not an equality correlation"
                    % _expr_text(conjunct))
            locals_bound.append(bound)
            continue
        left = bound.left
        right = bound.right
        if all(ref.depth == 0 for ref in bound.refs):
            locals_bound.append(bound)
            continue
        left_col = _pure_side_cols(left, 0)
        right_col = _pure_side_cols(right, 1)
        if left_col in (None, "mixed", "expression") or \
                right_col in (None, "mixed", "expression"):
            swapped_l = _pure_side_cols(left, 1)
            swapped_r = _pure_side_cols(right, 0)
            if isinstance(swapped_l, _BoundColumn) and \
                    isinstance(swapped_r, _BoundColumn):
                inner_col, outer_col = swapped_r, swapped_l
            else:
                raise UnsupportedSQLError(
                    "correlation predicates must compare a plain inner column "
                    "with a plain outer column: %s" % _expr_text(conjunct))
        else:
            inner_col, outer_col = left_col, right_col
        pairs.append((inner_col, outer_col))

    # De-duplicate identical correlation equalities, keep inner index order.
    unique_pairs: list[tuple[_BoundColumn, _BoundColumn]] = []
    seen: set[tuple[int, int, int]] = set()
    for inner_col, outer_col in pairs:
        signature = (inner_col.index, outer_col.depth, outer_col.index)
        if signature not in seen:
            seen.add(signature)
            unique_pairs.append((inner_col, outer_col))
    pairs = sorted(unique_pairs, key=lambda pair: pair[0].index)

    filtered = [row for row in inner_product()
                if all(as_bool(pred.eval([row])) for pred in locals_bound)]

    for _, outer_col in pairs:
        if outer_col.depth != 1:
            raise UnsupportedSQLError(
                "only correlation with the immediately enclosing query is "
                "supported")
    key_positions = [outer_col.index for _, outer_col in pairs]

    if is_exists:
        # Decorrelate to a hash semi-join: a set of distinct non-null keys.
        keys = set()
        for row in filtered:
            key = tuple(row[inner_col.index] for inner_col, _ in pairs)
            if all(part is not None for part in key):
                keys.add(key)
        if not pairs:
            keys = {()} if filtered else set()
        return _BSub(key_positions, keys, None, False, negated, True)

    # Scalar aggregate subquery: validate the single projection expression.
    if len(query.items) != 1 or isinstance(query.items[0], ast.Star):
        raise UnsupportedSQLError(
            "scalar subquery must project exactly one expression")
    expr_node = query.items[0].expr
    agg_ids = {id(n) for n in _walk(query) if isinstance(n, ast.Aggregate)}
    if not agg_ids:
        raise UnsupportedSQLError(
            "non-aggregate scalar subqueries are not supported")
    scalar_binder = _Binder(db, scopes, allow_aggregate=True,
                            allow_subquery=False)
    bound_expr = scalar_binder.bind(expr_node)
    free_inner = {ref for ref in bound_expr.refs if ref.depth == 0}
    if free_inner:
        raise SQLSemanticError(
            "scalar subquery projection must be a pure aggregate expression")
    free_outer = {ref for ref in bound_expr.refs if ref.depth >= 1}
    if free_outer and pairs:
        raise UnsupportedSQLError(
            "correlated scalar subquery may not use outer columns outside its "
            "aggregate argument")

    aggs = scalar_binder.aggregates
    groups: dict[tuple, dict[int, list]] = {}
    order: list[tuple] = []
    for row in filtered:
        key = tuple(row[inner_col.index] for inner_col, _ in pairs)
        if pairs and any(part is None for part in key):
            continue
        states = groups.get(key)
        if states is None:
            states = {node_id: _new_state(bound.func)
                      for node_id, bound in aggs.items()}
            groups[key] = states
            order.append(key)
        for node_id, bound in aggs.items():
            value = None
            if not bound.star:
                value = bound.arg.eval([row])
            _update_state(bound.func, states[node_id], value, bound.star)

    if not pairs:
        # Uncorrelated: exactly one value over all (filtered) inner rows.
        states = {node_id: _new_state(bound.func)
                  for node_id, bound in aggs.items()}
        for row in filtered:
            for node_id, bound in aggs.items():
                value = None if bound.star else bound.arg.eval([row])
                _update_state(bound.func, states[node_id], value, bound.star)
        default = bound_expr.eval([None], {
            node_id: _finalize_state(bound.func, state)
            for node_id, state in states.items()
            for bound in [aggs[node_id]]
        })
        return _BSub(key_positions, None, {(): default}, default, False, False)

    values: dict[tuple, object] = {}
    defaults = {}
    for key in order:
        finalized = {node_id: _finalize_state(bound.func, groups[key][node_id])
                     for node_id, bound in aggs.items()}
        values[key] = bound_expr.eval([None], finalized)
    empty_states = {node_id: _new_state(bound.func)
                    for node_id, bound in aggs.items()}
    defaults = {node_id: _finalize_state(bound.func, empty_states[node_id])
                for node_id, bound in aggs.items()}
    default = bound_expr.eval([None], defaults)
    return _BSub(key_positions, None, values, default, False, False)


# --------------------------------------------------------------------------- #
# Top-level query execution
# --------------------------------------------------------------------------- #
def _has_where_aggregate(query: ast.Select) -> bool:
    if query.where is None:
        return False
    return any(isinstance(n, ast.Aggregate) for n in _walk(query.where))


def execute(db: Database, sql: str) -> Result:
    query = sql if isinstance(sql, ast.Select) else parse(sql)

    if not query.froms:
        raise UnsupportedSQLError("queries without FROM are not supported")

    scope, product = _build_scope(db, query.froms)
    scopes = [scope]

    if _has_where_aggregate(query):
        raise UnsupportedSQLError("aggregates are not allowed in WHERE")

    # WHERE is evaluated before any projection-level aggregation/subquery.
    where_binder = _Binder(db, scopes, allow_aggregate=False,
                           allow_subquery=True)
    where_bound = None
    if query.where is not None:
        where_bound = where_binder.bind(query.where)
        if any(ref.depth != 0 for ref in where_bound.refs):
            raise SQLSemanticError("outer reference at top level")

    filtered = [row for row in product()
                if where_bound is None or as_bool(where_bound.eval([row]))]

    star_expanded: list[ast.SelectItem] = []
    for item in query.items:
        if isinstance(item, ast.Star):
            star_expanded.extend(
                ast.SelectItem(ast.Column(tab, col), None)
                for tab, col in scope.columns
            )
        else:
            star_expanded.append(item)

    # Collect aggregate nodes from the projection only.
    project_agg_ids: set[int] = set()
    for item in star_expanded:
        for sub in _walk(item.expr):
            if isinstance(sub, ast.Aggregate):
                project_agg_ids.add(id(sub))

    group_cols_bound: list[_BCol] = []
    if query.group_by:
        group_binder = _Binder(db, scopes, allow_aggregate=False,
                               allow_subquery=False)
        group_cols_bound = [group_binder.bind(col) for col in query.group_by]

    is_aggregate = bool(group_cols_bound) or bool(project_agg_ids)

    output_names: list[str] = []
    output_rows: list[tuple] = []

    if is_aggregate:
        # One representative row plus all members per multiset of group keys.
        representatives: dict[tuple, tuple] = {}
        members_by_key: dict[tuple, list[tuple]] = {}
        order: list[tuple] = []
        for row in filtered:
            key = tuple(col.eval([row]) for col in group_cols_bound)
            if key not in representatives:
                representatives[key] = row
                members_by_key[key] = []
                order.append(key)
            members_by_key[key].append(row)

        project_binder = _Binder(db, scopes, allow_aggregate=True,
                                 allow_subquery=True)
        bound_items = [project_binder.bind(item.expr)
                       for item in star_expanded]
        agg_nodes = project_binder.aggregates

        allowed_indexes = {col.index for col in group_cols_bound}
        for bound in bound_items:
            for ref in bound.refs:
                if group_cols_bound:
                    if ref.depth != 0 or ref.index not in allowed_indexes:
                        raise SQLSemanticError(
                            "column %r must appear in GROUP BY or an aggregate"
                            % ref.column)
                elif ref.depth == 0:
                    raise SQLSemanticError(
                        "column %r must appear inside an aggregate"
                        % ref.column)

        for key in order:
            representative = representatives[key]
            states = {node_id: _new_state(bound.func)
                      for node_id, bound in agg_nodes.items()}
            for member in members_by_key[key]:
                for node_id, bound in agg_nodes.items():
                    value = None
                    if not bound.star:
                        value = bound.arg.eval([member])
                    _update_state(bound.func, states[node_id], value,
                                  bound.star)
            finalized = {node_id: _finalize_state(bound.func, states[node_id])
                         for node_id, bound in agg_nodes.items()}
            output_rows.append(tuple(
                bound.eval([representative], finalized)
                for bound in bound_items
            ))

        if not filtered and not group_cols_bound:
            states = {node_id: _new_state(bound.func)
                      for node_id, bound in agg_nodes.items()}
            finalized = {node_id: _finalize_state(bound.func, states[node_id])
                         for node_id, bound in agg_nodes.items()}
            dummy = (None,) * scope.width
            output_rows = [tuple(bound.eval([dummy], finalized)
                                 for bound in bound_items)]
    else:
        project_binder = _Binder(db, scopes, allow_aggregate=False,
                                 allow_subquery=True)
        bound_items = [project_binder.bind(item.expr)
                       for item in star_expanded]
        for row in filtered:
            output_rows.append(tuple(bound.eval([row])
                                     for bound in bound_items))

    for item in star_expanded:
        if item.alias is not None:
            output_names.append(item.alias)
        elif isinstance(item.expr, ast.Column):
            output_names.append(item.expr.name)
        else:
            output_names.append(_expr_text(item.expr))

    return Result(tuple(output_names), output_rows)
