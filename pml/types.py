from __future__ import annotations

from dataclasses import dataclass

from . import ast
from .errors import CompileError, render_diagnostic


@dataclass(frozen=True)
class ConstructorInfo:
    union_name: str
    payload: list[ast.PType]


@dataclass
class TypeChecker:
    source: bytes
    filename: str
    unions: dict[str, ast.UnionDecl] = None
    constructors: dict[str, ConstructorInfo] = None

    def __post_init__(self):
        self.unions = {}
        self.constructors = {}

    def fail(self, span, message: str):
        raise CompileError([render_diagnostic(self.source, span, message, self.filename)])

    def check_program(self, program: ast.Program) -> None:
        scope: dict[str, ast.PType] = {}
        declared_constructors: set[str] = set()
        for statement in program.statements:
            if isinstance(statement, ast.UnionDecl):
                if statement.name in self.unions:
                    self.fail(statement.span, f"duplicate union {statement.name}")
                seen = set()
                for constructor in statement.constructors:
                    if constructor.name in declared_constructors:
                        self.fail(constructor.span, f"duplicate constructor {constructor.name}")
                    if constructor.name in seen:
                        self.fail(constructor.span, f"duplicate constructor {constructor.name}")
                    if not constructor.name[:1].isupper():
                        self.fail(constructor.span, "constructor names must begin with an uppercase letter")
                    seen.add(constructor.name)
                    declared_constructors.add(constructor.name)
                    for payload_type in constructor.payload:
                        self.check_type_ref(payload_type)
                self.unions[statement.name] = statement
                for constructor in statement.constructors:
                    self.constructors[constructor.name] = ConstructorInfo(statement.name, list(constructor.payload))
            elif isinstance(statement, ast.LetStmt):
                value_type = self.check_expr(statement.value, scope)
                if statement.annotation is not None:
                    self.check_type_ref(statement.annotation)
                    if not self.types_compatible(value_type, statement.annotation):
                        self.fail(statement.span, f"expected {self.format_type(statement.annotation)}, got {self.format_type(value_type)}")
                scope[statement.name] = statement.annotation or value_type
            else:
                self.check_expr(statement, scope)

    def check_type_ref(self, ptype: ast.PType) -> None:
        if isinstance(ptype, ast.BuiltinType):
            if ptype.name not in {"int", "string", "bool"}:
                self.fail(ptype.span, f"unknown builtin type {ptype.name}")
        elif isinstance(ptype, ast.UserType):
            if ptype.name not in self.unions:
                self.fail(ptype.span, f"unknown type {ptype.name}")
        else:
            for element in ptype.elements:
                self.check_type_ref(element)

    def types_compatible(self, actual: ast.PType, expected: ast.PType) -> bool:
        if isinstance(actual, ast.TupleType) and isinstance(expected, ast.TupleType):
            return len(actual.elements) == len(expected.elements) and all(
                self.types_compatible(left, right)
                for left, right in zip(actual.elements, expected.elements)
            )
        if isinstance(actual, ast.UserType) and isinstance(expected, ast.UserType):
            return actual.name == expected.name
        if isinstance(actual, ast.BuiltinType) and isinstance(expected, ast.BuiltinType):
            return actual.name == expected.name
        return False

    def format_type(self, ptype: ast.PType) -> str:
        if isinstance(ptype, ast.BuiltinType):
            return ptype.name
        if isinstance(ptype, ast.UserType):
            return ptype.name
        return "(" + ", ".join(self.format_type(element) for element in ptype.elements) + ")"

    def check_expr(self, expr: ast.Expr, scope: dict[str, ast.PType]) -> ast.PType:
        if isinstance(expr, ast.IntLiteral):
            return ast.BuiltinType("int")
        if isinstance(expr, ast.StringLiteral):
            return ast.BuiltinType("string")
        if isinstance(expr, ast.BoolLiteral):
            return ast.BuiltinType("bool")
        if isinstance(expr, ast.Variable):
            if expr.name not in scope:
                self.fail(expr.span, f"unknown variable {expr.name}")
            return scope[expr.name]
        if isinstance(expr, ast.TupleExpr):
            return ast.TupleType([self.check_expr(element, scope) for element in expr.elements])
        if isinstance(expr, ast.TagExpr):
            if expr.name not in self.constructors:
                self.fail(expr.span, f"unknown constructor {expr.name}")
            info = self.constructors[expr.name]
            if len(expr.payload) != len(info.payload):
                self.fail(expr.span, f"constructor {expr.name} expects {len(info.payload)} payload values")
            for value, expected in zip(expr.payload, info.payload):
                actual = self.check_expr(value, scope)
                if not self.types_compatible(actual, expected):
                    self.fail(value.span, f"expected {self.format_type(expected)}, got {self.format_type(actual)}")
            return ast.UserType(info.union_name, expr.span)
        if isinstance(expr, ast.UnaryExpr):
            operand = self.check_expr(expr.operand, scope)
            if expr.op == "-":
                if not self.types_compatible(operand, ast.BuiltinType("int")):
                    self.fail(expr.span, "unary '-' requires int")
                return ast.BuiltinType("int")
            if not self.types_compatible(operand, ast.BuiltinType("bool")):
                self.fail(expr.span, "unary '!' requires bool")
            return ast.BuiltinType("bool")
        if isinstance(expr, ast.BinaryExpr):
            return self.check_binary(expr, scope)
        if isinstance(expr, ast.IfExpr):
            condition = self.check_expr(expr.condition, scope)
            if not self.types_compatible(condition, ast.BuiltinType("bool")):
                self.fail(expr.condition.span, "if condition must be bool")
            consequent = self.check_expr(expr.consequent, scope)
            alternate = self.check_expr(expr.alternate, scope)
            if not self.types_compatible(consequent, alternate):
                self.fail(expr.span, "if branches have different types")
            return consequent
        return self.check_match(expr, scope)

    def check_binary(self, expr: ast.BinaryExpr, scope: dict[str, ast.PType]) -> ast.PType:
        left = self.check_expr(expr.left, scope)
        right = self.check_expr(expr.right, scope)
        int_type = ast.BuiltinType("int")
        bool_type = ast.BuiltinType("bool")
        if expr.op in {"+", "-", "*", "/"}:
            if not self.types_compatible(left, int_type) or not self.types_compatible(right, int_type):
                self.fail(expr.span, f"'{expr.op}' requires two ints")
            return int_type
        if expr.op in {"<", "<=", ">", ">="}:
            if not self.types_compatible(left, int_type) or not self.types_compatible(right, int_type):
                self.fail(expr.span, f"'{expr.op}' requires two ints")
            return bool_type
        if expr.op in {"==", "!="}:
            if not self.same_comparable_type(left, right):
                self.fail(expr.span, "equality requires matching operand types")
            return bool_type
        if expr.op in {"&&", "||"}:
            if not self.types_compatible(left, bool_type) or not self.types_compatible(right, bool_type):
                self.fail(expr.span, f"'{expr.op}' requires two bools")
            return bool_type
        self.fail(expr.span, f"unsupported operator {expr.op}")

    def same_comparable_type(self, left: ast.PType, right: ast.PType) -> bool:
        return self.types_compatible(left, right)

    def check_match(self, expr: ast.MatchExpr, outer_scope: dict[str, ast.PType]) -> ast.PType:
        scrutinee_type = self.check_expr(expr.scrutinee, outer_scope)
        self.check_type_ref(expr.scrutinee_type)
        if not self.types_compatible(scrutinee_type, expr.scrutinee_type):
            self.fail(
                expr.scrutinee.span,
                f"expected {self.format_type(expr.scrutinee_type)}, got {self.format_type(scrutinee_type)}",
            )
        for clause in expr.clauses:
            scope = dict(outer_scope)
            self.check_pattern(clause.pattern, expr.scrutinee_type, scope, set())
            if clause.guard is not None:
                guard_type = self.check_expr(clause.guard, scope)
                if not self.types_compatible(guard_type, ast.BuiltinType("bool")):
                    self.fail(clause.guard.span, "guard must be bool")
            self.check_expr(clause.body, scope)
        return ast.BuiltinType("bool")

    def check_pattern(
        self,
        pattern: ast.Pattern,
        expected: ast.PType,
        scope: dict[str, ast.PType],
        bound: set[str],
    ) -> None:
        if isinstance(pattern, ast.WildcardPattern):
            return
        if isinstance(pattern, ast.VariablePattern):
            if pattern.name[:1].isupper():
                self.fail(pattern.span, "constructor used without payload parentheses")
            if pattern.name in bound:
                self.fail(pattern.span, f"duplicate binding {pattern.name}")
            bound.add(pattern.name)
            scope[pattern.name] = expected
            return
        if isinstance(pattern, ast.LiteralPattern):
            actual = self.literal_type(pattern.value)
            if not self.types_compatible(actual, expected):
                self.fail(pattern.span, f"expected {self.format_type(expected)}, got {self.format_type(actual)}")
            return
        if isinstance(pattern, ast.TuplePattern):
            if not isinstance(expected, ast.TupleType) or len(pattern.elements) != len(expected.elements):
                self.fail(pattern.span, f"expected {self.format_type(expected)}")
            for child, child_type in zip(pattern.elements, expected.elements):
                self.check_pattern(child, child_type, scope, bound)
            return
        if pattern.name not in self.constructors:
            self.fail(pattern.span, f"unknown constructor {pattern.name}")
        if not isinstance(expected, ast.UserType):
            self.fail(pattern.span, f"expected {self.format_type(expected)}")
        info = self.constructors[pattern.name]
        if info.union_name != expected.name:
            self.fail(pattern.span, f"constructor {pattern.name} does not belong to {expected.name}")
        if len(pattern.payload) != len(info.payload):
            self.fail(pattern.span, f"constructor {pattern.name} expects {len(info.payload)} patterns")
        for child, child_type in zip(pattern.payload, info.payload):
            self.check_pattern(child, child_type, scope, bound)

    def literal_type(self, value: int | str | bool) -> ast.PType:
        if isinstance(value, bool):
            return ast.BuiltinType("bool")
        if isinstance(value, int):
            return ast.BuiltinType("int")
        return ast.BuiltinType("string")


def check_program(program: ast.Program, source: bytes, filename: str) -> TypeChecker:
    checker = TypeChecker(source, filename)
    checker.check_program(program)
    return checker
