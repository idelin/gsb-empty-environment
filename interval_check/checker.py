"""Conservative interval analysis with exact small-domain enumeration.

Two clearly separated verdicts:

* **conservative warning** - the interval machinery cannot prove safety
  (correlation loss, divisor interval spanning zero).  It *may* be a false
  alarm.
* **confirmed failure** - exhaustive enumeration produced a concrete witness
  (an assignment that violates the assertion, or that divides by zero).

When enumeration covers the whole (bounded) domain and finds no witness,
conservative warnings are explicitly marked ``refuted`` so that correlation
loss (e.g. ``x - x``) is never reported as a real defect.
"""

from __future__ import annotations

import math
from dataclasses import dataclass, field
from enum import Enum
from itertools import product
from typing import Iterable

from .evaluator import DivisionByZero, evaluate
from .interval import Interval
from .parser import Assertion, Node, parse, parse_assertion


class CheckStatus(str, Enum):
    SAFE = "safe"
    """Interval analysis proves safety; or enumeration found nothing."""

    CONSERVATIVE_WARNING = "conservative_warning"
    """Interval analysis cannot prove safety; not exact-checked."""

    CONSERVATIVE_WARNING_REFUTED = "conservative_warning_refuted"
    """Interval analysis warned, but full enumeration refuted it."""

    CONFIRMED_FAILURE = "confirmed_failure"
    """Exact enumeration found a concrete violating witness."""


@dataclass
class Finding:
    kind: str  # 'assertion_violation_possible' | 'division_by_zero_possible'
    certainty: str  # 'conservative' | 'confirmed'
    message: str
    refuted: bool = False
    interval: list[int] | None = None
    witness: dict | None = None

    def to_dict(self) -> dict:
        return {
            "kind": self.kind,
            "certainty": self.certainty,
            "refuted": self.refuted,
            "message": self.message,
            **({"interval": self.interval} if self.interval is not None else {}),
            **({"witness": self.witness} if self.witness is not None else {}),
        }


@dataclass
class AnalysisResult:
    status: CheckStatus
    expression: str
    assertion: str | None
    interval: list[int] | None
    guaranteed: bool
    enumeration: dict
    findings: list[Finding] = field(default_factory=list)
    witness: dict | None = None

    @property
    def has_confirmed_failure(self) -> bool:
        return self.status is CheckStatus.CONFIRMED_FAILURE

    def to_dict(self) -> dict:
        return {
            "status": self.status.value,
            "expression": self.expression,
            "assertion": self.assertion,
            "interval": self.interval,
            "assertion_guaranteed": self.guaranteed,
            "enumeration": self.enumeration,
            "findings": [finding.to_dict() for finding in self.findings],
            **({"witness": self.witness} if self.witness is not None else {}),
        }


def node_text(node: Node) -> str:
    if node.op == "const":
        return str(node.value)
    if node.op == "var":
        return str(node.value)
    if node.op == "neg":
        return f"(-{node_text(node.left)})"
    return f"({node_text(node.left)} {node.op} {node_text(node.right)})"


def collect_variables(node: Node) -> set[str]:
    names: set[str] = set()

    def walk(current: Node) -> None:
        if current.op == "var":
            names.add(str(current.value))
        if current.left is not None:
            walk(current.left)
        if current.right is not None:
            walk(current.right)

    walk(node)
    return names


def _violates(value: int, relop: str, rhs: int) -> bool:
    if relop == "==":
        return value != rhs
    if relop == "!=":
        return value == rhs
    if relop == "<":
        return value >= rhs
    if relop == "<=":
        return value > rhs
    if relop == ">":
        return value <= rhs
    if relop == ">=":
        return value < rhs
    raise ValueError(f"unknown relational operator: {relop!r}")


def _guaranteed(interval: Interval, relop: str, rhs: int) -> bool:
    if relop == "==":
        return interval.lo == rhs and interval.hi == rhs
    if relop == "!=":
        return not interval.contains(rhs)
    if relop == "<":
        return interval.hi < rhs
    if relop == "<=":
        return interval.hi <= rhs
    if relop == ">":
        return interval.lo > rhs
    if relop == ">=":
        return interval.lo >= rhs
    raise ValueError(f"unknown relational operator: {relop!r}")


def _coerce_variables(variables: dict, used: Iterable[str]) -> dict[str, Interval]:
    coerced: dict[str, Interval] = {}
    for name, bounds in variables.items():
        if not isinstance(name, str) or not name:
            raise ValueError("variable names must be non-empty strings")
        if isinstance(bounds, Interval):
            coerced[name] = bounds
        elif isinstance(bounds, (list, tuple)) and len(bounds) == 2:
            coerced[name] = Interval(int(bounds[0]), int(bounds[1]))
        else:
            raise ValueError(
                f"variable {name!r} must map to [lo, hi] with lo <= hi"
            )
    missing = sorted(set(used) - set(coerced))
    if missing:
        raise ValueError(f"missing domains for variables: {', '.join(missing)}")
    return coerced


def analyze(
    expression: "str | Node",
    variables: dict,
    assertion: "str | Assertion | None" = None,
    enumerate_limit: int = 1_000_000,
) -> AnalysisResult:
    """Run interval analysis and, for small domains, exact enumeration.

    ``variables`` maps each name used in the expression to a finite integer
    interval given as ``[lo, hi]``.
    """
    if isinstance(expression, str):
        root = parse(expression)
        expression_text = expression
    else:
        root = expression
        expression_text = node_text(expression)

    parsed_assertion: Assertion | None = None
    if assertion is not None:
        if isinstance(assertion, str):
            parsed_assertion = parse_assertion(assertion)
        else:
            parsed_assertion = assertion
        assertion_root = parsed_assertion.expr
    else:
        assertion_root = root

    used = collect_variables(assertion_root)
    domains = _coerce_variables(variables, used)
    domain_sizes = {
        name: domains[name].width for name in sorted(used)
    }

    findings: list[Finding] = []
    interval_map: dict[int, Interval] = {}

    def interval_eval(node: Node) -> Interval | None:
        """Return the sound interval, or None where div-by-zero is possible."""
        if node.op == "const":
            result = Interval(int(node.value), int(node.value))
        elif node.op == "var":
            result = domains[str(node.value)]
        elif node.op == "neg":
            operand = interval_eval(node.left)  # type: ignore[arg-type]
            result = None if operand is None else operand.negate()
        else:
            left = interval_eval(node.left)  # type: ignore[arg-type]
            right = interval_eval(node.right)  # type: ignore[arg-type]
            if left is None or right is None:
                result = None
            elif node.op == "//":
                if right.contains_zero:
                    findings.append(
                        Finding(
                            kind="division_by_zero_possible",
                            certainty="conservative",
                            message=(
                                "divisor of floor division may be zero: "
                                f"{node_text(node)} has divisor interval "
                                f"{right}"
                            ),
                            interval=[right.lo, right.hi],
                        )
                    )
                    result = None
                else:
                    result = left.floor_div(right)
            elif node.op == "+":
                result = left.add(right)
            elif node.op == "-":
                result = left.sub(right)
            elif node.op == "*":
                result = left.mul(right)
            else:
                raise ValueError(f"unknown operator: {node.op!r}")
        if result is not None:
            interval_map[id(node)] = result
        return result

    top_interval = interval_eval(assertion_root)

    guaranteed = False
    if parsed_assertion is not None:
        if top_interval is not None:
            guaranteed = _guaranteed(
                top_interval, parsed_assertion.relop, parsed_assertion.rhs
            )
            if not guaranteed:
                findings.append(
                    Finding(
                        kind="assertion_violation_possible",
                        certainty="conservative",
                        message=(
                            "interval analysis cannot guarantee "
                            f"{parsed_assertion.source!r}; result interval is "
                            f"{top_interval}"
                        ),
                        interval=[top_interval.lo, top_interval.hi],
                    )
                )
        else:
            findings.append(
                Finding(
                    kind="assertion_violation_possible",
                    certainty="conservative",
                    message=(
                        "assertion cannot be evaluated interval-safely because "
                        "a sub-expression has a divisor that may be zero: "
                        f"{parsed_assertion.source!r}"
                    ),
                )
            )

    enumeration_info = _enumerate(
        assertion_root,
        domains,
        sorted(used),
        parsed_assertion,
        enumerate_limit,
        findings,
    )

    confirmed = [
        finding for finding in findings if finding.certainty == "confirmed"
    ]
    conservative = [
        finding for finding in findings if finding.certainty == "conservative"
    ]
    witness = confirmed[0].witness if confirmed else None

    if confirmed:
        status = CheckStatus.CONFIRMED_FAILURE
    elif enumeration_info["completed"]:
        # Full enumeration covered the finite domain: every conservative
        # warning left on the list was checked and refuted.
        if conservative:
            status = CheckStatus.CONSERVATIVE_WARNING_REFUTED
        else:
            status = CheckStatus.SAFE
    else:
        status = (
            CheckStatus.CONSERVATIVE_WARNING if conservative else CheckStatus.SAFE
        )

    return AnalysisResult(
        status=status,
        expression=expression_text,
        assertion=parsed_assertion.source if parsed_assertion else None,
        interval=(
            [top_interval.lo, top_interval.hi]
            if top_interval is not None
            else None
        ),
        guaranteed=guaranteed,
        enumeration=enumeration_info,
        findings=findings,
        witness=witness,
    )


def _enumerate(
    root: Node,
    domains: dict[str, Interval],
    names: list[str],
    assertion: Assertion | None,
    limit: int,
    findings: list[Finding],
) -> dict:
    value_lists = [
        list(range(domains[name].lo, domains[name].hi + 1)) for name in names
    ]
    total = math.prod((len(values) for values in value_lists), start=1)
    info: dict = {
        "performed": False,
        "completed": False,
        "domain_sizes": {name: len(values) for name, values in zip(names, value_lists)},
        "total_assignments": total,
        "assignments_checked": 0,
        "limit": limit,
        "reason": None,
    }
    if total > limit:
        info["reason"] = (
            f"domain product {total} exceeds enumeration limit {limit}; "
            "only conservative interval analysis is available"
        )
        return info

    info["performed"] = True
    for combo in product(*value_lists):
        assignment = dict(zip(names, combo))
        info["assignments_checked"] += 1
        try:
            value = evaluate(root, assignment)
        except DivisionByZero:
            findings.append(
                Finding(
                    kind="division_by_zero",
                    certainty="confirmed",
                    message=(
                        "concrete assignment triggers floor division by zero "
                        f"in {node_text(root)}"
                    ),
                    witness={
                        "type": "division_by_zero",
                        "assignment": assignment,
                    },
                )
            )
            info["completed"] = False
            info["reason"] = "stopped at a confirmed division-by-zero witness"
            return info
        if assertion is not None and _violates(
            value, assertion.relop, assertion.rhs
        ):
            findings.append(
                Finding(
                    kind="assertion_violation",
                    certainty="confirmed",
                    message=(
                        f"assertion {assertion.source!r} is violated: "
                        f"{node_text(root)} = {value}"
                    ),
                    witness={
                        "type": "assertion_violation",
                        "assignment": assignment,
                        "expression_value": value,
                        "assertion": assertion.source,
                    },
                )
            )
            info["completed"] = False
            info["reason"] = "stopped at a confirmed violating witness"
            return info

    info["completed"] = True
    info["reason"] = "all assignments checked; no witness found"
    for finding in findings:
        if finding.certainty == "conservative":
            finding.refuted = True
    return info


def analyze_dict(spec: dict) -> AnalysisResult:
    """Analyze a specification already parsed from JSON.

    Shape::

        {
          "expression": "x - x",
          "variables": {"x": [0, 1]},
          "assertion": "x - x >= 0",        # optional
          "enumerate_limit": 1000000        # optional
        }
    """
    if not isinstance(spec, dict):
        raise ValueError("specification must be a JSON object")
    expression = spec.get("expression")
    if not isinstance(expression, str) or not expression.strip():
        raise ValueError("'expression' must be a non-empty string")
    variables = spec.get("variables", {})
    if not isinstance(variables, dict):
        raise ValueError("'variables' must be an object mapping name to [lo, hi]")
    assertion = spec.get("assertion")
    if assertion is not None and not isinstance(assertion, str):
        raise ValueError("'assertion' must be a string")
    limit = spec.get("enumerate_limit", 1_000_000)
    if isinstance(limit, bool) or not isinstance(limit, int) or limit < 0:
        raise ValueError("'enumerate_limit' must be a non-negative integer")
    return analyze(expression, variables, assertion=assertion, enumerate_limit=limit)
