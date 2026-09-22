from __future__ import annotations

from dataclasses import dataclass
from typing import Any

from . import ast, compiler


@dataclass(frozen=True)
class TagValue:
    name: str
    payload: tuple[Any, ...]

    def render(self) -> str:
        if not self.payload:
            return self.name
        return f"{self.name}({', '.join(render_value(item) for item in self.payload)})"


class RuntimeError(Exception):
    pass


class Interpreter:
    def __init__(self):
        self.env: dict[str, Any] = {}
        self.match_trees: dict[int, compiler.Decision] = {}

    def run_program(self, program: ast.Program) -> Any:
        result = None
        for statement in program.statements:
            if isinstance(statement, ast.UnionDecl):
                continue
            if isinstance(statement, ast.LetStmt):
                self.env[statement.name] = self.evaluate(statement.value)
            else:
                result = self.evaluate(statement)
        return result

    def set_tree(self, expr: ast.MatchExpr, tree: compiler.Decision) -> None:
        self.match_trees[id(expr)] = tree

    def evaluate(self, expr: ast.Expr) -> Any:
        if isinstance(expr, ast.IntLiteral):
            return expr.value
        if isinstance(expr, ast.StringLiteral):
            return expr.value
        if isinstance(expr, ast.BoolLiteral):
            return expr.value
        if isinstance(expr, ast.Variable):
            if expr.name not in self.env:
                raise RuntimeError(f"unknown variable {expr.name}")
            return self.env[expr.name]
        if isinstance(expr, ast.TupleExpr):
            return tuple(self.evaluate(element) for element in expr.elements)
        if isinstance(expr, ast.TagExpr):
            return TagValue(expr.name, tuple(self.evaluate(element) for element in expr.payload))
        if isinstance(expr, ast.UnaryExpr):
            value = self.evaluate(expr.operand)
            if expr.op == "-":
                return -value
            if expr.op == "!":
                return not value
            raise RuntimeError(f"unsupported unary operator {expr.op}")
        if isinstance(expr, ast.BinaryExpr):
            return self.eval_binary(expr)
        if isinstance(expr, ast.IfExpr):
            if self.evaluate(expr.condition):
                return self.evaluate(expr.consequent)
            return self.evaluate(expr.alternate)
        if isinstance(expr, ast.MatchExpr):
            return self.eval_match(expr)
        raise RuntimeError("unsupported expression")

    def eval_binary(self, expr: ast.BinaryExpr) -> Any:
        left = self.evaluate(expr.left)
        right = self.evaluate(expr.right)
        if expr.op == "+":
            return left + right
        if expr.op == "-":
            return left - right
        if expr.op == "*":
            return left * right
        if expr.op == "/":
            if right == 0:
                raise RuntimeError("integer division by zero")
            sign = -1 if (left < 0) != (right < 0) else 1
            return sign * (abs(left) // abs(right))
        if expr.op == "==":
            return left == right
        if expr.op == "!=":
            return left != right
        if expr.op == "<":
            return left < right
        if expr.op == "<=":
            return left <= right
        if expr.op == ">":
            return left > right
        if expr.op == ">=":
            return left >= right
        if expr.op == "&&":
            return left and right
        if expr.op == "||":
            return left or right
        raise RuntimeError(f"unsupported operator {expr.op}")

    def eval_match(self, expr: ast.MatchExpr) -> Any:
        scrutinee = self.evaluate(expr.scrutinee)
        tree = self.match_trees.get(id(expr))
        if tree is None:
            raise RuntimeError("match was not compiled")
        return self.eval_decision(tree, {compiler.Path(): scrutinee})

    def eval_decision(self, node: compiler.Decision, paths: dict[compiler.Path, Any]) -> Any:
        if isinstance(node, compiler.Fail):
            raise RuntimeError("non-exhaustive match")
        if isinstance(node, compiler.Success):
            old_env = self.env
            self.env = dict(old_env)
            for name, path in node.env.items():
                self.env[name] = paths[path]
            try:
                return self.evaluate(node.body)
            finally:
                self.env = old_env
        if isinstance(node, compiler.GuardNode):
            guard_env = dict(self.env)
            for name, path in node.env.items():
                guard_env[name] = paths[path]
            old_env = self.env
            self.env = guard_env
            try:
                condition = bool(self.evaluate(node.condition))
            finally:
                self.env = old_env
            if condition:
                target = node.on_success
            else:
                target = node.on_failure
            return self.eval_decision(target, paths)

        value = paths[node.path]
        if node.kind == "tuple":
            for index, element in enumerate(value):
                paths[node.path.child(index)] = element
            return self.eval_decision(node.branches[0][1], paths)

        if node.kind == "union":
            payload_paths = {node.path.child(index): item for index, item in enumerate(value.payload)}
            paths.update(payload_paths)
            for tag_name, child in node.branches:
                if value.name == tag_name:
                    return self.eval_decision(child, paths)
            raise RuntimeError("unrecognized constructor")

        for key, child in node.branches:
            if key == "other":
                matched = all(value != literal_key for literal_key, _ in node.branches[:-1])
                if matched:
                    return self.eval_decision(child, paths)
            elif value == key:
                return self.eval_decision(child, paths)
        raise RuntimeError("non-exhaustive literal match")


def render_value(value: Any) -> str:
    if isinstance(value, TagValue):
        return value.render()
    if isinstance(value, tuple):
        return "(" + ", ".join(render_value(item) for item in value) + ")"
    if isinstance(value, str):
        return '"' + value.replace("\\", "\\\\").replace('"', '\\"') + '"'
    if value is True:
        return "true"
    if value is False:
        return "false"
    return str(value)
