from __future__ import annotations

from . import ast
from . import compiler, interpreter, parser, types


def compile_source(source: bytes, filename: str = "<input>"):
    program = parser.parse(source, filename)
    checker = types.check_program(program, source, filename)
    interp = interpreter.Interpreter()
    for node in iter_match_nodes(program):
        tree = compiler.compile_match(node, source, filename, checker)
        interp.set_tree(node, tree)
    return program, interp


def run_source(source: bytes, filename: str = "<input>"):
    program, interp = compile_source(source, filename)
    return interp.run_program(program)


def iter_match_nodes(program):
    for statement in program.statements:
        if isinstance(statement, ast.LetStmt):
            yield from find_match_nodes(statement.value)
        elif not isinstance(statement, ast.UnionDecl):
            yield from find_match_nodes(statement)


def find_match_nodes(node):
    if node is None:
        return
    if isinstance(node, ast.MatchExpr):
        yield node
        yield from find_match_nodes(node.scrutinee)
    for child in expression_children(node):
        yield from find_match_nodes(child)


def expression_children(expr):
    if isinstance(expr, ast.UnaryExpr):
        return [expr.operand]
    if isinstance(expr, ast.BinaryExpr):
        return [expr.left, expr.right]
    if isinstance(expr, ast.IfExpr):
        return [expr.condition, expr.consequent, expr.alternate]
    if isinstance(expr, ast.TupleExpr):
        return list(expr.elements)
    if isinstance(expr, ast.TagExpr):
        return list(expr.payload)
    if isinstance(expr, ast.MatchExpr):
        children = [expr.scrutinee]
        for clause in expr.clauses:
            if clause.guard is not None:
                children.append(clause.guard)
            children.append(clause.body)
        return children
    return []
