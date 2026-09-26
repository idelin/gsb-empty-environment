from __future__ import annotations

from dataclasses import dataclass
from enum import Enum
from fractions import Fraction
from typing import Hashable, Iterable


class OptimizationStatus(Enum):
    INFEASIBLE = "infeasible"
    OPTIMAL = "optimal"
    FEASIBLE = "feasible"


class BudgetStatus(Enum):
    INFEASIBLE = "infeasible"
    FEASIBLE = "feasible"
    UNDECIDED = "undecided"


@dataclass(frozen=True)
class Task:
    task_id: Hashable
    start: int | str | Fraction
    end: int | str | Fraction
    resource: int | None = None


@dataclass(frozen=True)
class OptimizationResult:
    status: OptimizationStatus
    resource_count: int | None
    assignment: dict[Hashable, int]
    lower_bound: int
    upper_bound: int | None
    peak_overlap: int
    nodes_explored: int
    conflict: tuple[Hashable, Hashable, int] | None


@dataclass(frozen=True)
class BudgetResult:
    status: BudgetStatus
    budget: int
    assignment: dict[Hashable, int]
    lower_bound: int
    upper_bound: int | None
    nodes_explored: int
    conflict: tuple[Hashable, Hashable, int] | None


@dataclass(frozen=True)
class _Model:
    ids: list[Hashable]
    starts: list[Fraction]
    ends: list[Fraction]
    fixed_colors: dict[int, int]
    color_labels: dict[int, int]
    adjacency: list[set[int]]
    has_extra_constraints: bool
    conflict: tuple[Hashable, Hashable, int] | None


class _NodeLimitReached(Exception):
    pass


def parse_number(value: int | str | Fraction) -> Fraction:
    if isinstance(value, bool) or isinstance(value, float):
        raise TypeError("endpoints must be int, str, or Fraction; floats are not accepted")
    if isinstance(value, (int, Fraction)):
        return Fraction(value)
    if isinstance(value, str):
        return Fraction(value)
    raise TypeError("endpoints must be int, str, or Fraction")


def _valid_budget(value: int) -> int:
    if not isinstance(value, int) or isinstance(value, bool) or value < 0:
        raise ValueError("budget must be a non-negative integer")
    return value


def _valid_node_limit(value: int) -> int:
    if not isinstance(value, int) or isinstance(value, bool) or value <= 0:
        raise ValueError("node_limit must be a positive integer")
    return value


def _build_model(tasks: Iterable[Task], forbidden_pairs: Iterable[tuple[Hashable, Hashable]] = ()) -> _Model:
    task_list = list(tasks)
    pair_list = list(forbidden_pairs)
    starts: list[Fraction] = []
    ends: list[Fraction] = []
    fixed_labels: dict[int, int] = {}

    seen: set[Hashable] = set()
    for position, task in enumerate(task_list):
        if task.task_id in seen:
            raise ValueError(f"duplicate task id: {task.task_id!r}")
        seen.add(task.task_id)
        start = parse_number(task.start)
        end = parse_number(task.end)
        if start >= end:
            raise ValueError(f"task {task.task_id!r} requires start < end")
        starts.append(start)
        ends.append(end)
        if task.resource is not None:
            if not isinstance(task.resource, int) or isinstance(task.resource, bool) or task.resource < 0:
                raise ValueError(f"task {task.task_id!r} resource must be a non-negative integer")
            fixed_labels[position] = task.resource

    index = {task.task_id: i for i, task in enumerate(task_list)}
    count = len(task_list)
    adjacency = [set() for _ in range(count)]

    def add_edge(left: int, right: int) -> None:
        adjacency[left].add(right)
        adjacency[right].add(left)

    for i in range(count):
        for j in range(i + 1, count):
            if starts[i] < ends[j] and starts[j] < ends[i]:
                add_edge(i, j)

    for pair in pair_list:
        try:
            left_id, right_id = pair
        except (TypeError, ValueError) as exc:
            raise ValueError("each forbidden pair must contain exactly two task ids") from exc
        if left_id not in index or right_id not in index:
            raise ValueError(f"unknown task in forbidden pair: {(left_id, right_id)!r}")
        left = index[left_id]
        right = index[right_id]
        if left == right:
            raise ValueError(f"task {left_id!r} cannot be forbidden from sharing a resource with itself")
        add_edge(left, right)

    distinct_labels = sorted(set(fixed_labels.values()))
    label_to_color = {label: color for color, label in enumerate(distinct_labels)}
    fixed_colors = {node: label_to_color[label] for node, label in fixed_labels.items()}
    color_labels = {color: label for label, color in label_to_color.items()}

    conflict = None
    for left in range(count):
        for right in adjacency[left]:
            if left < right and left in fixed_colors and fixed_colors[left] == fixed_colors.get(right):
                conflict = (
                    task_list[left].task_id,
                    task_list[right].task_id,
                    color_labels[fixed_colors[left]],
                )
                break
        if conflict is not None:
            break

    return _Model(
        ids=[task.task_id for task in task_list],
        starts=starts,
        ends=ends,
        fixed_colors=fixed_colors,
        color_labels=color_labels,
        adjacency=adjacency,
        has_extra_constraints=bool(fixed_colors) or bool(pair_list),
        conflict=conflict,
    )


def _overlap_peak(starts: list[Fraction], ends: list[Fraction]) -> int:
    events: list[tuple[Fraction, int]] = []
    for start, end in zip(starts, ends):
        events.append((start, 1))
        events.append((end, -1))
    events.sort(key=lambda event: (event[0], event[1]))
    active = 0
    peak = 0
    for _, change in events:
        active += change
        peak = max(peak, active)
    return peak


def peak_overlap(tasks: Iterable[Task]) -> int:
    model = _build_model(tasks)
    return _overlap_peak(model.starts, model.ends)


def _clique_lower_bound(adjacency: list[set[int]]) -> int:
    best = 0
    for seed in range(len(adjacency)):
        candidates = set(adjacency[seed])
        clique_size = 1
        while candidates:
            node = max(candidates, key=lambda item: (len(adjacency[item]), -item))
            clique_size += 1
            candidates &= adjacency[node]
        best = max(best, clique_size)
    return best


def _new_color_label(color_labels: dict[int, int]) -> int:
    used = set(color_labels.values())
    label = 0
    while label in used:
        label += 1
    return label


def _greedy_assignment(model: _Model, budget: int | None = None) -> dict[int, int]:
    colors = dict(model.fixed_colors)
    color_labels = dict(model.color_labels)
    capacity = budget if budget is not None else len(model.ids)
    if len(color_labels) > capacity:
        return {}

    nodes = sorted(
        (node for node in range(len(model.ids)) if node not in colors),
        key=lambda node: (model.starts[node], -len(model.adjacency[node]), node),
    )
    for node in nodes:
        forbidden = {colors[neighbor] for neighbor in model.adjacency[node] if neighbor in colors}
        available = [color for color in range(len(color_labels)) if color not in forbidden]
        if available:
            colors[node] = min(available)
            continue
        next_color = len(color_labels)
        if next_color >= capacity:
            return {}
        color_labels[next_color] = _new_color_label(color_labels)
        colors[node] = next_color

    return colors


def _public_assignment(model: _Model, colors: dict[int, int]) -> dict[Hashable, int]:
    labels = dict(model.color_labels)
    for color in set(colors.values()):
        if color not in labels:
            labels[color] = _new_color_label(labels)
    return {model.ids[node]: labels[color] for node, color in colors.items()}


def _initial_domains(model: _Model, budget: int) -> list[frozenset[int]] | None:
    domains = [frozenset(range(budget)) for _ in model.ids]
    for node, color in model.fixed_colors.items():
        if color >= budget:
            return None
        domains[node] = frozenset({color})
    for node, color in model.fixed_colors.items():
        for neighbor in model.adjacency[node]:
            if neighbor in model.fixed_colors:
                continue
            remaining = set(domains[neighbor])
            remaining.discard(color)
            if not remaining:
                return None
            domains[neighbor] = frozenset(remaining)
    for node in range(len(model.ids)):
        if not domains[node]:
            return None
    return domains


def _search_coloring(
    model: _Model,
    budget: int,
    node_limit: int,
    nodes_explored: int = 0,
) -> tuple[dict[int, int] | None, int]:
    domains = _initial_domains(model, budget)
    if domains is None:
        return None, nodes_explored

    def backtrack(colors: dict[int, int], domains: list[frozenset[int]]) -> dict[int, int] | None:
        nonlocal nodes_explored
        nodes_explored += 1
        if nodes_explored > node_limit:
            raise _NodeLimitReached
        if len(colors) == len(model.ids):
            return colors

        uncolored = [node for node in range(len(model.ids)) if node not in colors]
        node = min(
            uncolored,
            key=lambda item: (
                len(domains[item]),
                -sum(neighbor in colors for neighbor in model.adjacency[item]),
                -len(model.adjacency[item]),
                item,
            ),
        )
        used = sorted(set(colors.values()))
        candidates = [color for color in used if color in domains[node]]
        candidates.extend(color for color in sorted(domains[node]) if color not in used)

        for color in candidates:
            next_domains = list(domains)
            next_domains[node] = frozenset({color})
            for neighbor in model.adjacency[node]:
                if neighbor not in colors:
                    remaining = set(next_domains[neighbor])
                    remaining.discard(color)
                    if not remaining:
                        break
                    next_domains[neighbor] = frozenset(remaining)
            else:
                next_colors = dict(colors)
                next_colors[node] = color
                solution = backtrack(next_colors, next_domains)
                if solution is not None:
                    return solution
        return None

    try:
        return backtrack(dict(model.fixed_colors), domains), nodes_explored
    except _NodeLimitReached:
        return None, nodes_explored


def _bounds(model: _Model) -> tuple[int, int, int]:
    peak = _overlap_peak(model.starts, model.ends)
    graph_lower = _clique_lower_bound(model.adjacency)
    fixed_count = len(model.color_labels)
    lower = max(peak, graph_lower, fixed_count)
    greedy = _greedy_assignment(model)
    upper = len(set(greedy.values())) if greedy else len(model.ids)
    return lower, upper, peak


def minimize_resources(
    tasks: Iterable[Task],
    forbidden_pairs: Iterable[tuple[Hashable, Hashable]] = (),
    node_limit: int = 100_000,
) -> OptimizationResult:
    node_limit = _valid_node_limit(node_limit)
    model = _build_model(tasks, forbidden_pairs)
    lower, upper, peak = _bounds(model)

    if model.conflict is not None:
        return OptimizationResult(
            status=OptimizationStatus.INFEASIBLE,
            resource_count=None,
            assignment={},
            lower_bound=lower,
            upper_bound=None,
            peak_overlap=peak,
            nodes_explored=0,
            conflict=model.conflict,
        )

    if not model.has_extra_constraints:
        return OptimizationResult(
            status=OptimizationStatus.OPTIMAL,
            resource_count=peak,
            assignment=_public_assignment(model, _greedy_assignment(model)),
            lower_bound=peak,
            upper_bound=peak,
            peak_overlap=peak,
            nodes_explored=0,
            conflict=None,
        )

    if lower == upper:
        colors = _greedy_assignment(model, upper)
        return OptimizationResult(
            status=OptimizationStatus.OPTIMAL,
            resource_count=upper,
            assignment=_public_assignment(model, colors),
            lower_bound=lower,
            upper_bound=upper,
            peak_overlap=peak,
            nodes_explored=0,
            conflict=None,
        )

    nodes_explored = 0
    for budget in range(lower, upper + 1):
        colors, nodes_explored = _search_coloring(model, budget, node_limit, nodes_explored)
        if colors is not None:
            optimal = budget == lower
            return OptimizationResult(
                status=OptimizationStatus.OPTIMAL if optimal else OptimizationStatus.FEASIBLE,
                resource_count=budget,
                assignment=_public_assignment(model, colors),
                lower_bound=lower,
                upper_bound=budget,
                peak_overlap=peak,
                nodes_explored=nodes_explored,
                conflict=None,
            )
        if nodes_explored >= node_limit:
            break

    fallback = _greedy_assignment(model)
    return OptimizationResult(
        status=OptimizationStatus.FEASIBLE,
        resource_count=len(set(fallback.values())),
        assignment=_public_assignment(model, fallback),
        lower_bound=lower,
        upper_bound=len(set(fallback.values())),
        peak_overlap=peak,
        nodes_explored=nodes_explored,
        conflict=None,
    )


def check_budget(
    tasks: Iterable[Task],
    budget: int,
    forbidden_pairs: Iterable[tuple[Hashable, Hashable]] = (),
    node_limit: int = 100_000,
) -> BudgetResult:
    budget = _valid_budget(budget)
    node_limit = _valid_node_limit(node_limit)
    model = _build_model(tasks, forbidden_pairs)
    lower, upper, _ = _bounds(model)

    if model.conflict is not None:
        return BudgetResult(
            status=BudgetStatus.INFEASIBLE,
            budget=budget,
            assignment={},
            lower_bound=lower,
            upper_bound=None,
            nodes_explored=0,
            conflict=model.conflict,
        )

    if budget < lower:
        return BudgetResult(
            status=BudgetStatus.INFEASIBLE,
            budget=budget,
            assignment={},
            lower_bound=lower,
            upper_bound=upper,
            nodes_explored=0,
            conflict=None,
        )

    if budget >= upper:
        colors = _greedy_assignment(model, upper)
        return BudgetResult(
            status=BudgetStatus.FEASIBLE,
            budget=budget,
            assignment=_public_assignment(model, colors),
            lower_bound=lower,
            upper_bound=upper,
            nodes_explored=0,
            conflict=None,
        )

    colors, nodes_explored = _search_coloring(model, budget, node_limit)
    if colors is not None:
        return BudgetResult(
            status=BudgetStatus.FEASIBLE,
            budget=budget,
            assignment=_public_assignment(model, colors),
            lower_bound=lower,
            upper_bound=upper,
            nodes_explored=nodes_explored,
            conflict=None,
        )

    if nodes_explored >= node_limit:
        status = BudgetStatus.UNDECIDED
    else:
        status = BudgetStatus.INFEASIBLE
    return BudgetResult(
        status=status,
        budget=budget,
        assignment={},
        lower_bound=lower,
        upper_bound=upper,
        nodes_explored=nodes_explored,
        conflict=None,
    )
