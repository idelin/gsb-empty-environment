"""Parameter-expression DAG with point and outward-rounded interval evaluation.

Nodes
-----
Const(value)                  constant leaf
Param(name)                   free parameter leaf (bound in an environment)
EpsPrime(q, eps, assumption)  the amplification formula node
Compose(children)             multi-round composition (sum of children)

Every node supports
    eval_point(env)     -> float
    eval_interval(env)  -> Interval   (outward-rounded, see intervals.py)

where env maps parameter names to floats (point eval) or to Intervals /
floats (interval eval).  Multi-round composition is only built through
``compose_rounds``, which rejects rounds whose sampling-model assumptions
are inconsistent -- the formula may not be stretched beyond its model.
"""

from __future__ import annotations

import math
from dataclasses import dataclass, field

from . import intervals, stable
from .assumptions import SamplingAssumption, check_composable


class Node:
    def eval_point(self, env: dict[str, float]) -> float:
        raise NotImplementedError

    def eval_interval(self, env: dict[str, object]) -> intervals.Interval:
        raise NotImplementedError


@dataclass(frozen=True)
class Const(Node):
    value: float

    def eval_point(self, env):
        return float(self.value)

    def eval_interval(self, env):
        return intervals.const(float(self.value))


@dataclass(frozen=True)
class Param(Node):
    name: str

    def eval_point(self, env):
        return float(env[self.name])

    def eval_interval(self, env):
        v = env[self.name]
        if isinstance(v, intervals.Interval):
            return v
        return intervals.const(float(v))


@dataclass(frozen=True)
class EpsPrime(Node):
    """eps' = log(1 + q*(exp(eps) - 1)) under an explicit sampling model."""

    q: Node
    eps: Node
    assumption: SamplingAssumption = field(default_factory=SamplingAssumption)

    def __post_init__(self):
        # Fail fast: never build a node the formula does not cover.
        self.assumption.check_formula_applicable()

    def eval_point(self, env):
        return stable.eps_prime(self.q.eval_point(env), self.eps.eval_point(env))

    def eval_interval(self, env):
        return intervals.eps_prime(
            self.q.eval_interval(env), self.eps.eval_interval(env)
        )


@dataclass(frozen=True)
class Compose(Node):
    """Sum of child eps' values (basic composition over rounds)."""

    children: tuple[Node, ...]

    def eval_point(self, env):
        return math.fsum(c.eval_point(env) for c in self.children)

    def eval_interval(self, env):
        acc = intervals.const(0.0)
        for c in self.children:
            acc = intervals.add(acc, c.eval_interval(env))
        return acc


def compose_rounds(rounds: list[EpsPrime]) -> Compose:
    """Build a multi-round composition, rejecting incompatible models."""
    assumptions = [r.assumption for r in rounds]
    check_composable(assumptions)
    if not all(isinstance(r, EpsPrime) for r in rounds):
        raise TypeError("compose_rounds expects EpsPrime nodes")
    return Compose(tuple(rounds))
