"""Iterative tree-walking evaluator.

Uses an explicit frame stack instead of host recursion, so deeply nested
expressions cannot crash the host interpreter.  ``and``/``or`` and ``if``
evaluate lazily: dead branches are never entered and never consume
budget.  BudgetExhausted always propagates as an exception; it is never
treated as a falsy value.
"""

import math

from .budget import MAX_INT
from .errors import ExprError

_MISSING = object()


class Context:
    """Handed to builtin/registered functions: budget, limits, span."""

    __slots__ = ("budget", "limits", "start", "end")

    def __init__(self, budget, limits, start, end):
        self.budget = budget
        self.limits = limits
        self.start = start
        self.end = end


def evaluate(root, variables, functions, budget, limits):
    stack = []          # frames: [node, stage, collected_values]
    node = root
    last = None
    while True:
        # --- enter one node ---
        budget.consume(1, "evaluation")
        kind = node.kind
        if kind == "literal":
            last = node.value
        elif kind == "name":
            value = variables.get(node.name, _MISSING)
            if value is _MISSING:
                raise ExprError("unknown variable '%s'" % node.name,
                                node.start, node.end)
            last = value
        else:
            if len(stack) + 1 > limits.max_depth:
                raise ExprError("expression too deep", node.start, node.end)
            first = _first_child(node)
            if first is None:
                last = _complete_leaf(node, functions, budget, limits)
            else:
                stack.append([node, 0, []])
                node = first
                continue
        # --- unwind: deliver ``last`` to parent frames ---
        while True:
            if not stack:
                return last
            frame = stack[-1]
            fnode = frame[0]
            fkind = fnode.kind
            if fkind == "unary":
                last = _apply_unary(fnode.op, last, fnode)
                stack.pop()
            elif fkind == "binary":
                if frame[1] == 0:
                    frame[1] = 1
                    if fnode.op == "and":
                        if not last:
                            stack.pop()
                            continue
                        node = fnode.right
                        break
                    if fnode.op == "or":
                        if last:
                            stack.pop()
                            continue
                        node = fnode.right
                        break
                    frame[2].append(last)
                    node = fnode.right
                    break
                if fnode.op in ("and", "or"):
                    stack.pop()  # result is the right operand's value
                else:
                    last = _apply_binary(fnode.op, frame[2][0], last,
                                         fnode, budget, limits)
                    stack.pop()
            elif fkind == "if":
                if frame[1] == 0:
                    frame[1] = 1
                    node = fnode.then if last else fnode.otherwise
                    break
                stack.pop()
            elif fkind == "call":
                frame[2].append(last)
                frame[1] += 1
                if frame[1] < len(fnode.args):
                    node = fnode.args[frame[1]]
                    break
                last = _invoke(fnode, frame[2], functions, budget, limits)
                stack.pop()
            else:  # list literal
                frame[2].append(last)
                frame[1] += 1
                if frame[1] < len(fnode.items):
                    node = fnode.items[frame[1]]
                    break
                last = list(frame[2])
                stack.pop()


def _first_child(node):
    kind = node.kind
    if kind == "unary":
        return node.operand
    if kind == "binary":
        return node.left
    if kind == "if":
        return node.cond
    if kind == "call":
        return node.args[0] if node.args else None
    if kind == "list":
        return node.items[0] if node.items else None
    return None


def _complete_leaf(node, functions, budget, limits):
    if node.kind == "call":
        return _invoke(node, [], functions, budget, limits)
    return []  # empty list literal


def _invoke(node, args, functions, budget, limits):
    func = functions.get(node.name)
    if func is None:
        raise ExprError("unknown function '%s'" % node.name,
                        node.name_start, node.name_end)
    return func(args, Context(budget, limits, node.start, node.end))


def _checked_number(value, node):
    if isinstance(value, bool):
        return value
    if isinstance(value, int):
        if value > MAX_INT or value < -MAX_INT:
            raise ExprError("number out of range", node.start, node.end)
        return value
    if not math.isfinite(value):
        raise ExprError("number out of range", node.start, node.end)
    return value


def _apply_unary(op, value, node):
    if op == "not":
        return not value
    if isinstance(value, (int, float)):
        return _checked_number(-value, node)
    raise ExprError("unsupported operands for -", node.start, node.end)


def _apply_binary(op, left, right, node, budget, limits):
    if op == "==" or op == "!=":
        if isinstance(left, str) and isinstance(right, str):
            budget.consume(len(left) + len(right), "comparison")
        elif isinstance(left, list) and isinstance(right, list):
            budget.consume(len(left) + len(right), "comparison")
        equal = left == right
        return equal if op == "==" else not equal
    if op in ("<", "<=", ">", ">="):
        if isinstance(left, (int, float)) and isinstance(right, (int, float)):
            pass
        elif isinstance(left, str) and isinstance(right, str):
            budget.consume(len(left) + len(right), "comparison")
        else:
            raise ExprError("unsupported operands for %s" % op,
                            node.start, node.end)
        if op == "<":
            return left < right
        if op == "<=":
            return left <= right
        if op == ">":
            return left > right
        return left >= right
    if op == "+":
        if isinstance(left, str) and isinstance(right, str):
            total = len(left) + len(right)
            if total > limits.max_string:
                raise ExprError("string too long", node.start, node.end)
            budget.consume(total, "string concatenation")
            return left + right
        if isinstance(left, list) and isinstance(right, list):
            total = len(left) + len(right)
            if total > limits.max_collection:
                raise ExprError("collection too large", node.start, node.end)
            budget.consume(total, "list concatenation")
            return left + right
        if isinstance(left, (int, float)) and isinstance(right, (int, float)):
            return _checked_number(left + right, node)
        raise ExprError("unsupported operands for +", node.start, node.end)
    if not (isinstance(left, (int, float))
            and isinstance(right, (int, float))):
        raise ExprError("unsupported operands for %s" % op,
                        node.start, node.end)
    if op == "-":
        return _checked_number(left - right, node)
    if op == "*":
        return _checked_number(left * right, node)
    if right == 0:
        raise ExprError("division by zero", node.start, node.end)
    if op == "/":
        return _checked_number(left / right, node)
    if op == "//":
        return _checked_number(left // right, node)
    return _checked_number(left % right, node)
