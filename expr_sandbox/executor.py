"""Public executor: parse, validate and evaluate guest expressions."""

import re
from dataclasses import dataclass

from .ast_nodes import Call, children
from .budget import Budget, Limits
from .builtins import BUILTINS
from .errors import ExprError
from .evaluator import evaluate
from .lexer import KEYWORDS
from .parser import parse
from .registry import FunctionRegistry
from .values import normalize

_NAME_RE = re.compile(r"[A-Za-z_][A-Za-z0-9_]*\Z")


@dataclass(frozen=True)
class ExecutionResult:
    value: object
    consumed: int          # total steps consumed by this execution
    parse_consumed: int    # steps spent parsing, validating, preparing variables
    eval_consumed: int     # steps spent evaluating


class Executor:
    """Executes guest expressions.  Safe to use reentrantly: all
    execution state is local to each ``execute`` call, so a registered
    host function may call back into the same executor (sharing the
    caller's Budget keeps the total budget intact)."""

    def __init__(self, limits=None, registry=None):
        self.limits = limits if limits is not None else Limits()
        self.registry = registry if registry is not None \
            else FunctionRegistry()

    def execute(self, source, variables=None, budget=None):
        limits = self.limits
        budget = budget if budget is not None else Budget(limits.max_steps)
        start_used = budget.used
        root = parse(source, budget, limits)
        functions = dict(BUILTINS)
        functions.update(self.registry.snapshot())
        _validate_functions(root, functions, budget)
        env = _prepare_variables(variables, limits, budget)
        after_parse = budget.used
        value = evaluate(root, env, functions, budget, limits)
        end = budget.used
        return ExecutionResult(value=value,
                               consumed=end - start_used,
                               parse_consumed=after_parse - start_used,
                               eval_consumed=end - after_parse)


def _validate_functions(root, functions, budget):
    """Reject unknown function names before evaluation, leftmost first."""
    calls = []
    stack = [root]
    while stack:
        node = stack.pop()
        budget.consume(1, "validation")
        if isinstance(node, Call):
            calls.append(node)
        stack.extend(children(node))
    calls.sort(key=lambda c: c.name_start)
    for call in calls:
        if call.name not in functions:
            raise ExprError("unknown function '%s'" % call.name,
                            call.name_start, call.name_end)


def _prepare_variables(variables, limits, budget):
    env = {}
    if variables is None:
        return env
    if not isinstance(variables, dict):
        raise TypeError("variables must be a dict of str to plain values")
    for key, value in variables.items():
        if not isinstance(key, str) or not _NAME_RE.match(key) \
                or key in KEYWORDS:
            raise ValueError("invalid variable name %r" % (key,))
        env[key] = normalize(value, limits, budget,
                             "variable '%s'" % key)
    return env
