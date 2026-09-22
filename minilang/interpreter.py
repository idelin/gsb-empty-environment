"""Tree-walking interpreter for the mini language."""
from __future__ import annotations

from . import ast_nodes as ast
from .incremental import parse


class LanguageError(Exception):
    def __init__(self, message, start=None, end=None):
        super().__init__(message)
        self.message = message
        self.start = start
        self.end = end

    def __str__(self):
        if self.start is not None:
            return f"{self.message} (at bytes [{self.start}, {self.end}))"
        return self.message


def collect_errors(tree) -> list:
    """All ErrorNodes in the tree, in source order."""
    found = []

    def walk(node):
        if isinstance(node, ast.ErrorNode):
            found.append(node)
        for child in node.child_nodes():
            walk(child)

    for decl in tree.decls:
        walk(decl)
    return found


class Env:
    def __init__(self, parent: "Env | None" = None):
        self.vars: dict = {}
        self.parent = parent

    def get(self, name, node=None):
        env = self
        while env is not None:
            if name in env.vars:
                return env.vars[name]
            env = env.parent
        if node is not None:
            raise LanguageError(f"undefined name '{name}'", node.start, node.end)
        raise LanguageError(f"undefined name '{name}'")

    def set(self, name, value):
        self.vars[name] = value


class Function:
    def __init__(self, name, params, body, env):
        self.name = name
        self.params = params
        self.body = body
        self.env = env

    def __repr__(self):
        return f"<fn {self.name}({', '.join(self.params)})>"


def _check_number(value, node):
    if isinstance(value, bool) or not isinstance(value, (int, float)):
        raise LanguageError(f"expected a number, got {value!r}",
                            node.start, node.end)
    return value


def evaluate(node, env: Env):
    if isinstance(node, ast.Num):
        return node.value
    if isinstance(node, ast.Name):
        return env.get(node.name, node)
    if isinstance(node, ast.Group):
        return evaluate(node.inner, env)
    if isinstance(node, ast.Unary):
        return -_check_number(evaluate(node.operand, env), node)
    if isinstance(node, ast.Binary):
        left = _check_number(evaluate(node.left, env), node.left)
        right = _check_number(evaluate(node.right, env), node.right)
        if node.op == "+":
            return left + right
        if node.op == "-":
            return left - right
        if node.op == "*":
            return left * right
        if node.op == "/":
            if right == 0:
                raise LanguageError("division by zero", node.start, node.end)
            return left / right
        if node.op == "==":
            return 1 if left == right else 0
        if node.op == "!=":
            return 1 if left != right else 0
        if node.op == "<":
            return 1 if left < right else 0
        if node.op == "<=":
            return 1 if left <= right else 0
        if node.op == ">":
            return 1 if left > right else 0
        if node.op == ">=":
            return 1 if left >= right else 0
        raise LanguageError(f"unknown operator {node.op!r}", node.start, node.end)
    if isinstance(node, ast.If):
        cond = _check_number(evaluate(node.cond, env), node.cond)
        return evaluate(node.then if cond != 0 else node.otherwise, env)
    if isinstance(node, ast.Call):
        callee = evaluate(node.callee, env)
        if not isinstance(callee, Function):
            raise LanguageError("attempted to call a non-function",
                                node.callee.start, node.callee.end)
        if len(node.args) != len(callee.params):
            raise LanguageError(
                f"'{callee.name}' expects {len(callee.params)} argument(s), "
                f"got {len(node.args)}", node.start, node.end)
        local = Env(callee.env)
        for param, arg in zip(callee.params, node.args):
            local.set(param, evaluate(arg, env))
        return evaluate(callee.body, local)
    if isinstance(node, ast.ErrorNode):
        raise LanguageError(f"cannot evaluate erroneous syntax: {node.message}",
                            node.start, node.end)
    raise LanguageError(f"cannot evaluate {type(node).__name__}",
                        node.start, node.end)


def interpret(tree) -> Env:
    """Evaluate all declarations, returning the global environment."""
    errors = collect_errors(tree)
    if errors:
        first = errors[0]
        raise LanguageError(f"syntax error: {first.message}",
                            first.start, first.end)
    env = Env()
    for decl in tree.decls:
        if isinstance(decl, ast.FnDecl):
            env.set(decl.name, Function(decl.name, decl.params, decl.body, env))
        elif isinstance(decl, ast.LetDecl):
            env.set(decl.name, evaluate(decl.value, env))
    return env


def run(source) -> Env:
    """Parse and interpret ``source`` in one step."""
    return interpret(parse(source))


def evaluate_expression(expr_source, env: Env):
    """Evaluate a standalone expression string in an existing environment."""
    tree = parse("let __result__ = " + expr_source)
    errors = collect_errors(tree)
    if errors:
        first = errors[0]
        raise LanguageError(f"syntax error: {first.message}",
                            first.start, first.end)
    decl = tree.decls[0]
    return evaluate(decl.value, env)
