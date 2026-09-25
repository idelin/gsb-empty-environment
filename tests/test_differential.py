"""Differential testing against an independent interpreter.

``Oracle`` is a small, deliberately straightforward *recursive* evaluator
written independently from the iterative machine in expr_sandbox.  For
finite small expressions we compare:

* the produced value,
* the reported error (message and UTF-8 byte span) and its ordering,
* the actual number of budget steps consumed during evaluation.
"""

import math
import random
import unittest

from expr_sandbox import Budget, Executor, ExprError, Limits
from expr_sandbox.parser import parse

MAX_INT = 2 ** 62


class OracleError(Exception):
    def __init__(self, message, start=None, end=None):
        super().__init__(message)
        self.message = message
        self.start = start
        self.end = end


class Oracle:
    """Independent recursive interpreter over the parsed AST."""

    def __init__(self, variables, limits):
        self.variables = variables
        self.limits = limits
        self.steps = 0

    def _consume(self, amount):
        self.steps += amount
        if self.steps > self.limits.max_steps:
            raise OracleError("step budget exhausted")

    def _num(self, value, node):
        if isinstance(value, bool):
            return value
        if isinstance(value, int):
            if abs(value) > MAX_INT:
                raise OracleError("number out of range", node.start, node.end)
            return value
        if not math.isfinite(value):
            raise OracleError("number out of range", node.start, node.end)
        return value

    def eval(self, node):
        self._consume(1)
        kind = node.kind
        if kind == "literal":
            return node.value
        if kind == "name":
            if node.name not in self.variables:
                raise OracleError("unknown variable '%s'" % node.name,
                                  node.start, node.end)
            return self.variables[node.name]
        if kind == "unary":
            value = self.eval(node.operand)
            if node.op == "not":
                return not value
            if not isinstance(value, (int, float)):
                raise OracleError("unsupported operands for -",
                                  node.start, node.end)
            return self._num(-value, node)
        if kind == "if":
            return self.eval(node.then if self.eval(node.cond)
                             else node.otherwise)
        if kind == "list":
            return [self.eval(item) for item in node.items]
        if kind == "binary":
            op = node.op
            left = self.eval(node.left)
            if op == "and":
                return self.eval(node.right) if left else left
            if op == "or":
                return left if left else self.eval(node.right)
            right = self.eval(node.right)
            return self._binary(op, left, right, node)
        raise AssertionError("oracle does not support %r" % kind)

    def _binary(self, op, left, right, node):
        if op in ("==", "!="):
            if isinstance(left, str) and isinstance(right, str):
                self._consume(len(left) + len(right))
            elif isinstance(left, list) and isinstance(right, list):
                self._consume(len(left) + len(right))
            equal = left == right
            return equal if op == "==" else not equal
        if op in ("<", "<=", ">", ">="):
            if isinstance(left, (int, float)) \
                    and isinstance(right, (int, float)):
                pass
            elif isinstance(left, str) and isinstance(right, str):
                self._consume(len(left) + len(right))
            else:
                raise OracleError("unsupported operands for %s" % op,
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
                if total > self.limits.max_string:
                    raise OracleError("string too long",
                                      node.start, node.end)
                self._consume(total)
                return left + right
            if isinstance(left, list) and isinstance(right, list):
                total = len(left) + len(right)
                if total > self.limits.max_collection:
                    raise OracleError("collection too large",
                                      node.start, node.end)
                self._consume(total)
                return left + right
            if isinstance(left, (int, float)) \
                    and isinstance(right, (int, float)):
                return self._num(left + right, node)
            raise OracleError("unsupported operands for +",
                              node.start, node.end)
        if not (isinstance(left, (int, float))
                and isinstance(right, (int, float))):
            raise OracleError("unsupported operands for %s" % op,
                              node.start, node.end)
        if op == "-":
            return self._num(left - right, node)
        if op == "*":
            return self._num(left * right, node)
        if right == 0:
            raise OracleError("division by zero", node.start, node.end)
        if op == "/":
            return self._num(left / right, node)
        if op == "//":
            return self._num(left // right, node)
        return self._num(left % right, node)


VARIABLES = {"x": 2, "y": -3, "s": "ab", "t": "", "lst": [1, 2],
             "b": True, "n": None}


def gen_leaf(rng):
    r = rng.random()
    if r < 0.25:
        return str(rng.randint(0, 9))
    if r < 0.35:
        return rng.choice(["0.5", "1.5", "2.5"])
    if r < 0.50:
        return rng.choice(['""', '"a"', '"bc"', '"abc"'])
    if r < 0.60:
        return rng.choice(["true", "false"])
    if r < 0.65:
        return "null"
    return rng.choice(sorted(VARIABLES))


def gen_source(rng, depth):
    if depth <= 0 or rng.random() < 0.3:
        return gen_leaf(rng)
    r = rng.random()
    if r < 0.15:
        op = rng.choice(["-", "not "])
        return op + "(" + gen_source(rng, depth - 1) + ")"
    if r < 0.20:
        return "if(%s, %s, %s)" % (gen_source(rng, depth - 1),
                                   gen_source(rng, depth - 1),
                                   gen_source(rng, depth - 1))
    if r < 0.30:
        count = rng.randint(0, 3)
        return "[" + ", ".join(gen_source(rng, depth - 1)
                               for _ in range(count)) + "]"
    op = rng.choice(["+", "-", "*", "/", "//", "%", "==", "!=",
                     "<", "<=", ">", ">=", "and", "or"])
    return "(%s %s %s)" % (gen_source(rng, depth - 1), op,
                           gen_source(rng, depth - 1))


def run_executor(executor, src, variables, limits):
    try:
        result = executor.execute(src, dict(variables))
        return ("ok", result.value, result.eval_consumed)
    except ExprError as err:
        return ("err", err.message, err.start, err.end)


def run_oracle(src, variables, limits):
    budget = Budget(limits.max_steps)
    try:
        root = parse(src, budget, limits)
    except ExprError as err:
        return ("err", err.message, err.start, err.end)
    oracle = Oracle(variables, limits)
    try:
        value = oracle.eval(root)
        return ("ok", value, oracle.steps)
    except OracleError as err:
        return ("err", err.message, err.start, err.end)


class TestDifferential(unittest.TestCase):
    def test_random_small_expressions_match_oracle(self):
        rng = random.Random(20260925)
        limits = Limits()
        executor = Executor(limits=limits)
        counts = {"ok": 0, "err": 0}
        for _ in range(1000):
            src = gen_source(rng, 4)
            expected = run_oracle(src, VARIABLES, limits)
            got = run_executor(executor, src, VARIABLES, limits)
            counts[expected[0]] += 1
            self.assertEqual(got, expected, "mismatch for %r" % src)
        # both outcomes were exercised
        self.assertGreater(counts["ok"], 100)
        self.assertGreater(counts["err"], 100)

    def test_error_order_is_left_to_right(self):
        limits = Limits()
        executor = Executor(limits=limits)
        cases = [
            "(1 / 0) + unknownvar",
            "unknownvar + (1 / 0)",
            "(1 / 0) * (2 / 0)",
            "if(true, 1 / 0, unknownvar)",
            "if(false, unknownvar, 1 / 0)",
            "(unknownvar / 0) + (1 / 0)",
            '"a" - 1 + (1 / 0)',
        ]
        for src in cases:
            expected = run_oracle(src, VARIABLES, limits)
            got = run_executor(executor, src, VARIABLES, limits)
            self.assertEqual(expected[0], "err", src)
            self.assertEqual(got, expected, "mismatch for %r" % src)

    def test_consumption_matches_oracle_exactly(self):
        rng = random.Random(7)
        limits = Limits()
        executor = Executor(limits=limits)
        checked = 0
        for _ in range(300):
            src = gen_source(rng, 3)
            expected = run_oracle(src, VARIABLES, limits)
            got = run_executor(executor, src, VARIABLES, limits)
            self.assertEqual(got, expected, "mismatch for %r" % src)
            if got[0] == "ok":
                checked += 1
        self.assertGreater(checked, 100)


if __name__ == "__main__":
    unittest.main()
