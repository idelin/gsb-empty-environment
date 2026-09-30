"""Parameter expression DAG with rigorous interval evaluation.

Expressions are built from constants, named parameters and operators
(add / mul / expm1 / log1p, plus the composite ``SubsampleAmp`` node).
Evaluating a node binds parameters to (point or ranged) intervals and
propagates outward-rounded intervals, so the result rigorously encloses
the exact mathematical value of the expression.

The ``SubsampleAmp`` node re-validates the applicability conditions of the
Poisson amplification formula over the *whole* input intervals; if any
admissible parameter value falls outside the model (e.g. q > 1 somewhere
in the interval), evaluation is refused with ApplicabilityError instead of
silently applying the formula.

``compose_rounds`` builds the basic-composition expression

    total_eps = sum_i log(1 + q_i * (exp(eps_i) - 1))

and refuses to compose rounds whose parameters violate the model.
"""

from __future__ import annotations

from dataclasses import dataclass
from decimal import Decimal

from .intervals import Interval
from .validation import (
    ApplicabilityError,
    SamplingModel,
    _normalize_model,
    validate_subsample_applicability,
)

# Mirror of core._OVERFLOW_GUARD: above this the algebraically identical,
# overflow-free form eps + log(q + (1 - q)*exp(-eps)) is used.  (Decimal
# arithmetic itself would not overflow until ~1e9, but using the stable
# form keeps the DAG semantics identical to the float path.)
_FORM_SWITCH = Decimal(700)


def amp_interval(eps_iv: Interval, q_iv: Interval) -> Interval:
    """Outward-rounded interval for log(1 + q*(exp(eps) - 1)).

    Requires pre-validated operands (see ``_validate_interval_domain``).
    Exact boundaries are preserved: q = 0 or eps = 0 -> [0, 0];
    q = 1 -> eps interval unchanged.
    """
    if (q_iv.is_point and q_iv.lo == 0) or (eps_iv.is_point and eps_iv.lo == 0):
        return Interval(0)
    if q_iv.is_point and q_iv.lo == 1:
        return eps_iv
    if eps_iv.hi <= _FORM_SWITCH:
        raw = (q_iv * eps_iv.expm1()).log1p()
    else:
        one = Interval(1)
        raw = eps_iv + (q_iv + (one - q_iv) * (-eps_iv).exp()).ln()
    # Universal bounds 0 <= amp <= eps hold for every 0 <= q <= 1;
    # intersecting with them is a rigorous tightening (it also absorbs the
    # -inf lower bound that log1p(-1) can produce under underflow).
    return raw.intersect(Decimal(0), eps_iv.hi)


def _validate_interval_domain(
    eps_iv: Interval, q_iv: Interval, model: object, base_delta: float
) -> None:
    _normalize_model(model)
    if isinstance(base_delta, bool) or float(base_delta) != 0.0:
        raise ApplicabilityError(
            "the formula requires a pure eps-DP base mechanism (delta = 0)"
        )
    if eps_iv.lo < 0:
        raise ApplicabilityError(
            f"eps interval [{eps_iv.lo}, {eps_iv.hi}] goes below 0"
        )
    if q_iv.lo < 0 or q_iv.hi > 1:
        raise ApplicabilityError(
            f"q interval [{q_iv.lo}, {q_iv.hi}] is not contained in [0, 1]"
        )


class Node:
    """Base class of the expression DAG."""

    def evaluate(self, env: dict[str, object] | None = None) -> Interval:
        raise NotImplementedError


def _as_node(value: object) -> "Node":
    return value if isinstance(value, Node) else Const(value)


@dataclass(frozen=True)
class Const(Node):
    value: object

    def evaluate(self, env: dict[str, object] | None = None) -> Interval:
        return Interval(self.value)


@dataclass(frozen=True)
class Param(Node):
    name: str

    def evaluate(self, env: dict[str, object] | None = None) -> Interval:
        if env is None or self.name not in env:
            raise KeyError(f"no binding for parameter {self.name!r}")
        binding = env[self.name]
        return binding if isinstance(binding, Interval) else Interval(binding)


@dataclass(frozen=True)
class Add(Node):
    left: Node
    right: Node

    def evaluate(self, env: dict[str, object] | None = None) -> Interval:
        return self.left.evaluate(env) + self.right.evaluate(env)


@dataclass(frozen=True)
class Mul(Node):
    left: Node
    right: Node

    def evaluate(self, env: dict[str, object] | None = None) -> Interval:
        return self.left.evaluate(env) * self.right.evaluate(env)


@dataclass(frozen=True)
class ExpM1(Node):
    operand: Node

    def evaluate(self, env: dict[str, object] | None = None) -> Interval:
        return self.operand.evaluate(env).expm1()


@dataclass(frozen=True)
class Log1P(Node):
    operand: Node

    def evaluate(self, env: dict[str, object] | None = None) -> Interval:
        return self.operand.evaluate(env).log1p()


@dataclass(frozen=True)
class SubsampleAmp(Node):
    """Node computing log(1 + q*(exp(eps) - 1)) as a rigorous interval."""

    eps: Node
    q: Node
    model: object = SamplingModel.POISSON
    base_delta: float = 0.0

    def evaluate(self, env: dict[str, object] | None = None) -> Interval:
        eps_iv = self.eps.evaluate(env)
        q_iv = self.q.evaluate(env)
        _validate_interval_domain(eps_iv, q_iv, self.model, self.base_delta)
        return amp_interval(eps_iv, q_iv)


def compose_rounds(
    rounds: list[tuple[object, object]],
    *,
    model: object = SamplingModel.POISSON,
    base_delta: float = 0.0,
) -> Node:
    """Build sum_i subsample_amp(eps_i, q_i) (basic composition).

    Each round is an ``(eps_i, q_i)`` pair of numbers or Nodes.  Constant
    rounds are validated eagerly: if the applicability conditions are not
    met, the composition is refused immediately.  Parameterised rounds are
    validated at evaluation time against their bound intervals.
    """
    nodes: list[Node] = []
    for eps, q in rounds:
        eps_node, q_node = _as_node(eps), _as_node(q)
        if isinstance(eps_node, Const) and isinstance(q_node, Const):
            validate_subsample_applicability(
                eps_node.value, q_node.value, model=model, base_delta=base_delta
            )
        nodes.append(
            SubsampleAmp(eps_node, q_node, model=model, base_delta=base_delta)
        )
    if not nodes:
        return Const(0)
    total = nodes[0]
    for node in nodes[1:]:
        total = Add(total, node)
    return total
