"""Exact integer linear algebra used by the unit constraint solver.

The module intentionally uses only Python integers. In particular, no
floating-point values are used to decide whether an exponent or a dimension
assignment is valid.
"""

from __future__ import annotations

from dataclasses import dataclass
from typing import Iterable, Sequence


IntMatrix = list[list[int]]


def _identity(size: int) -> IntMatrix:
    return [[1 if row == col else 0 for col in range(size)] for row in range(size)]


def _copy_matrix(matrix: Sequence[Sequence[int]]) -> IntMatrix:
    copied = [[int(value) for value in row] for row in matrix]
    if copied:
        width = len(copied[0])
        if any(len(row) != width for row in copied):
            raise ValueError("matrix rows must all have the same length")
    return copied


def _swap_rows(matrix: IntMatrix, left: int, right: int) -> None:
    if left != right:
        matrix[left], matrix[right] = matrix[right], matrix[left]


def _swap_columns(matrix: IntMatrix, left: int, right: int) -> None:
    if left == right:
        return
    for row in matrix:
        row[left], row[right] = row[right], row[left]


def _add_row_multiple(
    matrix: IntMatrix, target: int, source: int, factor: int
) -> None:
    if factor == 0:
        return
    matrix[target] = [
        target_value + factor * source_value
        for target_value, source_value in zip(matrix[target], matrix[source])
    ]


def _add_column_multiple(
    matrix: IntMatrix, target: int, source: int, factor: int
) -> None:
    if factor == 0:
        return
    for row in matrix:
        row[target] += factor * row[source]


def _negate_row(matrix: IntMatrix, row: int) -> None:
    matrix[row] = [-value for value in matrix[row]]


def smith_normal_form(matrix: Sequence[Sequence[int]]) -> tuple[IntMatrix, IntMatrix, IntMatrix]:
    """Return ``(D, U, V)`` with ``D = U * matrix * V`` in Smith normal form.

    ``U`` and ``V`` are unimodular integer matrices. The diagonal entries of
    ``D`` are non-negative and each non-zero diagonal entry divides the next
    one. The algorithm uses only row/column operations over the integers.
    """

    diagonal = _copy_matrix(matrix)
    row_count = len(diagonal)
    column_count = len(diagonal[0]) if diagonal else 0
    row_transform = _identity(row_count)
    column_transform = _identity(column_count)

    pivot_index = 0
    while pivot_index < row_count and pivot_index < column_count:
        nonzero_position: tuple[int, int] | None = None
        for row in range(pivot_index, row_count):
            for column in range(pivot_index, column_count):
                if diagonal[row][column] != 0:
                    nonzero_position = (row, column)
                    break
            if nonzero_position is not None:
                break
        if nonzero_position is None:
            break

        row, column = nonzero_position
        _swap_rows(diagonal, pivot_index, row)
        _swap_rows(row_transform, pivot_index, row)
        _swap_columns(diagonal, pivot_index, column)
        _swap_columns(column_transform, pivot_index, column)

        while True:
            restarted = False

            for row in range(pivot_index + 1, row_count):
                if diagonal[row][pivot_index] == 0:
                    continue
                quotient = diagonal[row][pivot_index] // diagonal[pivot_index][pivot_index]
                _add_row_multiple(diagonal, row, pivot_index, -quotient)
                _add_row_multiple(row_transform, row, pivot_index, -quotient)
                if diagonal[row][pivot_index] != 0:
                    _swap_rows(diagonal, row, pivot_index)
                    _swap_rows(row_transform, row, pivot_index)
                    restarted = True
                    break
            if restarted:
                continue

            for column in range(pivot_index + 1, column_count):
                if diagonal[pivot_index][column] == 0:
                    continue
                quotient = diagonal[pivot_index][column] // diagonal[pivot_index][pivot_index]
                _add_column_multiple(diagonal, column, pivot_index, -quotient)
                _add_column_multiple(column_transform, column, pivot_index, -quotient)
                if diagonal[pivot_index][column] != 0:
                    _swap_columns(diagonal, column, pivot_index)
                    _swap_columns(column_transform, column, pivot_index)
                    restarted = True
                    break
            if restarted:
                continue

            bad_entry: tuple[int, int] | None = None
            pivot = diagonal[pivot_index][pivot_index]
            for row in range(pivot_index + 1, row_count):
                for column in range(pivot_index + 1, column_count):
                    if diagonal[row][column] % pivot != 0:
                        bad_entry = (row, column)
                        break
                if bad_entry is not None:
                    break

            if bad_entry is None:
                break

            bad_row, _ = bad_entry
            _add_row_multiple(diagonal, pivot_index, bad_row, 1)
            _add_row_multiple(row_transform, pivot_index, bad_row, 1)

        if diagonal[pivot_index][pivot_index] < 0:
            _negate_row(diagonal, pivot_index)
            _negate_row(row_transform, pivot_index)
        pivot_index += 1

    return diagonal, row_transform, column_transform


def _matrix_vector_product(matrix: Sequence[Sequence[int]], vector: Sequence[int]) -> list[int]:
    return [
        sum(coefficient * value for coefficient, value in zip(row, vector))
        for row in matrix
    ]


@dataclass(frozen=True)
class SolutionVerification:
    """Independent substitution result for every original equation."""

    particular_residuals: tuple[int, ...]
    basis_residuals: tuple[tuple[int, ...], ...]

    @property
    def ok(self) -> bool:
        return all(residual == 0 for residual in self.particular_residuals) and all(
            residual == 0
            for row in self.basis_residuals
            for residual in row
        )


@dataclass(frozen=True)
class IntegerSolutionLattice:
    """All integer solutions as ``particular + Z-span(basis)``."""

    particular: tuple[int, ...]
    basis: tuple[tuple[int, ...], ...]
    verification: SolutionVerification

    @property
    def free_rank(self) -> int:
        return len(self.basis)

    def evaluate(self, parameters: Sequence[int] = ()) -> tuple[int, ...]:
        if len(parameters) != self.free_rank:
            raise ValueError(
                f"expected {self.free_rank} integer parameters, got {len(parameters)}"
            )
        result = list(self.particular)
        for parameter, basis_vector in zip(parameters, self.basis):
            for index, coefficient in enumerate(basis_vector):
                result[index] += parameter * coefficient
        return tuple(result)


@dataclass(frozen=True)
class InconsistencyEvidence:
    """Certificate that an integer system has no solution."""

    combination: tuple[int, ...]
    lhs_combination: tuple[int, ...]
    rhs_combination: int
    divisor: int | None = None

    def describe(self) -> str:
        if self.divisor is None:
            return (
                "原始约束的整数组合给出 "
                f"{list(self.lhs_combination)} = {self.rhs_combination}，"
                "左侧恒为 0 而右侧非 0"
            )
        return (
            "原始约束的整数组合给出 "
            f"{list(self.lhs_combination)} = {self.rhs_combination}，"
            f"左侧必被 {self.divisor} 整除而右侧不能"
        )


def verify_integer_solution(
    matrix: Sequence[Sequence[int]],
    rhs: Sequence[int],
    particular: Sequence[int],
    basis: Iterable[Sequence[int]],
) -> SolutionVerification:
    """Verify a solution lattice by substituting it into the original system."""

    particular_residuals = tuple(
        actual - expected
        for actual, expected in zip(_matrix_vector_product(matrix, particular), rhs)
    )
    basis_residuals = tuple(
        tuple(_matrix_vector_product(matrix, basis_vector)) for basis_vector in basis
    )
    return SolutionVerification(particular_residuals, basis_residuals)


def solve_integer_system(
    matrix: Sequence[Sequence[int]], rhs: Sequence[int]
) -> IntegerSolutionLattice | InconsistencyEvidence:
    """Solve ``matrix * x = rhs`` over the integers exactly.

    The result is either an explicit solution lattice or a small inconsistency
    certificate derived from the Smith normal form.
    """

    coefficients = _copy_matrix(matrix)
    constants = [int(value) for value in rhs]
    if len(coefficients) != len(constants):
        raise ValueError("matrix row count must match rhs length")
    variable_count = len(coefficients[0]) if coefficients else 0

    diagonal, row_transform, column_transform = smith_normal_form(coefficients)
    transformed_rhs = _matrix_vector_product(row_transform, constants)
    rank = 0
    limit = min(len(diagonal), variable_count)
    while rank < limit and diagonal[rank][rank] != 0:
        rank += 1

    reduced_solution = [0] * variable_count
    for row in range(rank):
        divisor = diagonal[row][row]
        if transformed_rhs[row] % divisor != 0:
            combination = tuple(row_transform[row])
            lhs_combination = tuple(
                sum(
                    combination[equation_index] * coefficients[equation_index][variable]
                    for equation_index in range(len(coefficients))
                )
                for variable in range(variable_count)
            )
            return InconsistencyEvidence(
                combination=combination,
                lhs_combination=lhs_combination,
                rhs_combination=transformed_rhs[row],
                divisor=divisor,
            )
        reduced_solution[row] = transformed_rhs[row] // divisor

    for row in range(rank, len(coefficients)):
        if transformed_rhs[row] != 0:
            combination = tuple(row_transform[row])
            lhs_combination = tuple(
                sum(
                    combination[equation_index] * coefficients[equation_index][variable]
                    for equation_index in range(len(coefficients))
                )
                for variable in range(variable_count)
            )
            return InconsistencyEvidence(
                combination=combination,
                lhs_combination=lhs_combination,
                rhs_combination=transformed_rhs[row],
            )

    particular = tuple(
        sum(
            column_transform[variable][reduced_index] * reduced_solution[reduced_index]
            for reduced_index in range(variable_count)
        )
        for variable in range(variable_count)
    )
    basis = tuple(
        tuple(column_transform[variable][free_index] for variable in range(variable_count))
        for free_index in range(rank, variable_count)
    )
    verification = verify_integer_solution(coefficients, constants, particular, basis)
    if not verification.ok:
        raise ArithmeticError("internal error: Smith normal form solution failed verification")
    return IntegerSolutionLattice(particular, basis, verification)
