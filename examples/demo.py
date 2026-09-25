"""Runnable demo: python3 examples/demo.py"""

import os
import sys

sys.path.insert(0, os.path.join(os.path.dirname(__file__), os.pardir))

from expr_sandbox import (Budget, BudgetExhausted, Executor, ExprError,
                          FunctionRegistry, Limits)


def main():
    registry = FunctionRegistry()
    cap = registry.issue_capability("demo")
    registry.register(cap, "double", lambda x: x * 2)

    executor = Executor(registry=registry)

    print("== basic evaluation ==")
    samples = [
        "1 + 2 * 3",
        'if(1 < 2, "yes", "no")',
        "[1, 2] + [3]",
        "double(21)",
        "true or (1 / 0)",
        'join(split("a,b,c", ","), "-")',
        "sum(range(5))",
    ]
    for src in samples:
        result = executor.execute(src)
        print("  %-38r => %-12r (steps: %d)"
              % (src, result.value, result.consumed))

    print("== sandbox violations ==")
    violations = [
        ("unknown function", "nosuch(1)"),
        ("division by zero", "1 + (2 / 0)"),
        ("exponential concat", 'repeat("ab", 1000000000)'),
        ("huge range", "range(1000000)"),
        ("deep nesting", "(" * 500 + "1" + ")" * 500),
        ("host access attempt", "x.__class__"),
    ]
    for label, src in violations:
        try:
            executor.execute(src)
            print("  %-22s NOT BLOCKED!" % label)
        except ExprError as err:
            print("  %-22s blocked: %s" % (label, err))

    print("== budget enforcement ==")
    try:
        executor.execute('repeat("x", 5000)', budget=Budget(50))
    except BudgetExhausted as err:
        print("  repeat under tiny budget blocked: %s" % err)

    print("== reentrant callback with shared budget ==")
    budget = Budget(10000)

    def inc(x):
        return executor.execute("x + 1", {"x": x}, budget=budget).value

    registry.register(cap, "inc", inc)
    result = executor.execute("inc(inc(5))", budget=budget)
    print("  inc(inc(5)) => %d (shared budget used: %d steps)"
          % (result.value, budget.used))

    print("== custom limits ==")
    strict = Executor(limits=Limits(max_string=20, max_steps=1000))
    try:
        strict.execute('repeat("x", 100)')
    except ExprError as err:
        print("  strict string limit blocked: %s" % err)


if __name__ == "__main__":
    main()
