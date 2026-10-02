"""Shared helpers for the test-suite."""

import random

from invcheck.model import parse_model

VARS = ["a", "b", "c", "d"]


def random_ast(rng, variables, depth):
    if depth == 0 or rng.random() < 0.3:
        if rng.random() < 0.2:
            return ("const", rng.random() < 0.5)
        return ("var", rng.choice(variables))
    op = rng.choice(["not", "and", "or", "xor", "implies", "iff"])
    if op == "not":
        return ("not", random_ast(rng, variables, depth - 1))
    return (op, random_ast(rng, variables, depth - 1),
            random_ast(rng, variables, depth - 1))


def random_model(rng, n_vars):
    variables = VARS[:n_vars]
    transitions = []
    for i in range(rng.randint(1, 3)):
        assign = {}
        for v in variables:
            if rng.random() < 0.7:
                assign[v] = random_ast(rng, variables, 2)
        transitions.append({
            "name": f"t{i}",
            "guard": random_ast(rng, variables, 2),
            "assign": assign,
        })
    return parse_model({
        "name": "random",
        "variables": variables,
        "init": random_ast(rng, variables, 1),
        "transitions": transitions,
    })


MOD4 = {
    "name": "mod4",
    "variables": ["a", "b"],
    "init": "!a & !b",
    "transitions": [
        {"name": "wrap", "guard": "a & b", "assign": {"a": "false", "b": "false"}},
        {"name": "tick", "guard": "!(a & b)", "assign": {"b": "!b", "a": "a ^ b"}},
    ],
}

MOD3 = {
    "name": "mod3",
    "variables": ["a", "b"],
    "init": "!a & !b",
    "transitions": [
        {"name": "wrap", "guard": "a & !b", "assign": {"a": "false", "b": "false"}},
        {"name": "tick", "guard": "!(a & !b)", "assign": {"b": "!b", "a": "a ^ b"}},
    ],
}

LATCH = {
    "name": "latch",
    "variables": ["a", "b"],
    "init": "!a & !b",
    "transitions": [
        {"name": "set", "guard": "true", "assign": {"a": "a | b"}},
    ],
}


def all_assignments(variables):
    for i in range(1 << len(variables)):
        yield {v: bool(i >> k & 1) for k, v in enumerate(variables)}


def lex_key(order):
    return lambda asg: tuple(asg[v] for v in order)
