
"""Exact finite Markov-chain absorption analysis and related utilities."""

from __future__ import annotations

from dataclasses import dataclass
from fractions import Fraction
from math import inf
from collections.abc import Collection
from typing import Mapping, Sequence
import random


Number = int | Fraction
Row = Sequence[Number] | Mapping[int, Number]
Matrix = Sequence[Row]
States = int | Collection[int]

ZERO = Fraction(0)
ONE = Fraction(1)


@dataclass(frozen=True)
class ChainAnalysis:
    labels: tuple[int, ...]
    closed_classes: tuple[tuple[int, ...], ...]
    absorbing_states: tuple[int, ...]
    state_to_closed_class: dict[int, int]
    transient_states: tuple[int, ...]
    hit_probability_by_class: dict[int, list[Fraction]]
    expected_absorption_time: list[Fraction]
    probability_residuals: dict[int, list[Fraction]]
    expected_time_residuals: list[Fraction]
    result_kind: str = "exact"


@dataclass(frozen=True)
class TargetAnalysis:
    target_label: int
    target_states: tuple[int, ...]
    hit_probability: list[Fraction]
    can_hit_target: list[bool]
    unconditional_expected_time: list[Fraction | None]
    conditional_expected_time: list[Fraction | None]
    hit_probability_residuals: list[Fraction]
    unconditional_expected_time_residuals: list[Fraction | None]
    conditional_expected_time_residuals: list[Fraction | None]
    result_kind: str = "exact"


@dataclass(frozen=True)
class LumpabilityWitness:
    group: int
    first_state: int
    second_state: int
    destination_group: int
    first_probability: Fraction
    second_probability: Fraction


@dataclass(frozen=True)
class LumpabilityResult:
    valid: bool
    labels: tuple[int, ...]
    lumped_matrix: tuple[tuple[Fraction, ...], ...]
    witness: LumpabilityWitness | None
    analysis: ChainAnalysis | None = None
    target_analysis: TargetAnalysis | None = None
    result_kind: str = "exact"


@dataclass(frozen=True)
class EmpiricalStateEstimate:
    state: int
    sample_size: int
    hits: int
    misses: int
    censored: int
    hit_frequency: Fraction
    finite_path_mean_steps: Fraction | None
    all_path_mean_steps: Fraction | None


@dataclass(frozen=True)
class EmpiricalTargetResult:
    target_label: int
    target_states: tuple[int, ...]
    seed: object
    sample_size: int
    max_steps: int
    random_source: str
    estimates: dict[int, EmpiricalStateEstimate]
    result_kind: str = "empirical"


@dataclass(frozen=True)
class RevisionComparison:
    state: int
    field: str
    incremental: Fraction | None
    full: Fraction | None
    match: bool


@dataclass(frozen=True)
class RevisionReport:
    row: int
    revised_row: tuple[Fraction, ...]
    row_sum: Fraction
    probability_bounds: tuple[Fraction, Fraction]
    before: ChainAnalysis
    after: ChainAnalysis
    incremental_analysis: ChainAnalysis
    removed_closed_classes: tuple[tuple[int, ...], ...]
    added_closed_classes: tuple[tuple[int, ...], ...]
    affected_states: tuple[int, ...]
    target_label: int | None
    incremental_target_analysis: TargetAnalysis | None
    full_target_analysis: TargetAnalysis | None
    target_status: str
    comparisons: tuple[RevisionComparison, ...]
    match: bool
    result_kind: str = "exact"


def _as_fraction(value: Number) -> Fraction:
    if isinstance(value, Fraction):
        return value
    if isinstance(value, int):
        return Fraction(value)
    raise TypeError("probabilities must be int or Fraction")


def _parse_row(row: Row, size: int, *, allow_missing: bool = False) -> list[Fraction]:
    if isinstance(row, Mapping):
        values = [ZERO] * size
        for key, raw_value in row.items():
            if not isinstance(key, int) or key < 0 or key >= size:
                raise ValueError(f"transition destination {key!r} is out of range")
            value = _as_fraction(raw_value)
            if value < 0:
                raise ValueError("transition probabilities must be non-negative")
            values[key] = value
        return values

    if not isinstance(row, Sequence) or isinstance(row, (str, bytes)):
        raise TypeError("each transition row must be a sequence or mapping")
    if allow_missing:
        if len(row) > size:
            raise ValueError("transition row is longer than matrix size")
        values = [ZERO] * size
        for column, raw_value in enumerate(row):
            value = _as_fraction(raw_value)
            if value < 0:
                raise ValueError("transition probabilities must be non-negative")
            values[column] = value
        return values

    if len(row) != size:
        raise ValueError("every transition row must have one entry per state")
    values = []
    for raw_value in row:
        value = _as_fraction(raw_value)
        if value < 0:
            raise ValueError("transition probabilities must be non-negative")
        values.append(value)
    return values


def validate_transition_matrix(matrix: Matrix) -> list[list[Fraction]]:
    if not isinstance(matrix, Sequence):
        raise TypeError("transition matrix must be a sequence")
    size = len(matrix)
    if size == 0:
        raise ValueError("transition matrix must contain at least one state")
    parsed = [_parse_row(row, size) for row in matrix]
    for state, row in enumerate(parsed):
        row_sum = sum(row, ZERO)
        if row_sum != ONE:
            raise ValueError(f"row {state} sums to {row_sum}, not 1")
    return parsed


def _build_graph(matrix: Sequence[Sequence[Fraction]]) -> list[list[int]]:
    return [
        [destination for destination, probability in enumerate(row) if probability != 0]
        for row in matrix
    ]


def _ordered_components(
    graph: Sequence[Sequence[int]],
) -> tuple[list[frozenset[int]], dict[int, int]]:
    size = len(graph)
    visited = [False] * size
    finished: list[int] = []

    for start in range(size):
        if visited[start]:
            continue
        stack: list[tuple[int, int]] = [(start, 0)]
        visited[start] = True
        while stack:
            state, edge_index = stack[-1]
            if edge_index < len(graph[state]):
                nxt = graph[state][edge_index]
                stack[-1] = (state, edge_index + 1)
                if not visited[nxt]:
                    visited[nxt] = True
                    stack.append((nxt, 0))
            else:
                finished.append(state)
                stack.pop()

    reverse_graph: list[list[int]] = [[] for _ in range(size)]
    for source, destinations in enumerate(graph):
        for destination in destinations:
            reverse_graph[destination].append(source)

    assigned = [False] * size
    raw_components: list[frozenset[int]] = []
    for start in reversed(finished):
        if assigned[start]:
            continue
        members: list[int] = []
        stack = [start]
        assigned[start] = True
        while stack:
            state = stack.pop()
            members.append(state)
            for nxt in reverse_graph[state]:
                if not assigned[nxt]:
                    assigned[nxt] = True
                    stack.append(nxt)
        raw_components.append(frozenset(members))

    raw_components.sort(key=lambda component: min(component))
    membership = {
        state: label
        for label, component in enumerate(raw_components)
        for state in component
    }
    return raw_components, membership


def _closed_components(
    graph: Sequence[Sequence[int]],
) -> tuple[list[frozenset[int]], dict[int, int | None]]:
    components, component_of = _ordered_components(graph)
    closed: list[frozenset[int]] = []
    closed_of: dict[int, int | None] = {state: None for state in range(len(graph))}
    label_remap: dict[int, int] = {}
    for component_label, component in enumerate(components):
        if all(destination in component for state in component for destination in graph[state]):
            new_label = len(closed)
            label_remap[component_label] = new_label
            closed.append(component)
            for state in component:
                closed_of[state] = new_label
    return closed, closed_of


def _solve_linear(
    matrix: Sequence[Sequence[Fraction]],
    vector: Sequence[Fraction],
) -> list[Fraction]:
    size = len(vector)
    augmented = [list(row) + [value] for row, value in zip(matrix, vector)]
    for pivot_column in range(size):
        pivot_row = next(
            (
                row
                for row in range(pivot_column, size)
                if augmented[row][pivot_column] != 0
            ),
            None,
        )
        if pivot_row is None:
            raise ValueError("linear system is singular")
        if pivot_row != pivot_column:
            augmented[pivot_column], augmented[pivot_row] = (
                augmented[pivot_row],
                augmented[pivot_column],
            )
        pivot = augmented[pivot_column][pivot_column]
        for column in range(pivot_column, size + 1):
            augmented[pivot_column][column] /= pivot
        for row in range(size):
            if row == pivot_column:
                continue
            factor = augmented[row][pivot_column]
            if factor == 0:
                continue
            for column in range(pivot_column, size + 1):
                augmented[row][column] -= factor * augmented[pivot_column][column]
    return [row[-1] for row in augmented]


def _solve_transient_system(
    matrix: Sequence[Sequence[Fraction]],
    transient_states: Sequence[int],
    boundary_vectors: Sequence[Sequence[Fraction]],
    constants: Sequence[Fraction],
) -> list[list[Fraction]]:
    transient_index = {state: index for index, state in enumerate(transient_states)}
    size = len(transient_states)
    coefficient = [[ZERO] * size for _ in range(size)]
    right_sides = [[ZERO] * size for _ in boundary_vectors]
    for row_index, state in enumerate(transient_states):
        coefficient[row_index][row_index] = ONE
        for destination, probability in enumerate(matrix[state]):
            if probability == 0:
                continue
            if destination in transient_index:
                coefficient[row_index][transient_index[destination]] -= probability
            else:
                for boundary_index, boundary in enumerate(boundary_vectors):
                    right_sides[boundary_index][row_index] += probability * boundary[destination]
        for vector, constant in zip(right_sides, constants):
            vector[row_index] += constant

    solutions = [_solve_linear(coefficient, vector) for vector in right_sides]
    return solutions


def _resolve_target(
    target: States,
    closed_classes: Sequence[frozenset[int]],
) -> tuple[int, frozenset[int]]:
    if isinstance(target, int):
        target_states: frozenset[int] = frozenset({target})
    elif isinstance(target, Collection) and not isinstance(target, (str, bytes)):
        if not all(isinstance(state, int) for state in target):
            raise ValueError("target states must be integers")
        target_states = frozenset(target)
    else:
        raise TypeError("target must be a state or a collection of states")
    for label, closed_class in enumerate(closed_classes):
        if target_states == closed_class:
            return label, closed_class
    raise ValueError("target must identify a closed class exactly")


def _build_target_analysis(
    matrix: Sequence[Sequence[Fraction]],
    target_label: int,
    target_states: frozenset[int],
    closed_of: Mapping[int, int | None],
) -> TargetAnalysis:
    size = len(matrix)
    transient_states = [state for state in range(size) if closed_of[state] is None]
    boundary_h = [
        ONE if state in target_states else ZERO if closed_of[state] is not None else ZERO
        for state in range(size)
    ]
    (h_transient,) = _solve_transient_system(matrix, transient_states, [boundary_h], [ZERO])
    hit_probability = list(boundary_h)
    for state, value in zip(transient_states, h_transient):
        hit_probability[state] = value

    boundary_numerator = [ZERO] * size
    (numerator_transient,) = _solve_transient_system(
        matrix,
        transient_states,
        [boundary_numerator],
        [ONE],
    )
    numerator = list(boundary_numerator)
    for state, value in zip(transient_states, numerator_transient):
        numerator[state] = value

    unconditional: list[Fraction | None] = [None] * size
    conditional: list[Fraction | None] = [None] * size
    for state in range(size):
        if state in target_states:
            unconditional[state] = ZERO
            conditional[state] = ZERO
        elif hit_probability[state] == ZERO:
            unconditional[state] = None
            conditional[state] = None
        elif hit_probability[state] == ONE:
            unconditional[state] = numerator[state]
            conditional[state] = numerator[state]
        else:
            unconditional[state] = None
            conditional[state] = numerator[state] / hit_probability[state]

    hit_residuals: list[Fraction] = [ZERO] * size
    unconditional_residuals: list[Fraction | None] = [None] * size
    conditional_residuals: list[Fraction | None] = [None] * size

    for state in range(size):
        if state in target_states:
            hit_residuals[state] = ZERO
            unconditional_residuals[state] = ZERO
            conditional_residuals[state] = ZERO
            continue

        hit_residual = hit_probability[state] - sum(
            probability * hit_probability[destination]
            for destination, probability in enumerate(matrix[state])
        )
        hit_residuals[state] = hit_residual

        if closed_of[state] is not None:
            continue

        expected_value = numerator[state]
        expected_residual = expected_value - ONE - sum(
            probability * numerator[destination]
            for destination, probability in enumerate(matrix[state])
        )
        if hit_probability[state] == ONE:
            unconditional_residuals[state] = expected_residual
        if hit_probability[state] > 0:
            conditional_value = conditional[state]
            assert conditional_value is not None
            conditional_residuals[state] = (
                hit_probability[state] * conditional_value
                - hit_probability[state]
                - sum(
                    probability * hit_probability[destination] * (conditional[destination] or ZERO)
                    for destination, probability in enumerate(matrix[state])
                )
            )

    return TargetAnalysis(
        target_label=target_label,
        target_states=tuple(sorted(target_states)),
        hit_probability=hit_probability,
        can_hit_target=[value > 0 for value in hit_probability],
        unconditional_expected_time=unconditional,
        conditional_expected_time=conditional,
        hit_probability_residuals=hit_residuals,
        unconditional_expected_time_residuals=unconditional_residuals,
        conditional_expected_time_residuals=conditional_residuals,
    )


def analyze_chain(matrix: Matrix) -> ChainAnalysis:
    """Identify closed classes and solve absorption probabilities and times."""
    parsed = validate_transition_matrix(matrix)
    graph = _build_graph(parsed)
    closed_sets, closed_of = _closed_components(graph)
    size = len(parsed)
    transient_states = [state for state in range(size) if closed_of[state] is None]

    boundary_probability = [
        {
            label: [ONE if closed_of[state] == label else ZERO for state in range(size)]
            for label in range(len(closed_sets))
        }
        for label in [0]
    ][0]
    probability_solutions = _solve_transient_system(
        parsed,
        transient_states,
        [boundary_probability[label] for label in range(len(closed_sets))],
        [ZERO] * len(closed_sets),
    )
    hit_probability_by_class: dict[int, list[Fraction]] = {}
    for label in range(len(closed_sets)):
        values = list(boundary_probability[label])
        for state, value in zip(transient_states, probability_solutions[label]):
            values[state] = value
        hit_probability_by_class[label] = values

    if transient_states:
        (expected_transient,) = _solve_transient_system(
            parsed,
            transient_states,
            [[ZERO] * size],
            [ONE],
        )
    else:
        expected_transient = []
    expected_time = [ZERO if closed_of[state] is not None else ZERO for state in range(size)]
    for state, value in zip(transient_states, expected_transient):
        expected_time[state] = value

    probability_residuals: dict[int, list[Fraction]] = {
        label: [ZERO] * size for label in range(len(closed_sets))
    }
    expected_residuals = [ZERO] * size
    for state in transient_states:
        for label, values in hit_probability_by_class.items():
            probability_residuals[label][state] = values[state] - sum(
                probability * values[destination]
                for destination, probability in enumerate(parsed[state])
            )
        expected_residuals[state] = expected_time[state] - ONE - sum(
            probability * expected_time[destination]
            for destination, probability in enumerate(parsed[state])
        )

    absorbing_states = tuple(
        state
        for closed_class in closed_sets
        if len(closed_class) == 1
        for state in closed_class
        if parsed[state][state] == ONE
    )

    return ChainAnalysis(
        labels=tuple(range(size)),
        closed_classes=tuple(tuple(sorted(closed_class)) for closed_class in closed_sets),
        absorbing_states=absorbing_states,
        state_to_closed_class=dict(closed_of),
        transient_states=tuple(transient_states),
        hit_probability_by_class=hit_probability_by_class,
        expected_absorption_time=expected_time,
        probability_residuals=probability_residuals,
        expected_time_residuals=expected_residuals,
    )


def analyze_target(matrix: Matrix, target: States) -> TargetAnalysis:
    """Solve target-class hit probabilities and conditional hitting times."""
    parsed = validate_transition_matrix(matrix)
    graph = _build_graph(parsed)
    _, closed_of = _closed_components(graph)
    closed_sets = [
        frozenset(state for state, label in closed_of.items() if label == class_label)
        for class_label in sorted({label for label in closed_of.values() if label is not None})
    ]
    target_label, target_states = _resolve_target(target, closed_sets)
    return _build_target_analysis(parsed, target_label, target_states, closed_of)


def _normalize_partition(
    partition: Sequence[Collection[int] | Sequence[int]],
    size: int,
) -> list[tuple[int, ...]]:
    groups: list[tuple[int, ...]] = []
    seen: set[int] = set()
    for group in partition:
        if isinstance(group, (str, bytes)) or not isinstance(group, Collection):
            raise TypeError("each visible-state group must be a collection of integers")
        members = tuple(sorted(group))
        if not members:
            raise ValueError("visible-state groups must be non-empty")
        for state in members:
            if not isinstance(state, int) or state < 0 or state >= size:
                raise ValueError(f"state {state!r} is outside the matrix")
            if state in seen:
                raise ValueError(f"state {state} appears in multiple groups")
            seen.add(state)
        groups.append(members)
    if seen != set(range(size)):
        raise ValueError("partition must contain every state exactly once")
    return groups


def check_strong_lumpability(
    matrix: Matrix,
    partition: Sequence[Collection[int] | Sequence[int]],
) -> LumpabilityResult:
    """Validate strong lumpability and return an exact witness when invalid."""
    parsed = validate_transition_matrix(matrix)
    size = len(parsed)
    groups = _normalize_partition(partition, size)
    state_to_group: dict[int, int] = {}
    for group_label, members in enumerate(groups):
        for state in members:
            state_to_group[state] = group_label

    witness: LumpabilityWitness | None = None
    for group_label, members in enumerate(groups):
        representative = members[0]
        representative_probabilities = {
            destination_group: sum(
                parsed[representative][destination]
                for destination in range(size)
                if state_to_group[destination] == destination_group
            )
            for destination_group in range(len(groups))
        }
        for state in members[1:]:
            for destination_group in range(len(groups)):
                probability = sum(
                    parsed[state][destination]
                    for destination in range(size)
                    if state_to_group[destination] == destination_group
                )
                if probability != representative_probabilities[destination_group]:
                    witness = LumpabilityWitness(
                        group=group_label,
                        first_state=representative,
                        second_state=state,
                        destination_group=destination_group,
                        first_probability=representative_probabilities[destination_group],
                        second_probability=probability,
                    )
                    break
            if witness is not None:
                break
        if witness is not None:
            break

    if witness is not None:
        return LumpabilityResult(
            valid=False,
            labels=tuple(range(size)),
            lumped_matrix=(),
            witness=witness,
        )

    lumped_matrix = []
    for members in groups:
        representative = members[0]
        lumped_matrix.append(
            tuple(
                sum(
                    parsed[representative][destination]
                    for destination in groups[destination_group]
                )
                for destination_group in range(len(groups))
            )
        )
    return LumpabilityResult(
        valid=True,
        labels=tuple(range(size)),
        lumped_matrix=tuple(lumped_matrix),
        witness=None,
    )


def analyze_lumped(
    matrix: Matrix,
    partition: Sequence[Collection[int] | Sequence[int]],
    target_group: int | None = None,
) -> LumpabilityResult:
    """Check lumpability, then solve the lumped chain when compression is valid."""
    result = check_strong_lumpability(matrix, partition)
    if not result.valid:
        return result
    analysis = analyze_chain(result.lumped_matrix)
    target_analysis = None
    if target_group is not None:
        if not isinstance(target_group, int) or target_group < 0 or target_group >= len(
            result.lumped_matrix
        ):
            raise ValueError("target_group is outside the partition")
        target_analysis = analyze_target(result.lumped_matrix, target_group)
    return LumpabilityResult(
        valid=True,
        labels=result.labels,
        lumped_matrix=result.lumped_matrix,
        witness=None,
        analysis=analysis,
        target_analysis=target_analysis,
    )


def _weights(row: Sequence[Fraction]) -> tuple[list[int], list[int], int]:
    destinations = [state for state, probability in enumerate(row) if probability != 0]
    denominators = [row[state].denominator for state in destinations]
    common = 1
    for denominator in denominators:
        common = common * denominator // _gcd(common, denominator)
    weights = [int(row[state] * common) for state in destinations]
    return destinations, weights, sum(weights)


def _gcd(left: int, right: int) -> int:
    while right:
        left, right = right, left % right
    return left


def _sample_next(
    rng: random.Random,
    destinations: Sequence[int],
    weights: Sequence[int],
    total_weight: int,
) -> int:
    return rng.choices(destinations, weights=weights, k=1)[0]


def _random_source_name(rng: object) -> str:
    module = type(rng).__module__
    name = type(rng).__name__
    if module == "builtins":
        return name
    return f"{module}.{name}"


def estimate_target_absorption(
    matrix: Matrix,
    target: States,
    *,
    rng: random.Random,
    seed: object = "unspecified",
    sample_size: int = 10_000,
    max_steps: int = 10_000,
    initial_states: Collection[int] | None = None,
) -> EmpiricalTargetResult:
    """Estimate target hits with a caller-injected random source."""
    parsed = validate_transition_matrix(matrix)
    exact = analyze_target(parsed, target)
    target_states = frozenset(exact.target_states)
    size = len(parsed)
    if initial_states is None:
        initial_states = range(size)
    starts = list(initial_states)
    if any(not isinstance(state, int) or state < 0 or state >= size for state in starts):
        raise ValueError("initial_states contains an invalid state")
    if not isinstance(sample_size, int) or sample_size <= 0:
        raise ValueError("sample_size must be a positive integer")
    if not isinstance(max_steps, int) or max_steps < 0:
        raise ValueError("max_steps must be a non-negative integer")

    rows = [_weights(row) for row in parsed]
    estimates: dict[int, EmpiricalStateEstimate] = {}
    for initial_state in starts:
        hits = 0
        misses = 0
        censored = 0
        finite_step_total = 0
        all_step_total = 0
        for _ in range(sample_size):
            state = initial_state
            steps = 0
            outcome = None
            while steps <= max_steps:
                if state in target_states:
                    outcome = "hit"
                    break
                if exact.hit_probability[state] == ZERO and state != initial_state:
                    outcome = "miss"
                    break
                if exact.hit_probability[state] == ZERO and steps > 0:
                    outcome = "miss"
                    break
                destinations, weights, total_weight = rows[state]
                state = _sample_next(rng, destinations, weights, total_weight)
                steps += 1
                if steps > max_steps:
                    break
            if outcome == "hit":
                hits += 1
                finite_step_total += steps
            elif outcome == "miss":
                misses += 1
            else:
                censored += 1
            all_step_total += steps

        terminated = hits + misses
        estimates[initial_state] = EmpiricalStateEstimate(
            state=initial_state,
            sample_size=sample_size,
            hits=hits,
            misses=misses,
            censored=censored,
            hit_frequency=Fraction(hits, sample_size),
            finite_path_mean_steps=(
                Fraction(finite_step_total, hits) if hits else None
            ),
            all_path_mean_steps=Fraction(all_step_total, sample_size),
        )

    return EmpiricalTargetResult(
        target_label=exact.target_label,
        target_states=exact.target_states,
        seed=seed,
        sample_size=sample_size,
        max_steps=max_steps,
        random_source=_random_source_name(rng),
        estimates=estimates,
    )
