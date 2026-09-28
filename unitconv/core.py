"""Exact-rational, transactional unit conversion graph.

The graph is stored as a *weighted* union-find structure: for every unit
``u`` we keep its DSU parent and an exact :class:`~fractions.Fraction`
``ratio[u]`` equal to ``u / parent[u]``.  Path compression rewrites the
ratios using exact rational arithmetic, so multi-hop conversions never
accumulate any floating-point error and no epsilon/threshold is ever used.

Dimensions ("length", "mass", ...) are optional explicit labels.  Units
joined by a conversion rule belong to one connected component and therefore
must share one dimension.  When no label is given, a component simply has
no dimension; queries between a labelled and an unlabelled (or differently
labelled) component are refused as dimension mismatches.

Every committed :meth:`VersionedGraph.batch` call creates a new immutable
:class:`GraphState` version while all previous versions stay queryable.
A batch is fully transactional: if *any* statement is invalid, closes a
contradictory cycle, or mixes dimensions, the whole batch is rejected and
the previous state is left byte-for-byte unchanged.
"""

from __future__ import annotations

import re
from dataclasses import dataclass, field
from fractions import Fraction
from typing import Dict, List, Optional, Tuple

__all__ = [
    "ConversionResult",
    "ContradictionError",
    "DimensionConflictError",
    "DimensionMismatchError",
    "GraphState",
    "NoConversionPathError",
    "StatementError",
    "UnknownUnitError",
    "VersionedGraph",
    "format_factor",
    "parse_factor",
    "parse_statement",
]


# ---------------------------------------------------------------------------
# Errors
# ---------------------------------------------------------------------------


class UnitConvError(Exception):
    """Base class for all errors raised by this package."""


class StatementError(UnitConvError):
    """A statement string cannot be parsed or carries an invalid value."""


class UnknownUnitError(UnitConvError):
    """A query references a unit that was never defined."""


class DimensionConflictError(UnitConvError):
    """A statement assigns incompatible dimension labels to one component."""


class DimensionMismatchError(UnitConvError):
    """A query/rule touches units from different dimensions."""

    def __init__(self, source: str, target: str,
                 dim_a: Optional[str], dim_b: Optional[str]):
        self.source = source
        self.target = target
        self.dim_a = dim_a
        self.dim_b = dim_b
        super().__init__(
            f"dimension mismatch: {source!r}"
            + (f" has dimension {dim_a!r}" if dim_a is not None
               else " has unlabelled dimension")
            + f" while {target!r}"
            + (f" has dimension {dim_b!r}" if dim_b is not None
               else " has unlabelled dimension")
        )


class ContradictionError(UnitConvError):
    """A rule would close a cycle whose existing factor disagrees.

    Attributes
    ----------
    path:
        Sequence of ``(unit, edge_factor)`` pairs.  ``edge_factor`` is the
        exact factor from that unit to the next unit (the last pair has
        ``edge_factor is None``).
    expected:
        The factor ``source/target`` already entailed by the graph.
    claimed:
        The factor that the rejected rule tried to assert.
    """

    def __init__(self, source: str, target: str,
                 path: List[Tuple[str, Optional[Fraction]]],
                 expected: Fraction, claimed: Fraction):
        self.source = source
        self.target = target
        self.path = path
        self.expected = expected
        self.claimed = claimed
        super().__init__(_render_contradiction(
            source, target, path, expected, claimed))


class NoConversionPathError(UnitConvError):
    """The two units live in disconnected components."""

    def __init__(self, source: str, target: str):
        self.source = source
        self.target = target
        super().__init__(
            f"no conversion path between {source!r} and {target!r}: "
            "they live in disconnected components"
        )


# ---------------------------------------------------------------------------
# Statement / factor parsing (exact)
# ---------------------------------------------------------------------------

# A unit identifier: any non-empty run without whitespace or '=' ':' '/'.
_UNIT_RE = re.compile(r"^[^\s=:/]+$")
_DIM_HEAD_RE = re.compile(r"^dim\s*:\s*(.*)$", re.IGNORECASE)


def parse_factor(text: str) -> Fraction:
    """Parse a positive exact decimal / integer / fraction into a Fraction.

    Accepted examples: ``"2"``, ``"0.5"``, ``"1/6"``, ``"3/2"``.  ``"0.1"``
    is stored *exactly* as 1/10 -- it is never routed through a binary
    float, so there is no rounding to hide.  Spaces inside the token are
    not allowed.
    """
    if not isinstance(text, str):
        raise StatementError(f"invalid conversion factor: {text!r}")
    token = text.strip()
    if not token:
        raise StatementError("empty conversion factor")
    parts = token.split("/")
    if len(parts) > 2 or any(not p for p in parts):
        raise StatementError(f"invalid conversion factor: {text!r}")
    try:
        if len(parts) == 1:
            value = Fraction(token)
        else:
            value = Fraction(parts[0]) / Fraction(parts[1])
    except (ValueError, ZeroDivisionError):
        raise StatementError(f"invalid conversion factor: {text!r}")
    if value <= 0:
        raise StatementError(
            f"conversion factor must be positive, got {text!r}")
    return value


def _valid_unit(name: str) -> bool:
    return bool(name) and bool(_UNIT_RE.match(name))


def parse_statement(statement: str) -> Tuple[str, object, ...]:
    """Parse one statement into a normalised tuple.

    Supported syntax (whitespace-insensitive, case-sensitive unit names)::

        dim: LENGTH m      -> ("dim", "LENGTH", "m")
        A = 2 B            -> ("rule", "A", "B", Fraction(2))
        A = B / 6          -> ("rule", "A", "B", Fraction(1, 6))
        A = 1/6 B          -> same as above
        A = B              -> ("rule", "A", "B", Fraction(1))

    The rule tuple always means ``unit_a = factor * unit_b``.
    """
    if not isinstance(statement, str):
        raise StatementError(
            f"statement must be a string, got {statement!r}")
    text = statement.strip()
    if not text:
        raise StatementError("empty statement")

    dim_head = _DIM_HEAD_RE.match(text)
    if dim_head:
        rest = dim_head.group(1).split()
        if len(rest) != 2:
            raise StatementError(
                "dimension declaration must look like 'dim: LENGTH m': "
                f"{statement!r}")
        dim, unit = rest
        if not _valid_unit(dim) or not _valid_unit(unit):
            raise StatementError(f"invalid dimension declaration: {statement!r}")
        return ("dim", dim, unit)

    if "=" not in text:
        raise StatementError(
            "statement must look like 'A = 2 B' or 'dim: LENGTH m': "
            f"{statement!r}")

    left, _, right = text.partition("=")
    unit_a = left.strip()
    tokens = right.split()
    if not _valid_unit(unit_a) or not tokens:
        raise StatementError(f"invalid rule: {statement!r}")

    factor: Fraction
    unit_b: str
    if len(tokens) == 1:
        token = tokens[0]
        if "/" in token:
            # Tight "A = B/6" spelling (no spaces around the slash).
            head, _, divisor = token.partition("/")
            if not head:
                raise StatementError(f"invalid rule: {statement!r}")
            unit_b = head
            factor = Fraction(1) / parse_factor(divisor)
        else:
            # A = B
            unit_b = token
            factor = Fraction(1)
    elif len(tokens) == 2:
        # A = 2 B   or   A = /6 B
        raw_factor, unit_b = tokens
        if raw_factor.startswith("/"):
            factor = Fraction(1) / parse_factor(raw_factor[1:])
        else:
            factor = parse_factor(raw_factor)
    elif len(tokens) == 3 and tokens[1] == "/":
        # A = B / 6
        unit_b, _, raw_divisor = tokens
        factor = Fraction(1) / parse_factor(raw_divisor)
    else:
        raise StatementError(f"invalid rule: {statement!r}")

    if not _valid_unit(unit_b):
        raise StatementError(f"invalid unit name in: {statement!r}")
    return ("rule", unit_a, unit_b, factor)


def format_factor(value: Fraction) -> str:
    """Render an exact Fraction as ``p/q`` (or just ``p`` when q == 1)."""
    if value.denominator == 1:
        return str(value.numerator)
    return f"{value.numerator}/{value.denominator}"


def _render_contradiction(source: str, target: str,
                          path: List[Tuple[str, Optional[Fraction]]],
                          expected: Fraction, claimed: Fraction) -> str:
    chain_parts: List[str] = []
    for unit, edge in path:
        chain_parts.append(unit)
        if edge is not None:
            chain_parts.append(f" ={format_factor(edge)}> ")
    chain = "".join(chain_parts)
    return (
        "contradictory rule rejected; existing graph is unchanged\n"
        f"  existing derivation : {source} = {format_factor(expected)} "
        f"{target}\n"
        f"  rejected rule       : {source} = {format_factor(claimed)} "
        f"{target}\n"
        f"  contradiction path  : {chain}\n"
        f"  closed cycle        : existing path {source} -> {target} "
        f"conflicts with new edge {source} -> {target}"
    )


# ---------------------------------------------------------------------------
# Graph state: weighted union-find plus an adjacency list for path display
# ---------------------------------------------------------------------------


@dataclass
class ConversionResult:
    """Result of an exact conversion query."""

    source: str
    target: str
    factor: Fraction
    """Exact value of ``source / target``."""
    amount: Fraction
    result: Fraction
    """Exact value of ``amount`` expressed in ``target`` units."""
    path: List[Tuple[str, Optional[Fraction]]]

    def render_path(self) -> str:
        parts: List[str] = []
        for unit, edge in self.path:
            parts.append(unit)
            if edge is not None:
                parts.append(f" ={format_factor(edge)}> ")
        return "".join(parts)


@dataclass
class GraphState:
    """One committed, effectively immutable version of the conversion graph.

    Invariants:

    * ``ratio[u]`` equals ``u / parent[u]`` exactly;
    * each connected component has at most one effective dimension label;
    * ``adj`` holds one directed edge pair per accepted, non-redundant rule.
    """

    parent: Dict[str, str] = field(default_factory=dict)
    ratio: Dict[str, Fraction] = field(default_factory=dict)
    declared_dim: Dict[str, str] = field(default_factory=dict)
    root_dim: Dict[str, str] = field(default_factory=dict)
    adj: Dict[str, List[Tuple[str, Fraction]]] = field(default_factory=dict)
    statements: List[Tuple[str, object, ...]] = field(default_factory=list)

    # -- basic introspection ------------------------------------------------

    def has_unit(self, unit: str) -> bool:
        return unit in self.parent

    def units(self) -> List[str]:
        return sorted(self.parent)

    def dimension_of(self, unit: str) -> Optional[str]:
        self._require_unit(unit)
        root, _ = self._find(unit)
        return self.root_dim.get(root)

    def rule_statements(self) -> List[Tuple[str, object, ...]]:
        return list(self.statements)

    def clone(self) -> "GraphState":
        return GraphState(
            parent=dict(self.parent),
            ratio=dict(self.ratio),
            declared_dim=dict(self.declared_dim),
            root_dim=dict(self.root_dim),
            adj={u: list(edges) for u, edges in self.adj.items()},
            statements=list(self.statements),
        )

    # -- weighted union-find ------------------------------------------------

    def _ensure(self, unit: str) -> None:
        if unit not in self.parent:
            self.parent[unit] = unit
            self.ratio[unit] = Fraction(1)
            self.adj[unit] = []

    def _find(self, unit: str) -> Tuple[str, Fraction]:
        """Return ``(root, unit/root)`` and compress with exact ratios."""
        parent = self.parent[unit]
        if parent == unit:
            return unit, Fraction(1)
        root, parent_over_root = self._find(parent)
        unit_over_root = self.ratio[unit] * parent_over_root
        self.parent[unit] = root
        self.ratio[unit] = unit_over_root
        return root, unit_over_root

    # -- dimension handling -------------------------------------------------

    def declare_dimension(self, unit: str, dimension: str) -> None:
        """Label ``unit``'s component with ``dimension``.

        Raises :class:`DimensionConflictError` if the unit/component already
        carries a different label.
        """
        self._ensure(unit)
        previous = self.declared_dim.get(unit)
        if previous is not None and previous != dimension:
            raise DimensionConflictError(
                f"unit {unit!r} already declared as dimension {previous!r}, "
                f"cannot relabel as {dimension!r}")
        root, _ = self._find(unit)
        component_dim = self.root_dim.get(root)
        if component_dim is not None and component_dim != dimension:
            raise DimensionConflictError(
                f"dimension conflict: component containing {unit!r} is "
                f"{component_dim!r}, cannot also be {dimension!r}")
        self.declared_dim[unit] = dimension
        self.root_dim[root] = dimension

    # -- rule insertion ------------------------------------------------------

    def add_rule(self, unit_a: str, unit_b: str,
                 factor: Fraction) -> None:
        """Assert ``unit_a = factor * unit_b`` exactly.

        Raises :class:`DimensionMismatchError` if the two components have
        different dimension labels, or :class:`ContradictionError` if the
        units are already connected through a *different* exact factor.
        """
        self._ensure(unit_a)
        self._ensure(unit_b)
        if factor <= 0:
            raise StatementError(
                f"conversion factor must be positive, got {factor!r}")

        root_a, a_over_ra = self._find(unit_a)
        root_b, b_over_rb = self._find(unit_b)
        dim_a = self.root_dim.get(root_a)
        dim_b = self.root_dim.get(root_b)
        if dim_a is not None and dim_b is not None and dim_a != dim_b:
            raise DimensionMismatchError(unit_a, unit_b, dim_a, dim_b)

        if root_a == root_b:
            existing = a_over_ra / b_over_rb  # exact unit_a / unit_b
            if existing != factor:
                path = self.path_between(unit_a, unit_b)
                raise ContradictionError(unit_a, unit_b, path,
                                         existing, factor)
            # Consistent redundant rule: graph already entails it; nothing
            # to mutate, so no duplicate edge is stored.
            return

        # unit_a = f * unit_b  =>  rb / ra = (a/ra) / (f * b/rb)
        rb_over_ra = a_over_ra / (factor * b_over_rb)
        self.parent[root_b] = root_a
        self.ratio[root_b] = rb_over_ra

        merged_dim = dim_a if dim_a is not None else dim_b
        if merged_dim is not None:
            self.root_dim[root_a] = merged_dim
        self.root_dim.pop(root_b, None)

        self.adj[unit_a].append((unit_b, factor))
        self.adj[unit_b].append((unit_a, Fraction(1) / factor))

    # -- queries -------------------------------------------------------------

    def path_between(
            self, source: str, target: str
    ) -> List[Tuple[str, Optional[Fraction]]]:
        """BFS-derived derivation path ``source -> ... -> target``.

        Raises :class:`NoConversionPathError` for disconnected units.
        """
        if source == target:
            return [(source, None)]
        previous: Dict[str, Tuple[str, Fraction]] = {}
        visited = {source}
        queue = [source]
        head = 0
        while head < len(queue):
            current = queue[head]
            head += 1
            for neighbour, edge in self.adj[current]:
                if neighbour in visited:
                    continue
                visited.add(neighbour)
                previous[neighbour] = (current, edge)
                if neighbour == target:
                    queue = []
                    break
                queue.append(neighbour)
        if target not in visited:
            raise NoConversionPathError(source, target)

        nodes = [target]
        edges: List[Fraction] = []
        node = target
        while node != source:
            prev, edge = previous[node]
            edges.append(edge)
            nodes.append(prev)
            node = prev
        nodes.reverse()
        edges.reverse()
        return [
            (nodes[i], edges[i] if i < len(edges) else None)
            for i in range(len(nodes))
        ]

    def convert(self, source: str, target: str,
                amount: Fraction = Fraction(1)) -> ConversionResult:
        self._require_unit(source)
        self._require_unit(target)
        if amount < 0:
            raise StatementError("amount must be non-negative")

        root_a, a_over_root = self._find(source)
        root_b, b_over_root = self._find(target)
        dim_a = self.root_dim.get(root_a)
        dim_b = self.root_dim.get(root_b)

        if root_a != root_b:
            if dim_a is not None and dim_b is not None and dim_a != dim_b:
                raise DimensionMismatchError(source, target, dim_a, dim_b)
            raise NoConversionPathError(source, target)

        factor = a_over_root / b_over_root
        path = self.path_between(source, target)
        return ConversionResult(
            source=source,
            target=target,
            factor=factor,
            amount=amount,
            result=amount * factor,
            path=path,
        )

    def _require_unit(self, unit: str) -> None:
        if unit not in self.parent:
            raise UnknownUnitError(f"unknown unit: {unit!r}")


# ---------------------------------------------------------------------------
# Versioned graph: append-only transactional batches
# ---------------------------------------------------------------------------


@dataclass
class CommitInfo:
    """Metadata describing one committed batch/version."""

    version: int
    timestamp: Optional[str]
    statement_count: int


class VersionedGraph:
    """Append-only, transactional multi-version conversion graph.

    Version 0 is always the empty graph.  Each successful
    :meth:`batch` call appends one new :class:`GraphState`; rejected
    batches mutate nothing and create no version.
    """

    def __init__(self) -> None:
        self._states: List[GraphState] = [GraphState()]
        self._commits: List[Optional[CommitInfo]] = [None]

    # -- version access ------------------------------------------------------

    @property
    def version(self) -> int:
        return len(self._states) - 1

    def state(self, version: Optional[int] = None) -> GraphState:
        if version is None:
            version = self.version
        if not isinstance(version, int) or version < 0 or version > self.version:
            raise ValueError(
                f"unknown version {version!r}; current versions are "
                f"0..{self.version}")
        return self._states[version]

    def commits(self) -> List[CommitInfo]:
        return [info for info in self._commits if info is not None]

    # -- transactional mutation ----------------------------------------------

    def batch(self, statements: Sequence[str],
              timestamp: Optional[str] = None) -> CommitInfo:
        """Apply ``statements`` atomically; return the new version metadata.

        Dimension declarations are applied first, then rules.  If anything
        fails the tentative copy is discarded and the live graph is
        guaranteed unchanged (no partial rules, no version bump).
        """
        if isinstance(statements, str):
            raise StatementError(
                "batch expects a sequence of statements, not a single string")

        parsed: List[Tuple[str, object, ...]] = []
        for raw in statements:
            parsed.append(parse_statement(raw))

        candidate = self.state().clone()

        # Phase 1: dimensions, so a rule in the same batch cannot violate a
        # dimension declared in that batch regardless of statement order.
        for stmt in parsed:
            if stmt[0] == "dim":
                _, dimension, unit = stmt
                candidate.declare_dimension(unit, dimension)  # type: ignore[arg-type]

        # Phase 2: conversion rules.
        for stmt in parsed:
            if stmt[0] == "rule":
                _, unit_a, unit_b, factor = stmt
                candidate.add_rule(unit_a, unit_b, factor)  # type: ignore[arg-type]

        candidate.statements.extend(parsed)
        new_version = self.version + 1
        info = CommitInfo(
            version=new_version,
            timestamp=timestamp,
            statement_count=len(parsed),
        )
        self._states.append(candidate)
        self._commits.append(info)
        return info

    # -- convenience ---------------------------------------------------------

    def convert(self, source: str, target: str,
                amount: Fraction = Fraction(1),
                version: Optional[int] = None) -> ConversionResult:
        return self.state(version).convert(source, target, amount)
