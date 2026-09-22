from __future__ import annotations

from dataclasses import dataclass
from typing import Any

from . import ast
from .errors import CompileError, render_diagnostic


@dataclass(frozen=True)
class Path:
    steps: tuple[Any, ...] = ()

    def child(self, step: Any) -> "Path":
        return Path(self.steps + (step,))


@dataclass(frozen=True)
class Column:
    ptype: ast.PType
    path: Path


@dataclass(frozen=True)
class Row:
    clause_index: int
    patterns: list[ast.Pattern | None]
    pattern_paths: dict[int, Path]
    env: dict[str, Path]
    guard: ast.Expr | None


@dataclass(frozen=True)
class Fail:
    constraints: tuple[tuple[Path, Any], ...]


@dataclass(frozen=True)
class Success:
    clause_index: int
    env: dict[str, Path]
    body: ast.Expr


@dataclass(frozen=True)
class GuardNode:
    condition: ast.Expr
    env: dict[str, Path]
    on_success: Any
    on_failure: Any


@dataclass(frozen=True)
class Switch:
    path: Path
    kind: str
    branches: list[tuple[Any, Any]]


Decision = Fail | Success | GuardNode | Switch


def wildcard(span: ast.Span | None = None) -> ast.WildcardPattern:
    return ast.WildcardPattern(span or ast.Span(0, 0))


class MatchCompiler:
    def __init__(self, source: bytes, filename: str, checker, clauses: list[ast.Clause]):
        self.source = source
        self.filename = filename
        self.unions = checker.unions
        self.constructors = checker.constructors
        self.clauses = clauses

    def compile_match(self, match: ast.MatchExpr) -> tuple[Decision, list[Fail], list[int]]:
        columns = [Column(match.scrutinee_type, Path())]
        rows = [
            Row(index, [clause.pattern], {0: Path()}, {}, clause.guard)
            for index, clause in enumerate(match.clauses)
        ]
        reachable: set[int] = set()
        tree = self.compile_matrix(columns, rows, (), reachable)
        return tree, collect_failures(tree), sorted(reachable)

    def compile_matrix(
        self,
        columns: list[Column],
        rows: list[Row],
        constraints: tuple[tuple[Path, Any], ...],
        reachable: set[int],
    ) -> Decision:
        if not rows:
            return Fail(constraints)

        reachable.add(rows[0].clause_index)

        if not columns:
            row = rows[0]
            env = self.final_bindings(row, [])
            success = Success(row.clause_index, env, self.clauses[row.clause_index].body)
            if row.guard is None:
                return success
            failure = self.compile_matrix([], rows[1:], constraints, reachable)
            return GuardNode(row.guard, env, success, failure)

        column_index = self.choose_column(columns, rows)
        if column_index is None:
            row = rows[0]
            env = self.final_bindings(row, columns)
            success = Success(row.clause_index, env, self.clauses[row.clause_index].body)
            if row.guard is None:
                return success
            remaining = [
                Row(
                    item.clause_index,
                    list(item.patterns),
                    dict(item.pattern_paths),
                    dict(item.env),
                    item.guard,
                )
                for item in rows[1:]
            ]
            failure = self.compile_matrix(columns, remaining, constraints, reachable)
            return GuardNode(row.guard, env, success, failure)
        column = columns[column_index]
        useful = self.useful_heads(rows, column_index)
        branches_spec = self.cases_for(column, useful)
        branches: list[tuple[Any, Any]] = []

        for key, added_columns, branch_constraint in branches_spec:
            child_columns = columns[:column_index] + added_columns + columns[column_index + 1 :]
            child_rows: list[Row] = []
            for row in rows:
                specialized = self.specialize(row, column, column_index, key, added_columns)
                if specialized is not None:
                    child_rows.append(specialized)
            child_constraints = constraints
            if branch_constraint is not None:
                child_constraints += ((column.path, branch_constraint),)
            child = self.compile_matrix(child_columns, child_rows, child_constraints, reachable)
            branches.append((key, child))

        return Switch(column.path, type_kind(column.ptype), branches)

    def choose_column(self, columns: list[Column], rows: list[Row]) -> int | None:
        first = rows[0]
        all_wildcards = all(isinstance(pattern, ast.WildcardPattern) for pattern in first.patterns)
        if first.guard is None and all_wildcards:
            return None
        for index, column in enumerate(columns):
            if any(is_concrete(row.patterns[index]) for row in rows):
                return index
        return None

    def final_bindings(self, row: Row, columns: list[Column]) -> dict[str, Path]:
        env = dict(row.env)
        for pattern, column in zip(row.patterns, columns):
            if isinstance(pattern, ast.VariablePattern):
                env[pattern.name] = row.pattern_paths[id_of(columns, column)]
        return env

    def useful_heads(self, rows: list[Row], column_index: int) -> list[ast.Pattern]:
        result: list[ast.Pattern] = []
        seen: set[str] = set()
        for row in rows:
            pattern = row.patterns[column_index]
            if is_concrete(pattern):
                identity = head_identity(pattern)
                if identity not in seen:
                    seen.add(identity)
                    result.append(pattern)
        return result

    def cases_for(self, column: Column, useful: list[ast.Pattern]) -> list[tuple[Any, list[Column], Any]]:
        ptype = column.ptype
        if isinstance(ptype, ast.TupleType):
            children = [
                Column(element, column.path.child(index))
                for index, element in enumerate(ptype.elements)
            ]
            return [("tuple", children, None)]

        if isinstance(ptype, ast.UserType):
            union = self.unions[ptype.name]
            result = []
            for constructor in union.constructors:
                payload_types = self.constructors[constructor.name].payload
                children = [
                    Column(payload_type, column.path.child(index))
                    for index, payload_type in enumerate(payload_types)
                ]
                result.append((constructor.name, children, ("tag", constructor.name)))
            return result

        if isinstance(ptype, ast.BuiltinType) and ptype.name == "bool":
            return [
                (False, [], ("literal", False)),
                (True, [], ("literal", True)),
            ]

        literals = []
        for pattern in useful:
            if isinstance(pattern, ast.LiteralPattern) and not isinstance(pattern.value, bool):
                if pattern.value not in literals:
                    literals.append(pattern.value)
        result = [(value, [], ("literal", value)) for value in literals]
        result.append(("other", [], ("other", tuple(literals))))
        return result

    def specialize(
        self,
        row: Row,
        column: Column,
        column_index: int,
        key: Any,
        added_columns: list[Column],
    ) -> Row | None:
        pattern = row.patterns[column_index]
        extra_patterns: list[ast.Pattern | None] = []
        extra_paths: list[Path] = []
        env = dict(row.env)

        if isinstance(pattern, ast.WildcardPattern):
            extra_patterns = [wildcard(pattern.span) for _ in added_columns]
            extra_paths = [child_column.path for child_column in added_columns]
        elif isinstance(pattern, ast.VariablePattern):
            env[pattern.name] = row.pattern_paths[column_index]
            extra_patterns = [wildcard(pattern.span) for _ in added_columns]
            extra_paths = [row.pattern_paths[column_index] for _ in added_columns]
        elif isinstance(pattern, ast.TuplePattern):
            if key != "tuple" or len(pattern.elements) != len(added_columns):
                return None
            extra_patterns = list(pattern.elements)
            extra_paths = [child_column.path for child_column in added_columns]
        elif isinstance(pattern, ast.TagPattern):
            if key != pattern.name:
                return None
            extra_patterns = list(pattern.payload)
            extra_paths = [
                row.pattern_paths[column_index] if not added_columns else column.path.child(index)
                for index in range(len(pattern.payload))
            ]
        elif isinstance(pattern, ast.LiteralPattern):
            if key == "other" or pattern.value != key:
                return None
            extra_paths = []
        else:
            return None

        patterns = row.patterns[:column_index] + extra_patterns + row.patterns[column_index + 1 :]
        new_paths: dict[int, Path] = {}
        for index in range(column_index):
            new_paths[index] = row.pattern_paths[index]
        for offset, path in enumerate(extra_paths):
            new_paths[column_index + offset] = path
        tail_start = column_index + 1
        for old_index, path in row.pattern_paths.items():
            if old_index > column_index:
                new_paths[column_index + len(extra_paths) + (old_index - tail_start)] = path
        return Row(row.clause_index, patterns, new_paths, env, row.guard)


def id_of(columns: list[Column], target: Column) -> int:
    for index, column in enumerate(columns):
        if column == target:
            return index
    raise KeyError("column not found")


def type_kind(ptype: ast.PType) -> str:
    if isinstance(ptype, ast.TupleType):
        return "tuple"
    if isinstance(ptype, ast.UserType):
        return "union"
    return ptype.name


def is_concrete(pattern: ast.Pattern | None) -> bool:
    return isinstance(pattern, (ast.TuplePattern, ast.TagPattern, ast.LiteralPattern))


def head_identity(pattern: ast.Pattern) -> str:
    if isinstance(pattern, ast.LiteralPattern):
        return repr(pattern.value)
    if isinstance(pattern, ast.TuplePattern):
        return f"tuple/{len(pattern.elements)}"
    return pattern.name


def collect_failures(tree: Decision) -> list[Fail]:
    if isinstance(tree, Fail):
        return [tree]
    if isinstance(tree, Success):
        return []
    if isinstance(tree, GuardNode):
        return collect_failures(tree.on_failure)
    result = []
    for _, child in tree.branches:
        result.extend(collect_failures(child))
    return result


def compile_match(match: ast.MatchExpr, source: bytes, filename: str, checker) -> Decision:
    compiler = MatchCompiler(source, filename, checker, match.clauses)
    tree, failures, reachable = compiler.compile_match(match)
    diagnostics: list[str] = []

    for index, clause in enumerate(match.clauses):
        if index not in reachable:
            diagnostics.append(render_diagnostic(source, clause.span, "unreachable match clause", filename))

    examples = []
    for failure in failures:
        example = failure_to_example(failure, checker, match.scrutinee_type)
        if example not in examples:
            examples.append(example)
    if examples:
        rendered = ", ".join(format_value(example) for example in examples[:5])
        diagnostics.append(
            render_diagnostic(source, match.span, f"match is not exhaustive; uncovered example: {rendered}", filename)
        )

    if diagnostics:
        raise CompileError(diagnostics)
    return tree


def failure_to_example(failure: Fail, checker, root_type: ast.PType):
    constraints = dict(failure.constraints)
    return build_value(Path(), root_type, constraints, checker)


def build_value(path: Path, ptype: ast.PType, constraints: dict[Path, Any], checker):
    if path in constraints:
        constraint = constraints[path]
        if constraint[0] == "literal":
            return constraint[1]
        if constraint[0] == "other":
            return other_value(ptype, constraint[1])

    if isinstance(ptype, ast.TupleType):
        return tuple(
            build_value(path.child(index), element, constraints, checker)
            for index, element in enumerate(ptype.elements)
        )

    if isinstance(ptype, ast.BuiltinType):
        return default_value(ptype.name, set())

    union = checker.unions[ptype.name]
    chosen = union.constructors[0]
    if path in constraints and constraints[path][0] == "tag":
        tag_name = constraints[path][1]
        chosen = next(constructor for constructor in union.constructors if constructor.name == tag_name)
    payload_types = checker.constructors[chosen.name].payload
    payload = tuple(
        build_value(path.child(index), payload_type, constraints, checker)
        for index, payload_type in enumerate(payload_types)
    )
    return (chosen.name, payload)


def other_value(ptype: ast.PType, excluded: tuple[Any, ...]):
    if isinstance(ptype, ast.BuiltinType) and ptype.name == "int":
        value = 0
        while value in excluded:
            value += 1
        return value
    value = ""
    while value in excluded:
        value += "_"
    return value


def default_value(name: str, _excluded: set[Any]):
    if name == "int":
        return 0
    if name == "bool":
        return False
    return ""


def format_value(value: Any) -> str:
    if isinstance(value, tuple):
        if len(value) == 2 and isinstance(value[0], str) and isinstance(value[1], tuple):
            name, payload = value
            if not payload:
                return name
            return f"{name}({', '.join(format_value(item) for item in payload)})"
        return "(" + ", ".join(format_value(item) for item in value) + ")"
    if isinstance(value, str):
        return '"' + value.replace("\\", "\\\\").replace('"', '\\"') + '"'
    if value is True:
        return "true"
    if value is False:
        return "false"
    return str(value)
