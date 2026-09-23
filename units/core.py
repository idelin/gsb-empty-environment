"""Dimension vectors, affine unit typing, and unknown-unit constraints."""

from __future__ import annotations

from dataclasses import dataclass
from types import MappingProxyType
from typing import Mapping, Sequence

from .graph import ConversionGraph, ConversionGraphSnapshot, ExactValue, exact_fraction
from .linear import (
    InconsistencyEvidence,
    IntegerSolutionLattice,
    SolutionVerification,
    solve_integer_system,
)


def _require_integer(value: int, *, name: str = "exponent") -> int:
    if isinstance(value, bool) or not isinstance(value, int):
        raise TypeError(f"{name} must be an integer, got {type(value).__name__}")
    return value


def unknown_symbol(unit_name: str) -> str:
    return f"?{unit_name}"


@dataclass(frozen=True)
class DimensionVector:
    """A product of powers represented as an exact integer exponent vector."""

    exponents: Mapping[str, int]

    def __post_init__(self) -> None:
        normalized = {
            str(symbol): _require_integer(exponent)
            for symbol, exponent in self.exponents.items()
            if _require_integer(exponent) != 0
        }
        object.__setattr__(self, "exponents", MappingProxyType(normalized))

    @classmethod
    def symbol(cls, symbol: str, exponent: int = 1) -> "DimensionVector":
        return cls({symbol: exponent})

    def exponent(self, symbol: str) -> int:
        return self.exponents.get(symbol, 0)

    def _combine(self, other: "DimensionVector", sign: int) -> "DimensionVector":
        symbols = set(self.exponents) | set(other.exponents)
        return DimensionVector(
            {
                symbol: self.exponent(symbol) + sign * other.exponent(symbol)
                for symbol in symbols
            }
        )

    def __mul__(self, other: "DimensionVector") -> "DimensionVector":
        return self._combine(other, 1)

    def __truediv__(self, other: "DimensionVector") -> "DimensionVector":
        return self._combine(other, -1)

    def __pow__(self, exponent: int) -> "DimensionVector":
        exponent = _require_integer(exponent)
        return DimensionVector(
            {symbol: power * exponent for symbol, power in self.exponents.items()}
        )

    def __str__(self) -> str:
        if not self.exponents:
            return "1"
        return " * ".join(
            f"{symbol}^{exponent}" if exponent != 1 else symbol
            for symbol, exponent in sorted(self.exponents.items())
        )


class UnitTypeError(TypeError):
    """Base class for unit type errors."""


class AffineUnitError(UnitTypeError):
    """Raised when an absolute affine unit is used in an invalid operation."""


class DimensionMismatchError(UnitTypeError):
    """Raised when additive operands have different dimensions."""


@dataclass(frozen=True)
class UnitDefinition:
    name: str
    dimension: DimensionVector
    affine: bool = False
    delta_unit: str | None = None


@dataclass(frozen=True)
class UnitExpression:
    dimension: DimensionVector
    affine: bool = False
    source_units: tuple[str, ...] = ()

    def _binary(self, other: "UnitExpression", sign: int, operation: str) -> "UnitExpression":
        if self.affine or other.affine:
            raise AffineUnitError(
                f"绝对仿射单位不能参与普通{operation}；"
                f"左仿射={self.affine}，右仿射={other.affine}，"
                f"左量纲={self.dimension}，右量纲={other.dimension}"
            )
        dimension = self.dimension * other.dimension if sign == 1 else self.dimension / other.dimension
        return UnitExpression(dimension, False, self.source_units + other.source_units)

    def __mul__(self, other: "UnitExpression") -> "UnitExpression":
        return self._binary(other, 1, "乘法")

    def __truediv__(self, other: "UnitExpression") -> "UnitExpression":
        return self._binary(other, -1, "除法")

    def __pow__(self, exponent: int) -> "UnitExpression":
        if self.affine:
            raise AffineUnitError(
                f"绝对仿射单位不能取幂；量纲={self.dimension}，指数={exponent}"
            )
        return UnitExpression(self.dimension**exponent, False, self.source_units)

    def compatible_with(self, other: "UnitExpression") -> bool:
        return self.dimension == other.dimension


@dataclass(frozen=True)
class QuantityType:
    """Joint dimension/affine type, including unknown dimension symbols."""

    dimension: DimensionVector
    affine: bool = False


def type_add(left: QuantityType, right: QuantityType) -> QuantityType:
    _require_same_dimension(left, right, "加法")
    if left.affine and right.affine:
        raise AffineUnitError("两个绝对仿射量不能相加；可先求差得到差量")
    return QuantityType(left.dimension, left.affine or right.affine)


def type_subtract(left: QuantityType, right: QuantityType) -> QuantityType:
    _require_same_dimension(left, right, "减法")
    if not left.affine and right.affine:
        raise AffineUnitError("差量不能减去绝对仿射量")
    if left.affine and right.affine:
        return QuantityType(left.dimension, False)
    return QuantityType(left.dimension, left.affine)


def type_multiply(left: QuantityType, right: QuantityType) -> QuantityType:
    if left.affine or right.affine:
        raise AffineUnitError(
            "绝对仿射量默认不能参与乘法；"
            f"左(仿射={left.affine}, 量纲={left.dimension})，"
            f"右(仿射={right.affine}, 量纲={right.dimension})"
        )
    return QuantityType(left.dimension * right.dimension, False)


def type_divide(left: QuantityType, right: QuantityType) -> QuantityType:
    if left.affine or right.affine:
        raise AffineUnitError(
            "绝对仿射量默认不能参与除法；"
            f"左(仿射={left.affine}, 量纲={left.dimension})，"
            f"右(仿射={right.affine}, 量纲={right.dimension})"
        )
    return QuantityType(left.dimension / right.dimension, False)


def _require_same_dimension(
    left: QuantityType, right: QuantityType, operation: str
) -> None:
    if left.dimension != right.dimension:
        raise DimensionMismatchError(
            f"{operation}要求量纲相同：{left.dimension} != {right.dimension}"
        )


@dataclass(frozen=True)
class IntegerAffineDimension:
    constant: int
    coefficients: tuple[int, ...]

    def evaluate(self, parameters: Sequence[int]) -> int:
        if len(parameters) != len(self.coefficients):
            raise ValueError("parameter count does not match free dimension rank")
        return self.constant + sum(
            parameter * coefficient
            for parameter, coefficient in zip(parameters, self.coefficients)
        )

    def describe(self, free_variables: Sequence[str]) -> str:
        terms = [str(self.constant)]
        terms.extend(
            f"{coefficient}*{name}"
            for coefficient, name in zip(self.coefficients, free_variables)
            if coefficient != 0
        )
        return " + ".join(terms)


@dataclass(frozen=True)
class FreeDimensionVector:
    """A dimension vector whose coordinates may contain free integers."""

    entries: Mapping[str, IntegerAffineDimension]
    free_variables: tuple[str, ...]

    def evaluate(self, parameters: Sequence[int] = ()) -> DimensionVector:
        return DimensionVector(
            {
                symbol: expression.evaluate(parameters)
                for symbol, expression in self.entries.items()
            }
        )

    def as_dict(self) -> dict[str, dict[str, object]]:
        return {
            symbol: {
                "constant": expression.constant,
                "coefficients": expression.coefficients,
            }
            for symbol, expression in self.entries.items()
        }

    def describe(self) -> str:
        if not self.entries:
            return "1"
        return ", ".join(
            f"{symbol}^({expression.describe(self.free_variables)})"
            for symbol, expression in sorted(self.entries.items())
        )


@dataclass(frozen=True)
class ConstraintEquation:
    left: DimensionVector
    right: DimensionVector
    label: str


class InconsistentUnitConstraints(ValueError):
    def __init__(self, evidence: InconsistencyEvidence) -> None:
        super().__init__(evidence.describe())
        self.evidence = evidence


@dataclass(frozen=True)
class UnitConstraintSolution:
    unknown_units: tuple[str, ...]
    base_dimensions: tuple[str, ...]
    lattice: IntegerSolutionLattice

    @property
    def free_variables(self) -> tuple[str, ...]:
        return tuple(f"k{index + 1}" for index in range(self.lattice.free_rank))

    @property
    def verification(self) -> SolutionVerification:
        return self.lattice.verification

    def dimension_of(self, unit_name: str) -> FreeDimensionVector:
        if unit_name not in self.unknown_units:
            raise KeyError(f"unknown unit is not constrained here: {unit_name}")
        unit_index = self.unknown_units.index(unit_name)
        width = len(self.base_dimensions)
        start = unit_index * width
        entries = {}
        for offset, base_dimension in enumerate(self.base_dimensions):
            variable_index = start + offset
            entries[base_dimension] = IntegerAffineDimension(
                self.lattice.particular[variable_index],
                tuple(
                    basis_vector[variable_index]
                    for basis_vector in self.lattice.basis
                ),
            )
        return FreeDimensionVector(MappingProxyType(entries), self.free_variables)


class UnitConstraintSystem:
    """Build and solve exact integer constraints for unknown unit dimensions."""

    def __init__(
        self,
        base_dimensions: Sequence[str],
        unknown_units: Sequence[str],
    ) -> None:
        self.base_dimensions = tuple(base_dimensions)
        self.unknown_units = tuple(unknown_units)
        self._unknown_symbols = {
            unit_name: unknown_symbol(unit_name) for unit_name in self.unknown_units
        }
        self.equations: list[ConstraintEquation] = []

    def equate(
        self,
        left: DimensionVector,
        right: DimensionVector,
        *,
        label: str = "",
    ) -> None:
        allowed = set(self.base_dimensions) | set(self._unknown_symbols.values())
        for side_name, side in (("left", left), ("right", right)):
            unknown_symbols = set(side.exponents) - allowed
            if unknown_symbols:
                raise KeyError(
                    f"{side_name} uses unregistered dimensions/units: "
                    f"{sorted(unknown_symbols)}"
                )
        self.equations.append(ConstraintEquation(left, right, label))

    def _integer_system(self) -> tuple[list[list[int]], list[int]]:
        rows: list[list[int]] = []
        constants: list[int] = []
        for equation in self.equations:
            for base_dimension in self.base_dimensions:
                row = []
                for unit_name in self.unknown_units:
                    symbol = self._unknown_symbols[unit_name]
                    unknown_exponent = equation.left.exponent(symbol) - equation.right.exponent(symbol)
                    row.extend(
                        unknown_exponent if coordinate == base_dimension else 0
                        for coordinate in self.base_dimensions
                    )
                known_difference = equation.right.exponent(base_dimension) - equation.left.exponent(base_dimension)
                rows.append(row)
                constants.append(known_difference)
        return rows, constants

    def solve(self) -> UnitConstraintSolution:
        if not self.equations:
            variable_count = len(self.unknown_units) * len(self.base_dimensions)
            lattice = IntegerSolutionLattice(
                tuple(0 for _ in range(variable_count)),
                tuple(
                    tuple(1 if row == column else 0 for row in range(variable_count))
                    for column in range(variable_count)
                ),
                SolutionVerification((), ()),
            )
            return UnitConstraintSolution(
                self.unknown_units,
                self.base_dimensions,
                lattice,
            )
        matrix, rhs = self._integer_system()
        result = solve_integer_system(matrix, rhs)
        if isinstance(result, InconsistencyEvidence):
            raise InconsistentUnitConstraints(result)
        return UnitConstraintSolution(
            self.unknown_units,
            self.base_dimensions,
            result,
        )


class UnitRegistry:
    """Unit definitions plus a versioned exact conversion graph."""

    def __init__(self) -> None:
        self.base_dimensions: set[str] = set()
        self.definitions: dict[str, UnitDefinition] = {}
        self.graph = ConversionGraph()

    @property
    def version(self) -> int:
        return self.graph.version

    def define_base_dimension(self, name: str) -> None:
        self.base_dimensions.add(name)

    def define_unit(
        self,
        name: str,
        dimensions: Mapping[str, int] | DimensionVector,
        *,
        affine: bool = False,
        delta_unit: str | None = None,
    ) -> None:
        dimension = (
            dimensions
            if isinstance(dimensions, DimensionVector)
            else DimensionVector(dimensions)
        )
        unknown_symbols = set(dimension.exponents) - self.base_dimensions
        if unknown_symbols:
            raise KeyError(
                f"unit {name} uses undeclared dimensions: {sorted(unknown_symbols)}"
            )
        if delta_unit is not None:
            delta = self.definitions.get(delta_unit)
            if delta is None:
                raise KeyError(f"unknown delta unit: {delta_unit}")
            if delta.affine:
                raise ValueError("delta_unit must be proportional, not affine")
            if delta.dimension != dimension:
                raise DimensionMismatchError(
                    f"absolute unit {name} and delta unit {delta_unit} differ in dimension"
                )
        self.graph.add_node(name)
        self.definitions[name] = UnitDefinition(name, dimension, affine, delta_unit)

    def define_unknown_unit(self, name: str, *, affine: bool = False) -> None:
        self.graph.add_node(name)
        self.definitions[name] = UnitDefinition(
            name,
            DimensionVector.symbol(unknown_symbol(name)),
            affine,
        )

    def remove_unit(self, name: str) -> None:
        if name not in self.definitions:
            raise KeyError(f"unknown unit: {name}")
        del self.definitions[name]
        self.graph.remove_node(name)

    def unit(self, name: str) -> UnitExpression:
        try:
            definition = self.definitions[name]
        except KeyError as exc:
            raise KeyError(f"unknown unit: {name}") from exc
        return UnitExpression(definition.dimension, definition.affine, (name,))

    def quantity_type(self, name: str) -> QuantityType:
        expression = self.unit(name)
        return QuantityType(expression.dimension, expression.affine)

    def define_conversion(
        self,
        source: str,
        target: str,
        scale: ExactValue,
        offset: ExactValue = 0,
        *,
        domain: str = "",
    ) -> int:
        if source not in self.definitions or target not in self.definitions:
            missing = source if source not in self.definitions else target
            raise KeyError(f"unknown unit: {missing}")
        source_definition = self.definitions[source]
        target_definition = self.definitions[target]
        if source_definition.dimension != target_definition.dimension:
            raise DimensionMismatchError(
                f"conversion requires equal dimensions: {source} and {target}"
            )
        if exact_fraction(offset, name="offset") != 0 and not (source_definition.affine or target_definition.affine):
            raise ValueError("非仿射比例单位之间不能定义偏移转换")
        return self.graph.add_edge(source, target, scale, offset, domain=domain)

    def snapshot(self, version: int | None = None) -> ConversionGraphSnapshot:
        return self.graph.snapshot(version)

    def convert(self, value: ExactValue, source: str, target: str):
        return self.graph.convert(value, source, target)

    def validate_conversions(self, version: int | None = None):
        return self.graph.validate(version)
