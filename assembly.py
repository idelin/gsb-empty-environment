"""Incremental finite-element-style global assembly with provenance tracking.

Pure standard library; sizes and indices are always given explicitly.

Features:
- Assemble local element matrices/vectors into a global system through an
  explicit global-dof map; shared dofs accumulate, repeated contributions add.
- Provenance: every global entry records which elements contributed to it,
  and under which constraint version the constrained system was produced.
- Element contributions can be retracted or corrected without traversing
  other elements.
- Constrained dofs are eliminated linearly (Dirichlet u_d = c): the
  correction ``b_j -= A_jd * c`` is applied to the right-hand side, then row
  and column ``d`` are replaced by the identity row/column.
- When constraints change, the old constrained assembly is undone and the
  new one is re-applied incrementally (only constrained rows/columns and
  recorded right-hand-side corrections are touched).
"""

from __future__ import annotations

import math
from dataclasses import dataclass
from typing import Optional


class AssemblyError(Exception):
    """Raised when a contribution, mapping, or constraint is invalid."""


@dataclass(frozen=True)
class Contribution:
    """One element's local contribution.

    ``dof_map[p]`` is the global dof index of local dof ``p``. Repeated
    global indices inside one map are allowed; their contributions add up.
    ``rhs=None`` means a zero local load vector.
    """

    element_id: str
    dof_map: tuple[int, ...]
    matrix: tuple[tuple[float, ...], ...]
    rhs: Optional[tuple[float, ...]] = None


def contribution(element_id, dof_map, matrix, rhs=None) -> Contribution:
    """Convenience builder accepting plain lists."""
    return Contribution(
        element_id=str(element_id),
        dof_map=tuple(int(i) for i in dof_map),
        matrix=tuple(tuple(float(v) for v in row) for row in matrix),
        rhs=None if rhs is None else tuple(float(v) for v in rhs),
    )


def _validate(size: int, contrib: Contribution) -> None:
    eid = contrib.element_id
    if not contrib.dof_map:
        raise AssemblyError(f"element {eid!r}: empty dof map")
    dim = len(contrib.dof_map)
    for index in contrib.dof_map:
        if not isinstance(index, int) or isinstance(index, bool):
            raise AssemblyError(f"element {eid!r}: non-integer dof {index!r}")
        if index < 0 or index >= size:
            raise AssemblyError(
                f"element {eid!r}: dof {index} out of range [0, {size})"
            )
    if len(contrib.matrix) != dim or any(len(row) != dim for row in contrib.matrix):
        raise AssemblyError(
            f"element {eid!r}: matrix must be {dim}x{dim} to match the dof map"
        )
    if contrib.rhs is not None and len(contrib.rhs) != dim:
        raise AssemblyError(
            f"element {eid!r}: rhs length must be {dim} to match the dof map"
        )


class Assembler:
    """Incremental global assembler with provenance and constraint versions."""

    def __init__(self, size: int):
        if not isinstance(size, int) or isinstance(size, bool) or size <= 0:
            raise AssemblyError(f"size must be a positive integer, got {size!r}")
        self.size = size
        self._elements: dict[str, Contribution] = {}
        # Provenance: global entry -> {element_id: accumulated value}.
        self._src_matrix: dict[tuple[int, int], dict[str, float]] = {}
        self._src_rhs: dict[int, dict[str, float]] = {}
        # Unconstrained ("base") system, maintained incrementally.
        self.base_matrix = [[0.0] * size for _ in range(size)]
        self.base_rhs = [0.0] * size
        # Constrained working system, derived from the base system.
        self.matrix = [row[:] for row in self.base_matrix]
        self.rhs = list(self.base_rhs)
        # Constraint state: dof -> prescribed value, plus a version counter.
        self.constraints: dict[int, float] = {}
        self.constraint_version = 0
        self._applied_version = 0
        # dof -> {row: rhs correction} recorded while applying constraints.
        self._applied_corrections: dict[int, dict[int, float]] = {}
        # Base entries modified since the last constraint refresh.
        self._dirty_matrix: set[tuple[int, int]] = set()
        self._dirty_rhs: set[int] = set()

    # ------------------------------------------------------------------
    # element management
    # ------------------------------------------------------------------
    def add_elements(self, contributions) -> None:
        """Add a batch of contributions atomically.

        Every contribution is validated first; if any mapping is invalid the
        whole batch is rejected and the assembler state is left untouched.
        """
        contribs = [
            c if isinstance(c, Contribution) else contribution(*c) for c in contributions
        ]
        seen = set()
        for c in contribs:
            _validate(self.size, c)
            if c.element_id in self._elements or c.element_id in seen:
                raise AssemblyError(f"duplicate element id {c.element_id!r}")
            seen.add(c.element_id)
        for c in contribs:
            self._scatter(c)
        self._refresh_constraints()

    def remove_element(self, element_id: str) -> None:
        """Retract one element's contribution."""
        element_id = str(element_id)
        if element_id not in self._elements:
            raise AssemblyError(f"unknown element {element_id!r}")
        self._gather(self._elements.pop(element_id))
        self._refresh_constraints()

    def update_element(self, element_id, dof_map, matrix, rhs=None) -> None:
        """Correct an existing element in place (retract + re-add).

        Only the global entries touched by this element's old and new maps
        are recomputed; other elements are never traversed.
        """
        new = contribution(element_id, dof_map, matrix, rhs)
        _validate(self.size, new)
        if new.element_id not in self._elements:
            raise AssemblyError(f"unknown element {new.element_id!r}")
        self._gather(self._elements.pop(new.element_id))
        self._scatter(new)
        self._refresh_constraints()

    def element_ids(self):
        return sorted(self._elements)

    # ------------------------------------------------------------------
    # constraints
    # ------------------------------------------------------------------
    def set_constraints(self, constraints: dict[int, float]) -> None:
        """Replace the full constraint set and bump the constraint version."""
        checked = {}
        for dof, value in constraints.items():
            if not isinstance(dof, int) or isinstance(dof, bool) or not 0 <= dof < self.size:
                raise AssemblyError(f"constraint dof {dof!r} out of range [0, {self.size})")
            checked[dof] = float(value)
        self.constraints = checked
        self.constraint_version += 1
        self._refresh_constraints()

    # ------------------------------------------------------------------
    # provenance queries
    # ------------------------------------------------------------------
    def entry_provenance(self, i: int, j: int) -> dict:
        """Explain how global matrix entry (i, j) was produced."""
        self._check_index(i)
        self._check_index(j)
        return {
            "value": self.matrix[i][j],
            "unconstrained_value": self.base_matrix[i][j],
            "elements": dict(self._src_matrix.get((i, j), {})),
            "constraints": [
                {"dof": d, "value": self.constraints[d]}
                for d in (i, j)
                if d in self.constraints
            ],
            "constraint_version": self._applied_version,
        }

    def rhs_provenance(self, i: int) -> dict:
        """Explain how global rhs entry i was produced."""
        self._check_index(i)
        corrections = {
            dof: corr[i]
            for dof, corr in self._applied_corrections.items()
            if i in corr
        }
        return {
            "value": self.rhs[i],
            "unconstrained_value": self.base_rhs[i],
            "elements": dict(self._src_rhs.get(i, {})),
            "constraint_corrections": corrections,
            "prescribed": self.constraints.get(i),
            "constraint_version": self._applied_version,
        }

    # ------------------------------------------------------------------
    # internal: scatter / gather of one element (never touches others)
    # ------------------------------------------------------------------
    def _scatter(self, c: Contribution) -> None:
        self._elements[c.element_id] = c
        rhs = c.rhs if c.rhs is not None else (0.0,) * len(c.dof_map)
        for p, i in enumerate(c.dof_map):
            for q, j in enumerate(c.dof_map):
                value = c.matrix[p][q]
                if value == 0.0:
                    continue
                sources = self._src_matrix.setdefault((i, j), {})
                sources[c.element_id] = sources.get(c.element_id, 0.0) + value
                self.base_matrix[i][j] = math.fsum(sources.values())
                self._dirty_matrix.add((i, j))
            if rhs[p] != 0.0:
                sources = self._src_rhs.setdefault(i, {})
                sources[c.element_id] = sources.get(c.element_id, 0.0) + rhs[p]
                self.base_rhs[i] = math.fsum(sources.values())
                self._dirty_rhs.add(i)

    def _gather(self, c: Contribution) -> None:
        rhs = c.rhs if c.rhs is not None else (0.0,) * len(c.dof_map)
        for p, i in enumerate(c.dof_map):
            for q, j in enumerate(c.dof_map):
                if c.matrix[p][q] == 0.0:
                    continue
                sources = self._src_matrix.get((i, j))
                if sources is None or c.element_id not in sources:
                    continue
                del sources[c.element_id]
                self.base_matrix[i][j] = math.fsum(sources.values()) if sources else 0.0
                self._dirty_matrix.add((i, j))
                if not sources:
                    del self._src_matrix[(i, j)]
            if rhs[p] != 0.0:
                sources = self._src_rhs.get(i)
                if sources is None or c.element_id not in sources:
                    continue
                del sources[c.element_id]
                self.base_rhs[i] = math.fsum(sources.values()) if sources else 0.0
                self._dirty_rhs.add(i)
                if not sources:
                    del self._src_rhs[i]

    # ------------------------------------------------------------------
    # internal: constraint elimination with undo bookkeeping
    # ------------------------------------------------------------------
    def _refresh_constraints(self) -> None:
        self._unapply_constraints()
        for i, j in self._dirty_matrix:
            self.matrix[i][j] = self.base_matrix[i][j]
        self._dirty_matrix.clear()
        for i in self._dirty_rhs:
            self.rhs[i] = self.base_rhs[i]
        self._dirty_rhs.clear()
        for dof in sorted(self.constraints):
            self._apply_constraint(dof, self.constraints[dof])
        self._applied_version = self.constraint_version

    def _apply_constraint(self, dof: int, value: float) -> None:
        corrections = {}
        for j in range(self.size):
            if j == dof:
                continue
            coupling = self.matrix[j][dof]
            if coupling != 0.0:
                corrections[j] = coupling * value
                self.rhs[j] -= coupling * value
        for j in range(self.size):
            self.matrix[dof][j] = 0.0
            self.matrix[j][dof] = 0.0
        self.matrix[dof][dof] = 1.0
        self.rhs[dof] = value
        self._applied_corrections[dof] = corrections

    def _unapply_constraints(self) -> None:
        if not self._applied_corrections:
            return
        touched_rows = set()
        for dof, corrections in self._applied_corrections.items():
            for j in range(self.size):
                self.matrix[dof][j] = self.base_matrix[dof][j]
                self.matrix[j][dof] = self.base_matrix[j][dof]
            touched_rows.add(dof)
            touched_rows.update(corrections)
        for j in touched_rows:
            self.rhs[j] = self.base_rhs[j]
        self._applied_corrections.clear()

    def _check_index(self, i: int) -> None:
        if not isinstance(i, int) or isinstance(i, bool) or not 0 <= i < self.size:
            raise AssemblyError(f"index {i!r} out of range [0, {self.size})")


def assemble_reference(size: int, contributions, constraints=None):
    """Independent from-scratch full assembly, used to cross-check results.

    Returns ``(matrix, rhs)``. Applies the textbook Dirichlet elimination:
    ``b_j -= A_jd * c`` for every unconstrained ``j``, then replaces row and
    column ``d`` with the identity and sets ``b_d = c``.
    """
    matrix = [[0.0] * size for _ in range(size)]
    rhs = [0.0] * size
    for raw in contributions:
        c = raw if isinstance(raw, Contribution) else contribution(*raw)
        _validate(size, c)
        local_rhs = c.rhs if c.rhs is not None else (0.0,) * len(c.dof_map)
        for p, i in enumerate(c.dof_map):
            for q, j in enumerate(c.dof_map):
                matrix[i][j] += c.matrix[p][q]
            rhs[i] += local_rhs[p]
    for dof in sorted(constraints or {}):
        value = float(constraints[dof])
        for j in range(size):
            if j != dof:
                rhs[j] -= matrix[j][dof] * value
        for j in range(size):
            matrix[dof][j] = 0.0
            matrix[j][dof] = 0.0
        matrix[dof][dof] = 1.0
        rhs[dof] = value
    return matrix, rhs
