"""Versioned exact conversion graph for proportional and affine units."""

from __future__ import annotations

from collections import deque
from dataclasses import dataclass
from fractions import Fraction
from types import MappingProxyType
from typing import Mapping, Sequence


ExactValue = int | str | Fraction


def exact_fraction(value: ExactValue, *, name: str = "value") -> Fraction:
    """Convert an exact user value to ``Fraction`` and reject floats."""

    if isinstance(value, float):
        raise TypeError(f"{name} must be exact (int, str, or Fraction), not float")
    return Fraction(value)


@dataclass(frozen=True)
class Transform:
    """Exact transform ``y = scale * x + offset`` with an explicit domain."""

    scale: Fraction
    offset: Fraction = Fraction(0)
    domain: str = ""

    def __post_init__(self) -> None:
        scale = exact_fraction(self.scale, name="scale")
        offset = exact_fraction(self.offset, name="offset")
        if scale == 0:
            raise ValueError("conversion scale must be non-zero")
        object.__setattr__(self, "scale", scale)
        object.__setattr__(self, "offset", offset)
        if not self.domain:
            domain = (
                "比例/差量单位；适用于所有精确有理数值；不表示绝对仿射零点"
                if offset == 0
                else "绝对仿射单位；仅用于绝对量加减和求差，禁止直接参与乘除"
            )
            object.__setattr__(self, "domain", domain)

    @property
    def kind(self) -> str:
        return "proportional" if self.offset == 0 else "affine"

    def apply(self, value: ExactValue) -> Fraction:
        return self.scale * exact_fraction(value) + self.offset

    def inverse(self) -> "Transform":
        return Transform(
            scale=Fraction(1, 1) / self.scale,
            offset=-self.offset / self.scale,
            domain=f"逆变换；{self.domain}",
        )

    def then(self, next_transform: "Transform") -> "Transform":
        """Compose this transform followed by ``next_transform``."""

        return Transform(
            scale=next_transform.scale * self.scale,
            offset=next_transform.scale * self.offset + next_transform.offset,
            domain=f"复合变换；第一步适用域：{self.domain}；第二步适用域：{next_transform.domain}",
        )

    def formula(self, variable: str = "x") -> str:
        scaled = f"{self.scale}*{variable}"
        if self.offset == 0:
            return scaled
        sign = "+" if self.offset >= 0 else "-"
        return f"{scaled} {sign} {abs(self.offset)}"


@dataclass(frozen=True)
class ConversionStep:
    source: str
    target: str
    transform: Transform

    @property
    def explanation(self) -> str:
        return (
            f"{self.source} -> {self.target}: y = {self.transform.formula()}；"
            f"适用域：{self.transform.domain}"
        )

    def reversed(self) -> "ConversionStep":
        return ConversionStep(self.target, self.source, self.transform.inverse())


@dataclass(frozen=True)
class ConversionResult:
    value: Fraction
    path: tuple[str, ...]
    steps: tuple[ConversionStep, ...]

    @property
    def explanation(self) -> str:
        if not self.steps:
            return "恒等转换；适用域：同一单位内的所有精确有理数值"
        return "\n".join(step.explanation for step in self.steps)


@dataclass(frozen=True)
class EdgeCheck:
    source: str
    target: str
    samples: tuple[Fraction, ...]
    residuals: tuple[Fraction, ...]

    @property
    def ok(self) -> bool:
        return all(residual == 0 for residual in self.residuals)


@dataclass(frozen=True)
class CycleContradiction:
    path: tuple[str, ...]
    steps: tuple[ConversionStep, ...]
    composed: Transform
    sample_residuals: tuple[Fraction, ...]

    @property
    def scale_residual(self) -> Fraction:
        return self.composed.scale - 1

    @property
    def offset_residual(self) -> Fraction:
        return self.composed.offset

    def describe(self) -> str:
        return (
            f"矛盾环 {' -> '.join(self.path)}：复合比例残差 "
            f"{self.scale_residual}，复合偏移残差 {self.offset_residual}"
        )


@dataclass(frozen=True)
class GraphValidation:
    version: int
    edge_checks: tuple[EdgeCheck, ...]
    contradictions: tuple[CycleContradiction, ...]

    @property
    def ok(self) -> bool:
        return all(check.ok for check in self.edge_checks) and not self.contradictions


@dataclass(frozen=True)
class GraphRevision:
    version: int
    action: str
    node_count: int
    edge_count: int


def _identity_transform() -> Transform:
    return Transform(Fraction(1), Fraction(0), "恒等变换；同一单位内的所有精确有理数值")


def _find_path(
    source: str,
    target: str,
    nodes: frozenset[str],
    edges: Mapping[tuple[str, str], Transform],
) -> tuple[ConversionStep, ...]:
    if source not in nodes or target not in nodes:
        missing = source if source not in nodes else target
        raise KeyError(f"unknown unit in this graph version: {missing}")
    if source == target:
        return ()

    adjacency: dict[str, list[tuple[str, Transform]]] = {node: [] for node in nodes}
    for (edge_source, edge_target), transform in edges.items():
        adjacency[edge_source].append((edge_target, transform))
        adjacency[edge_target].append((edge_source, transform.inverse()))

    queue: deque[str] = deque([source])
    paths: dict[str, tuple[ConversionStep, ...]] = {source: ()}
    while queue:
        current = queue.popleft()
        for neighbor, transform in adjacency[current]:
            if neighbor in paths:
                continue
            paths[neighbor] = paths[current] + (
                ConversionStep(current, neighbor, transform),
            )
            if neighbor == target:
                return paths[neighbor]
            queue.append(neighbor)
    raise ValueError(f"no conversion path from {source} to {target}")


def _convert_value(
    value: ExactValue,
    source: str,
    target: str,
    nodes: frozenset[str],
    edges: Mapping[tuple[str, str], Transform],
) -> ConversionResult:
    steps = _find_path(source, target, nodes, edges)
    current = exact_fraction(value)
    for step in steps:
        current = step.transform.apply(current)
    if steps:
        path = (steps[0].source,) + tuple(step.target for step in steps)
    else:
        path = (source,)
    return ConversionResult(current, path, steps)


def _validate_graph(
    version: int,
    nodes: frozenset[str],
    edges: Mapping[tuple[str, str], Transform],
) -> GraphValidation:
    samples = (Fraction(0), Fraction(1), Fraction(-2), Fraction(7, 11))
    edge_checks = []
    for (source, target), transform in edges.items():
        inverse = transform.inverse()
        residuals = tuple(
            inverse.apply(transform.apply(sample)) - sample for sample in samples
        )
        edge_checks.append(EdgeCheck(source, target, samples, residuals))

    adjacency: dict[str, list[tuple[str, Transform]]] = {node: [] for node in nodes}
    for (source, target), transform in edges.items():
        adjacency[source].append((target, transform))
        adjacency[target].append((source, transform.inverse()))

    potentials: dict[str, Transform] = {}
    paths: dict[str, tuple[ConversionStep, ...]] = {}
    for root in sorted(nodes):
        if root in potentials:
            continue
        potentials[root] = _identity_transform()
        paths[root] = ()
        queue: deque[str] = deque([root])
        while queue:
            current = queue.popleft()
            for neighbor, transform in adjacency[current]:
                if neighbor in potentials:
                    continue
                potentials[neighbor] = potentials[current].then(transform)
                paths[neighbor] = paths[current] + (
                    ConversionStep(current, neighbor, transform),
                )
                queue.append(neighbor)

    contradictions: list[CycleContradiction] = []
    for (source, target), transform in sorted(edges.items()):
        if source not in potentials or target not in potentials:
            continue
        expected = potentials[source].then(transform)
        if (
            expected.scale == potentials[target].scale
            and expected.offset == potentials[target].offset
        ):
            continue
        cycle_steps = paths[source] + (ConversionStep(source, target, transform),)
        cycle_steps += tuple(step.reversed() for step in reversed(paths[target]))
        composed = _identity_transform()
        for step in cycle_steps:
            composed = composed.then(step.transform)
        cycle_path = (cycle_steps[0].source,) + tuple(step.target for step in cycle_steps)
        sample_residuals = tuple(
            composed.apply(sample) - sample for sample in samples
        )
        contradictions.append(
            CycleContradiction(cycle_path, cycle_steps, composed, sample_residuals)
        )

    return GraphValidation(version, tuple(edge_checks), tuple(contradictions))


class ConversionGraphSnapshot:
    """Immutable view of one graph revision."""

    def __init__(
        self,
        version: int,
        nodes: frozenset[str],
        edges: Mapping[tuple[str, str], Transform],
    ) -> None:
        self.version = version
        self.nodes = nodes
        self.edges = MappingProxyType(dict(edges))

    def convert(self, value: ExactValue, source: str, target: str) -> ConversionResult:
        return _convert_value(value, source, target, self.nodes, self.edges)

    def validate(self) -> GraphValidation:
        return _validate_graph(self.version, self.nodes, self.edges)


class ConversionGraph:
    """Mutable graph that preserves every committed revision."""

    def __init__(self) -> None:
        self._version = 0
        self._nodes: set[str] = set()
        self._edges: dict[tuple[str, str], Transform] = {}
        self._snapshots: dict[int, ConversionGraphSnapshot] = {
            0: ConversionGraphSnapshot(0, frozenset(), {})
        }
        self._history: list[GraphRevision] = [GraphRevision(0, "init", 0, 0)]

    @property
    def version(self) -> int:
        return self._version

    @property
    def history(self) -> tuple[GraphRevision, ...]:
        return tuple(self._history)

    def _commit(self, action: str) -> int:
        self._version += 1
        snapshot = ConversionGraphSnapshot(
            self._version, frozenset(self._nodes), dict(self._edges)
        )
        self._snapshots[self._version] = snapshot
        self._history.append(
            GraphRevision(self._version, action, len(self._nodes), len(self._edges))
        )
        return self._version

    def add_node(self, name: str) -> int:
        if name in self._nodes:
            raise ValueError(f"unit already defined: {name}")
        self._nodes.add(name)
        return self._commit(f"add unit {name}")

    def remove_node(self, name: str) -> int:
        if name not in self._nodes:
            raise KeyError(f"unknown unit: {name}")
        self._nodes.remove(name)
        self._edges = {
            edge: transform
            for edge, transform in self._edges.items()
            if name not in edge
        }
        return self._commit(f"remove unit {name}")

    def add_edge(
        self,
        source: str,
        target: str,
        scale: ExactValue,
        offset: ExactValue = 0,
        *,
        domain: str = "",
    ) -> int:
        if source not in self._nodes or target not in self._nodes:
            missing = source if source not in self._nodes else target
            raise KeyError(f"unknown unit: {missing}")
        self._edges[(source, target)] = Transform(
            exact_fraction(scale, name="scale"),
            exact_fraction(offset, name="offset"),
            domain,
        )
        return self._commit(f"set conversion {source}->{target}")

    def remove_edge(self, source: str, target: str) -> int:
        try:
            del self._edges[(source, target)]
        except KeyError as exc:
            raise KeyError(f"unknown conversion: {source}->{target}") from exc
        return self._commit(f"remove conversion {source}->{target}")

    def snapshot(self, version: int | None = None) -> ConversionGraphSnapshot:
        requested = self._version if version is None else version
        try:
            return self._snapshots[requested]
        except KeyError as exc:
            raise KeyError(f"unknown graph version: {requested}") from exc

    def convert(self, value: ExactValue, source: str, target: str) -> ConversionResult:
        return self.snapshot().convert(value, source, target)

    def validate(self, version: int | None = None) -> GraphValidation:
        return self.snapshot(version).validate()
