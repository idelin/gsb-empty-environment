"""Exact rational Newton/Hermite interpolation.

The module implements value and first-derivative interpolation with
``fractions.Fraction``. It does not use a symbolic algebra package.
"""

from __future__ import annotations

from dataclasses import dataclass
from fractions import Fraction
from typing import Iterable, Mapping, Sequence


RationalInput = int | Fraction | str


def as_fraction(value: RationalInput) -> Fraction:
    """Convert an exact rational input.

    Floating-point values are rejected because their binary representation is
    normally not the intended exact decimal rational.
    """
    if isinstance(value, float):
        raise TypeError("use an int, Fraction, or exact rational string, not float")
    return Fraction(value)


def fraction_bits(value: Fraction) -> tuple[int, int]:
    """Return the actual normalized numerator/denominator bit lengths."""
    return value.numerator.bit_length(), value.denominator.bit_length()


def max_bits(values: Iterable[Fraction]) -> tuple[int, int]:
    numerator = denominator = 0
    for value in values:
        num_bits, den_bits = fraction_bits(value)
        numerator = max(numerator, num_bits)
        denominator = max(denominator, den_bits)
    return numerator, denominator


@dataclass(frozen=True)
class Observation:
    """One scalar constraint: either p(x)=value or p'(x)=value."""

    id: int | None
    kind: str
    x: Fraction
    value: Fraction

    def __post_init__(self) -> None:
        if self.kind not in ("value", "derivative"):
            raise ValueError("kind must be 'value' or 'derivative'")


@dataclass(frozen=True)
class LinearSystemResult:
    """Result of the independent monomial-basis linear-equation check."""

    status: str
    rank: int
    unknowns: int
    coefficients: tuple[Fraction, ...] | None = None
    witness: dict[int | str, Fraction] | None = None
    residual: Fraction | None = None

    @property
    def unique(self) -> bool:
        return self.status == "unique"


class InterpolationError(ValueError):
    """Base class for interpolation data errors."""


class ConflictingObservations(InterpolationError):
    def __init__(
        self,
        existing: Observation,
        incoming: Observation,
        witness: Mapping[str, Fraction],
        residual: Fraction,
    ) -> None:
        super().__init__(
            f"conflicting {incoming.kind} constraints at x={incoming.x}: "
            f"{existing.value} versus {incoming.value}; certificate residual={residual}"
        )
        self.existing = existing
        self.incoming = incoming
        self.witness = dict(witness)
        self.residual = residual


class IncompatibleConstraints(InterpolationError):
    def __init__(self, result: LinearSystemResult) -> None:
        super().__init__(
            "constraints are incompatible; "
            f"witness={result.witness}, residual={result.residual}"
        )
        self.result = result


class UnderdeterminedSystem(InterpolationError):
    def __init__(self, rank: int, unknowns: int) -> None:
        super().__init__(f"rank {rank} < {unknowns} unknowns")
        self.rank = rank
        self.unknowns = unknowns


class HermiteStructureError(InterpolationError):
    """Raised when data is outside the complete first-order Hermite domain."""


@dataclass(frozen=True)
class _Occurrence:
    id: int
    x: Fraction
    kind: str
    value: Fraction
    node_value: Fraction


@dataclass(frozen=True)
class VerificationReport:
    all_values_match: bool
    all_derivatives_match: bool
    reference_matches: bool
    value_residuals: dict[Fraction, Fraction]
    derivative_residuals: dict[Fraction, Fraction]
    reference_status: str

    @property
    def passed(self) -> bool:
        return (
            self.all_values_match
            and self.all_derivatives_match
            and self.reference_matches
        )


def solve_constraints(
    constraints: Sequence[Observation | tuple[str, RationalInput, RationalInput]],
) -> LinearSystemResult:
    """Solve value/derivative constraints independently in the monomial basis.

    For n constraints, the unknown polynomial has degree at most n-1. Value
    constraints contribute ``sum a_j x^j = y`` and derivative constraints
    contribute ``sum j a_j x^(j-1) = d``. Exact Gaussian elimination produces
    either a unique solution or an explicit linear-combination witness to
    inconsistency.
    """
    rows: list[Observation] = []
    for index, constraint in enumerate(constraints):
        if isinstance(constraint, Observation):
            rows.append(constraint)
        else:
            kind, x, value = constraint
            rows.append(Observation(index, kind, as_fraction(x), as_fraction(value)))

    n = len(rows)
    matrix = [[Fraction(0) for _ in range(n + 1)] for _ in range(n)]
    combinations = [[Fraction(1) if i == j else Fraction(0) for j in range(n)] for i in range(n)]

    for row_index, observation in enumerate(rows):
        x = observation.x
        matrix[row_index][n] = observation.value
        if observation.kind == "value":
            power = Fraction(1)
            for column in range(n):
                matrix[row_index][column] = power
                power *= x
        else:
            power = Fraction(1)
            for column in range(1, n):
                matrix[row_index][column] = Fraction(column) * power
                power *= x

    rank = 0
    pivot_rows: list[int] = []
    for column in range(n):
        pivot = next((row for row in range(rank, n) if matrix[row][column] != 0), None)
        if pivot is None:
            continue
        matrix[rank], matrix[pivot] = matrix[pivot], matrix[rank]
        combinations[rank], combinations[pivot] = combinations[pivot], combinations[rank]

        pivot_value = matrix[rank][column]
        matrix[rank] = [entry / pivot_value for entry in matrix[rank]]
        combinations[rank] = [entry / pivot_value for entry in combinations[rank]]

        for row in range(n):
            if row == rank:
                continue
            factor = matrix[row][column]
            if factor == 0:
                continue
            matrix[row] = [
                left - factor * right for left, right in zip(matrix[row], matrix[rank])
            ]
            combinations[row] = [
                left - factor * right
                for left, right in zip(combinations[row], combinations[rank])
            ]
        pivot_rows.append(rank)
        rank += 1

    for row in range(n):
        if all(matrix[row][column] == 0 for column in range(n)) and matrix[row][n] != 0:
            witness = {
                rows[index].id if rows[index].id is not None else index: weight
                for index, weight in enumerate(combinations[row])
                if weight != 0
            }
            residual = matrix[row][n]
            first_weight = next(iter(witness.values()), Fraction(1))
            if first_weight < 0:
                witness = {key: -weight for key, weight in witness.items()}
                residual = -residual
            return LinearSystemResult(
                "inconsistent",
                rank,
                n,
                witness=witness,
                residual=residual,
            )

    if rank < n:
        return LinearSystemResult("underdetermined", rank, n)

    coefficients: list[Fraction] = [Fraction(0)] * n
    for pivot_column, row in enumerate(pivot_rows):
        coefficients[pivot_column] = matrix[row][n]
    return LinearSystemResult("unique", rank, n, tuple(coefficients))


def add_power_polynomials(
    left: Sequence[Fraction], right: Sequence[Fraction]
) -> list[Fraction]:
    size = max(len(left), len(right))
    result = [Fraction(0) for _ in range(size)]
    for index, coefficient in enumerate(left):
        result[index] += coefficient
    for index, coefficient in enumerate(right):
        result[index] += coefficient
    return result


def scale_power_polynomial(
    coefficients: Sequence[Fraction], factor: Fraction
) -> list[Fraction]:
    return [coefficient * factor for coefficient in coefficients]


def multiply_by_linear(
    coefficients: Sequence[Fraction], center: Fraction
) -> list[Fraction]:
    """Multiply a low-to-high rational polynomial by ``x - center``."""
    result = [Fraction(0) for _ in range(len(coefficients) + 1)]
    for index, coefficient in enumerate(coefficients):
        result[index] -= center * coefficient
        result[index + 1] += coefficient
    return result


def newton_to_power(
    nodes: Sequence[Fraction], coefficients: Sequence[Fraction]
) -> list[Fraction]:
    """Convert Newton form to the canonical monomial/power basis."""
    if len(nodes) != len(coefficients):
        raise ValueError("nodes and coefficients must have equal length")
    result: list[Fraction] = [Fraction(0)]
    basis: list[Fraction] = [Fraction(1)]
    for node, coefficient in zip(nodes, coefficients):
        result = add_power_polynomials(
            result, scale_power_polynomial(basis, coefficient)
        )
        basis = multiply_by_linear(basis, node)
    return result


def evaluate_power(coefficients: Sequence[Fraction], x: Fraction) -> Fraction:
    result = Fraction(0)
    for coefficient in reversed(coefficients):
        result = result * x + coefficient
    return result


def evaluate_power_derivative(
    coefficients: Sequence[Fraction], x: Fraction
) -> Fraction:
    return sum(
        Fraction(index) * coefficient * x ** (index - 1)
        for index, coefficient in enumerate(coefficients)
        if index > 0
    )


def format_rational(value: Fraction) -> str:
    if value.denominator == 1:
        return str(value.numerator)
    return f"{value.numerator}/{value.denominator}"


def format_power(coefficients: Sequence[Fraction], variable: str = "x") -> str:
    terms: list[str] = []
    for index, coefficient in enumerate(coefficients):
        if coefficient == 0:
            continue
        sign = "-" if coefficient < 0 else ""
        magnitude = format_rational(abs(coefficient))
        if index == 0:
            term = f"{sign}{magnitude}"
        elif index == 1:
            factor = "" if magnitude == "1" else f"{magnitude}*"
            term = f"{sign}{factor}{variable}"
        else:
            factor = "" if magnitude == "1" else f"{magnitude}*"
            term = f"{sign}{factor}{variable}^{index}"
        terms.append(term)
    if not terms:
        return "0"
    return " + ".join(terms).replace("+ -", "- ")


def format_newton(
    nodes: Sequence[Fraction], coefficients: Sequence[Fraction], variable: str = "x"
) -> str:
    terms: list[str] = []
    for index, coefficient in enumerate(coefficients):
        if coefficient == 0:
            continue
        factors: list[str] = []
        for node in nodes[:index]:
            if node == 0:
                factors.append(variable)
            elif node > 0:
                factors.append(f"({variable} - {format_rational(node)})")
            else:
                factors.append(f"({variable} + {format_rational(-node)})")
        body = "*".join(factors)
        magnitude = format_rational(abs(coefficient))
        sign = "-" if coefficient < 0 else ""
        if body and magnitude != "1":
            term = f"{sign}{magnitude}*{body}"
        elif body:
            term = f"{sign}{body}"
        else:
            term = f"{sign}{magnitude}"
        terms.append(term)
    if not terms:
        return "0"
    return " + ".join(terms).replace("+ -", "- ")


class RationalHermiteInterpolator:
    """Incremental exact rational Newton/Hermite interpolator.

    The confluent Newton domain is:
    * each x has at most one value constraint p(x)=y;
    * each x has at most one first-derivative constraint p'(x)=d;
    * a derivative constraint is represented by a second adjacent occurrence of
      x, and its x must also have a value constraint;
    * groups of repeated nodes may occur in any group order.

    The independent monomial solver can examine arbitrary first-derivative
    constraints, including derivative-only data outside this Newton domain.
    """

    def __init__(self) -> None:
        self._values: dict[Fraction, Observation] = {}
        self._derivatives: dict[Fraction, Observation] = {}
        self._x_order: list[Fraction] = []
        self._next_id = 1
        self._cache: dict[tuple[int, ...], Fraction] = {}
        self._active_by_id: dict[int, _Occurrence] = {}
        self._nodes: list[Fraction] = []
        self._newton_coefficients: list[Fraction] = []
        self._power_coefficients: list[Fraction] = []
        self._cache_hits = 0
        self._cache_misses = 0
        self._last_error: str | None = None
        self._bit_history: list[dict[str, object]] = []
        self._refresh("initialized")

    @property
    def observation_count(self) -> int:
        return len(self._values) + len(self._derivatives)

    @property
    def node_order(self) -> list[Fraction]:
        return list(self._x_order)

    @property
    def bit_growth_history(self) -> list[dict[str, object]]:
        return [dict(record) for record in self._bit_history]

    @property
    def last_build_stats(self) -> dict[str, int]:
        return {
            "hits": self._cache_hits,
            "misses": self._cache_misses,
            "cached_spans": len(self._cache),
        }

    def add_point(self, x: RationalInput, y: RationalInput) -> Observation:
        return self._add("value", x, y)

    def add_derivative(self, x: RationalInput, derivative: RationalInput) -> Observation:
        return self._add("derivative", x, derivative)

    def remove_point(self, x: RationalInput) -> None:
        self._remove("value", x)

    def remove_derivative(self, x: RationalInput) -> None:
        self._remove("derivative", x)

    def remove_observation(self, observation_id: int) -> None:
        for table in (self._values, self._derivatives):
            for x, observation in list(table.items()):
                if observation.id == observation_id:
                    del table[x]
                    if x not in self._values and x not in self._derivatives:
                        self._x_order.remove(x)
                    self._refresh(f"remove_observation:{observation_id}")
                    return
        raise KeyError(f"unknown observation id {observation_id}")

    def reorder_nodes(self, new_order: Sequence[RationalInput]) -> None:
        """Permute distinct x groups; the mathematical polynomial is unchanged."""
        order = [as_fraction(x) for x in new_order]
        if len(order) != len(self._x_order) or set(order) != set(self._x_order):
            raise ValueError("new_order must be a permutation of the current distinct x values")
        self._x_order = order
        self._refresh("reorder_nodes")

    def to_newton(self) -> tuple[tuple[Fraction, ...], tuple[Fraction, ...]]:
        self._require_built()
        return tuple(self._nodes), tuple(self._newton_coefficients)

    def to_power(self) -> tuple[Fraction, ...]:
        self._require_built()
        return tuple(self._power_coefficients)

    def evaluate(self, x: RationalInput) -> Fraction:
        self._require_built()
        return evaluate_power(self._power_coefficients, as_fraction(x))

    def derivative(self, x: RationalInput) -> Fraction:
        self._require_built()
        return evaluate_power_derivative(self._power_coefficients, as_fraction(x))

    def verify(self) -> VerificationReport:
        """Check every value/derivative and compare with the independent solver."""
        self._require_built()
        value_residuals = {
            x: self.evaluate(x) - observation.value
            for x, observation in self._values.items()
        }
        derivative_residuals = {
            x: self.derivative(x) - observation.value
            for x, observation in self._derivatives.items()
        }
        reference = solve_constraints(self._observations())
        reference_matches = (
            reference.status == "unique"
            and list(reference.coefficients or ()) == self._power_coefficients
        )
        return VerificationReport(
            all(value == 0 for value in value_residuals.values()),
            all(value == 0 for value in derivative_residuals.values()),
            reference_matches,
            value_residuals,
            derivative_residuals,
            reference.status,
        )

    def diagnose(self) -> dict[str, object]:
        """Independently diagnose the active constraints in the monomial basis."""
        result = solve_constraints(self._observations())
        return {
            "status": result.status,
            "rank": result.rank,
            "unknowns": result.unknowns,
            "coefficients": result.coefficients,
            "witness": result.witness,
            "residual": result.residual,
            "newton_domain_error": self._last_error,
        }

    def require_consistent(self) -> None:
        result = solve_constraints(self._observations())
        if result.status == "inconsistent":
            raise IncompatibleConstraints(result)
        if result.status == "underdetermined":
            raise UnderdeterminedSystem(result.rank, result.unknowns)

    def _add(self, kind: str, x: RationalInput, value: RationalInput) -> Observation:
        x_fraction = as_fraction(x)
        value_fraction = as_fraction(value)
        table = self._values if kind == "value" else self._derivatives
        existing = table.get(x_fraction)
        if existing is not None:
            if existing.value == value_fraction:
                return existing
            incoming = Observation(self._next_id, kind, x_fraction, value_fraction)
            residual = existing.value - value_fraction
            witness = {
                f"existing#{existing.id}": Fraction(1),
                f"incoming#{incoming.id}": Fraction(-1),
            }
            raise ConflictingObservations(existing, incoming, witness, residual)

        observation = Observation(self._next_id, kind, x_fraction, value_fraction)
        self._next_id += 1
        if x_fraction not in self._x_order:
            self._x_order.append(x_fraction)
        table[x_fraction] = observation
        self._refresh(f"add_{kind}")
        return observation

    def _remove(self, kind: str, x: RationalInput) -> None:
        x_fraction = as_fraction(x)
        table = self._values if kind == "value" else self._derivatives
        if x_fraction not in table:
            raise KeyError(f"no {kind} observation at x={x_fraction}")
        del table[x_fraction]
        if x_fraction not in self._values and x_fraction not in self._derivatives:
            self._x_order.remove(x_fraction)
        self._refresh(f"remove_{kind}")

    def _observations(self) -> list[Observation]:
        observations: list[Observation] = []
        for x in self._x_order:
            if x in self._values:
                observations.append(self._values[x])
            if x in self._derivatives:
                observations.append(self._derivatives[x])
        return observations

    def _occurrences(self) -> list[_Occurrence]:
        occurrences: list[_Occurrence] = []
        for x in self._x_order:
            value = self._values.get(x)
            derivative = self._derivatives.get(x)
            if derivative is not None and value is None:
                raise HermiteStructureError(
                    f"derivative at x={x} has no matching value constraint; "
                    "confluent Newton form is outside its domain"
                )
            if value is not None:
                occurrences.append(
                    _Occurrence(value.id or -1, x, "value", value.value, value.value)
                )
            if derivative is not None and value is not None:
                occurrences.append(
                    _Occurrence(
                        derivative.id or -1,
                        x,
                        "derivative",
                        derivative.value,
                        value.value,
                    )
                )
        return occurrences

    def _refresh(self, operation: str) -> None:
        self._cache_hits = 0
        self._cache_misses = 0
        try:
            occurrences = self._occurrences()
            self._active_by_id = {occurrence.id: occurrence for occurrence in occurrences}
            self._nodes = [occurrence.x for occurrence in occurrences]
            self._newton_coefficients = [
                self._divided_difference(occurrences, 0, end)
                for end in range(1, len(occurrences) + 1)
            ]
            self._power_coefficients = (
                newton_to_power(self._nodes, self._newton_coefficients)
                if occurrences
                else []
            )
            self._last_error = None
            status = "ok"
        except HermiteStructureError as error:
            self._active_by_id = {}
            self._nodes = []
            self._newton_coefficients = []
            self._power_coefficients = []
            self._last_error = str(error)
            status = "outside_newton_domain"
        self._record_bits(operation, status)

    def _divided_difference(
        self, occurrences: Sequence[_Occurrence], start: int, end: int
    ) -> Fraction:
        key = tuple(occurrence.id for occurrence in occurrences[start:end])
        cached = self._cache.get(key)
        if cached is not None:
            self._cache_hits += 1
            return cached
        self._cache_misses += 1

        if end - start == 1:
            result = occurrences[start].node_value
        elif end - start == 2 and occurrences[start].x == occurrences[end - 1].x:
            first, second = occurrences[start], occurrences[end - 1]
            if first.kind != "value" or second.kind != "derivative":
                raise HermiteStructureError(
                    "a repeated node must be ordered as value then first derivative"
                )
            result = second.value
        else:
            left = occurrences[start].x
            right = occurrences[end - 1].x
            if left == right:
                raise HermiteStructureError(
                    "only first-order Hermite repetition is supported; "
                    f"span at x={left} needs a higher-order derivative"
                )
            result = (
                self._divided_difference(occurrences, start + 1, end)
                - self._divided_difference(occurrences, start, end - 1)
            ) / (right - left)

        self._cache[key] = result
        return result

    def _require_built(self) -> None:
        if self._last_error is not None:
            raise HermiteStructureError(self._last_error)

    def _record_bits(self, operation: str, status: str) -> None:
        observations = self._observations()
        input_num, input_den = max_bits(observation.value for observation in observations)
        newton_num, newton_den = max_bits(self._newton_coefficients)
        power_num, power_den = max_bits(self._power_coefficients)
        self._bit_history.append(
            {
                "operation": operation,
                "status": status,
                "observations": len(observations),
                "input_numerator_bits": input_num,
                "input_denominator_bits": input_den,
                "newton_numerator_bits": newton_num,
                "newton_denominator_bits": newton_den,
                "power_numerator_bits": power_num,
                "power_denominator_bits": power_den,
                "cache_hits": self._cache_hits,
                "cache_misses": self._cache_misses,
            }
        )
