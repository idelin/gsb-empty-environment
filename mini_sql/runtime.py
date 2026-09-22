from dataclasses import dataclass

from .errors import TypeRuntimeError, UnsupportedSQL


@dataclass(frozen=True)
class BColumn:
    level: int
    alias: str
    name: str


@dataclass(frozen=True)
class BLiteral:
    value: object


@dataclass(frozen=True)
class BUnary:
    op: str
    expr: object


@dataclass(frozen=True)
class BBinary:
    left: object
    op: str
    right: object


@dataclass(frozen=True)
class BMarker:
    name: str


class ExecRow:
    def __init__(self, columns=None, markers=None):
        self.columns = dict(columns or {})
        self.markers = dict(markers or {})

    def column(self, column):
        try:
            return self.columns[(column.level, column.alias, column.name)]
        except KeyError:
            raise TypeRuntimeError(f"internal error: missing column {column}") from None

    def with_column(self, key, value):
        columns = dict(self.columns)
        columns[key] = value
        return ExecRow(columns, self.markers)

    def with_marker(self, name, value):
        markers = dict(self.markers)
        markers[name] = value
        return ExecRow(self.columns, markers)


def eval_expr(expr, row):
    if isinstance(expr, BLiteral):
        return expr.value
    if isinstance(expr, BColumn):
        return row.column(expr)
    if isinstance(expr, BMarker):
        return row.markers[expr.name]
    if isinstance(expr, BUnary):
        value = eval_expr(expr.expr, row)
        if expr.op == "-":
            if value is None:
                return None
            return -numeric_value(value)
        if expr.op == "NOT":
            if value is None:
                return None
            return not value
    if isinstance(expr, BBinary):
        return eval_binary(expr, row)
    raise TypeRuntimeError(f"unknown bound expression {type(expr).__name__}")


def numeric_value(value):
    if isinstance(value, bool) or not isinstance(value, (int, float)):
        raise TypeRuntimeError(f"numeric value required but found {value!r}")
    return value


def eval_binary(expr, row):
    op = expr.op
    if op == "AND":
        left = eval_expr(expr.left, row)
        right = eval_expr(expr.right, row)
        if left is False or right is False:
            return False
        if left is None or right is None:
            return None
        return bool(left and right)
    if op == "OR":
        left = eval_expr(expr.left, row)
        right = eval_expr(expr.right, row)
        if left is True or right is True:
            return True
        if left is None or right is None:
            return None
        return bool(left or right)

    left = eval_expr(expr.left, row)
    right = eval_expr(expr.right, row)
    if op == "IS NULL":
        return left is None
    if op == "IS NOT NULL":
        return left is not None
    if left is None or right is None:
        return None

    if op in {"=", "!=", "<", ">", "<=", ">="}:
        return compare(left, right, op)
    if op in {"+", "-", "*", "/"}:
        return arithmetic(left, right, op)
    raise TypeRuntimeError(f"unknown operator {op!r}")


def compare(left, right, op):
    if isinstance(left, str) != isinstance(right, str):
        raise TypeRuntimeError(f"cannot compare {type(left).__name__} with {type(right).__name__}")
    if isinstance(left, bool) != isinstance(right, bool):
        raise TypeRuntimeError("cannot compare BOOLEAN with another type")
    if not isinstance(left, (str, bool, int, float)) or not isinstance(right, (str, bool, int, float)):
        raise TypeRuntimeError("only scalar BOOLEAN, NUMBER and TEXT values are supported")
    if op == "=":
        return left == right
    if op == "!=":
        return left != right
    if isinstance(left, bool):
        raise TypeRuntimeError("BOOLEAN only supports = and !=")
    if op == "<":
        return left < right
    if op == ">":
        return left > right
    if op == "<=":
        return left <= right
    return left >= right


def arithmetic(left, right, op):
    left = numeric_value(left)
    right = numeric_value(right)
    if op == "+":
        return left + right
    if op == "-":
        return left - right
    if op == "*":
        return left * right
    if right == 0:
        raise TypeRuntimeError("division by zero")
    return left / right


def is_true(value):
    return value is True


class Plan:
    children = ()

    def execute(self, db):
        raise NotImplementedError

    def explain(self, indent=0):
        prefix = "  " * indent
        lines = [f"{prefix}{self.describe()}"]
        for child in self.children:
            lines.extend(child.explain(indent + 1).splitlines())
        return "\n".join(lines)

    def describe(self):
        return type(self).__name__


class Scan(Plan):
    def __init__(self, table_name, alias, columns, level=0):
        self.table_name = table_name
        self.alias = alias
        self.columns = columns
        self.level = level

    def execute(self, db):
        relation = db.get(self.table_name)
        for values in relation.rows:
            row = ExecRow()
            for name, value in zip(relation.columns, values):
                row.columns[(self.level, self.alias, name)] = value
            yield row

    def describe(self):
        return f"Scan {self.table_name} AS {self.alias} (level={self.level})"


class Filter(Plan):
    def __init__(self, input_plan, predicate):
        self.input_plan = input_plan
        self.predicate = predicate
        self.children = (input_plan,)

    def execute(self, db):
        for row in self.input_plan.execute(db):
            if is_true(eval_expr(self.predicate, row)):
                yield row

    def describe(self):
        return f"Filter {format_expr(self.predicate)}"


class SemiJoin(Plan):
    def __init__(self, left, right, predicates, negated):
        self.left = left
        self.right = right
        self.predicates = predicates
        self.negated = negated
        self.children = (left, right)

    def execute(self, db):
        right_rows = list(self.right.execute(db))
        for left_row in self.left.execute(db):
            found = any(
                all(
                    is_true(eval_expr(predicate, combine_rows(left_row, right_row)))
                    for predicate in self.predicates
                )
                for right_row in right_rows
            )
            if found != self.negated:
                yield left_row

    def describe(self):
        name = "AntiJoin" if self.negated else "SemiJoin"
        return f"{name} {format_predicates(self.predicates)}"


class GroupJoin(Plan):
    def __init__(self, left, right, predicates, marker, aggregate, argument):
        self.left = left
        self.right = right
        self.predicates = predicates
        self.marker = marker
        self.aggregate = aggregate
        self.argument = argument
        self.children = (left, right)

    def execute(self, db):
        right_rows = list(self.right.execute(db))
        for left_row in self.left.execute(db):
            matches = [
                right_row for right_row in right_rows
                if all(is_true(eval_expr(predicate, combine_rows(left_row, right_row)))
                       for predicate in self.predicates)
            ]
            value = run_aggregate(self.aggregate, self.argument, matches)
            yield left_row.with_marker(self.marker, value)

    def describe(self):
        arg = "*" if self.aggregate == "COUNT" and self.argument is None else format_expr(self.argument)
        return f"GroupJoin {self.aggregate}({arg}) AS {self.marker}"


class Project(Plan):
    def __init__(self, input_plan, outputs=(), star_columns=(), schema=()):
        self.input_plan = input_plan
        self.outputs = outputs
        self.star_columns = tuple(star_columns)
        self.schema = tuple(schema)
        self.children = (input_plan,)

    def execute(self, db):
        for row in self.input_plan.execute(db):
            if self.star_columns:
                yield tuple(row.columns[key] for key in self.star_columns)
            else:
                yield tuple(eval_expr(expr, row) for name, expr in self.outputs)

    def describe(self):
        if self.star_columns:
            return "Project " + ", ".join(key[2] for key in self.star_columns)
        return "Project " + ", ".join(name for name, expr in self.outputs)


def combine_rows(left_row, right_row):
    return ExecRow({**left_row.columns, **right_row.columns}, left_row.markers)


def run_aggregate(name, argument, rows):
    if name == "COUNT" and argument is None:
        return len(rows)
    values = [eval_expr(argument, row) for row in rows]
    values = [value for value in values if value is not None]
    if name == "COUNT":
        return len(values)
    if not values:
        return None
    if name == "SUM":
        return sum(numeric_value(value) for value in values)
    if name == "AVG":
        return sum(numeric_value(value) for value in values) / len(values)
    if name in {"MIN", "MAX"}:
        try:
            return min(values) if name == "MIN" else max(values)
        except TypeError:
            raise TypeRuntimeError(f"{name} requires comparable values") from None
    raise UnsupportedSQL(f"unsupported aggregate {name}")


def format_expr(expr):
    if isinstance(expr, BLiteral):
        return repr(expr.value)
    if isinstance(expr, BColumn):
        return f"{expr.alias}.{expr.name}@{expr.level}"
    if isinstance(expr, BMarker):
        return f"${expr.name}"
    if isinstance(expr, BUnary):
        return f"{expr.op}({format_expr(expr.expr)})"
    if isinstance(expr, BBinary):
        return f"({format_expr(expr.left)} {expr.op} {format_expr(expr.right)})"
    return str(expr)


def format_predicates(predicates):
    if not predicates:
        return "TRUE"
    return " AND ".join(format_expr(predicate) for predicate in predicates)
