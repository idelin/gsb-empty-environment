"""Random small expression generator used by tests and the demo."""
from __future__ import annotations

from fractions import Fraction

DEFAULT_CONSTS = (
    Fraction(-2), Fraction(-1), Fraction(-1, 2), Fraction(0),
    Fraction(1, 2), Fraction(1), Fraction(2), Fraction(3),
)
DEFAULT_VARS = ("x", "y", "z")
OPS = ("add", "sub", "mul", "div", "neg", "if", "lt", "eq")


def random_spec(rng, variables=DEFAULT_VARS, consts=DEFAULT_CONSTS, depth=3):
    """Return a random nested-tuple spec suitable for Factory.parse."""
    if depth <= 0 or rng.random() < 0.35:
        if variables and rng.random() < 0.6:
            return ("var", rng.choice(tuple(variables)))
        return ("const", rng.choice(tuple(consts)))
    op = rng.choice(OPS)
    if op == "neg":
        return ("neg", random_spec(rng, variables, consts, depth - 1))
    if op == "if":
        return ("if",) + tuple(
            random_spec(rng, variables, consts, depth - 1) for _ in range(3))
    return (op, random_spec(rng, variables, consts, depth - 1),
            random_spec(rng, variables, consts, depth - 1))
