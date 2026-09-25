"""Pure-Python global assembler with provenance and constraint elimination.

Assembles local element matrices/vectors into a global system through an
explicit global-DOF map.  Features:

* shared dofs: repeated contributions to one global entry accumulate;
* provenance: every global entry records each contributing element and,
  for the constrained (reduced) system, the constraint version;
* incremental updates: element corrections/withdrawals touch only the
  entries covered by that element's dof map, never all elements;
* linear constraint elimination: u[c] = sum(t_k * u[k]) + g is eliminated
  via u = T v + g, giving A_red = T^T A T and b_red = T^T (b - A g), so
  the right-hand-side correction from non-homogeneous constraints is kept;
* constraint changes bump ``constraint_version`` and lazily rebuild the
  reduced system from the untouched element-level assembly (the old
  constrained assembly is discarded, elements are not re-visited);
* batch application is atomic: one invalid mapping rejects the whole batch.

Standard library only.  Sizes and indices are always explicit inputs.
"""

from __future__ import annotations

from dataclasses import dataclass, field


class AssemblyError(Exception):
    """Raised for invalid element definitions, mappings, or constraints."""


def _is_number(value):
    return isinstance(value, (int, float)) and not isinstance(value, bool)


def _check_dof(dof, n):
    if isinstance(dof, bool) or not isinstance(dof, int):
        raise AssemblyError(f"dof {dof!r} is not an integer")
    if not 0 <= dof < n:
        raise AssemblyError(f"dof {dof} out of range [0, {n})")


@dataclass(frozen=True)
class Contribution:
    """One recorded contribution to a global or reduced entry.

    kind:        'element' or 'constraint'
    source:      element id, or the constrained dof for constraint terms
    local_index: (li, lj) or (li,) inside the element; (row, dof) for
                 constraint correction terms
    weight:      multiplier introduced by constraint elimination (1.0 for
                 the element-level assembly)
    value:       actual signed amount this record adds to the entry
    version:     constraint version the record was created under
                 (0 = element-level base assembly, constraint independent)
    """

    kind: str
    source: object
    local_index: tuple
    weight: float
    value: float
    version: int


@dataclass(frozen=True)
class Constraint:
    """Linear constraint  u[dof] = sum(coef * u[k]) + constant.

    terms is a tuple of (dof, coefficient) pairs; referenced dofs must be
    free (chained constraints are rejected).
    """

    dof: int
    terms: tuple = ()
    constant: float = 0.0


@dataclass
class _Element:
    elem_id: object
    dof_map: tuple
    ke: tuple
    fe: tuple


@dataclass
class ReducedSystem:
    """Constraint-eliminated system over the free dofs."""

    matrix: list
    rhs: list
    free_dofs: list
    transform: list          # T: n_dofs x n_free, u = T v + g
    offset: list             # g: n_dofs
    matrix_provenance: list
    rhs_provenance: list
    version: int


def solve_dense(matrix, rhs):
    """Gaussian elimination with partial pivoting (small dense systems)."""
    n = len(matrix)
    if len(rhs) != n:
        raise AssemblyError("matrix/rhs size mismatch")
    a = [list(row) + [float(rhs[i])] for i, row in enumerate(matrix)]
    for col in range(n):
        piv = max(range(col, n), key=lambda r: abs(a[r][col]))
        if abs(a[piv][col]) < 1e-15:
            raise AssemblyError("singular system")
        a[col], a[piv] = a[piv], a[col]
        for r in range(col + 1, n):
            factor = a[r][col] / a[col][col]
            if factor == 0.0:
                continue
            for c in range(col, n + 1):
                a[r][c] -= factor * a[col][c]
    x = [0.0] * n
    for i in range(n - 1, -1, -1):
        s = a[i][n]
        for j in range(i + 1, n):
            s -= a[i][j] * x[j]
        x[i] = s / a[i][i]
    return x


class Assembler:
    """Global matrix/rhs assembler with provenance and constraints."""

    def __init__(self, n_dofs):
        if isinstance(n_dofs, bool) or not isinstance(n_dofs, int) or n_dofs <= 0:
            raise AssemblyError("n_dofs must be a positive integer")
        self.n = n_dofs
        self.matrix = [[0.0] * n_dofs for _ in range(n_dofs)]
        self.rhs = [0.0] * n_dofs
        self._elements = {}
        self._matrix_prov = [[[] for _ in range(n_dofs)] for _ in range(n_dofs)]
        self._rhs_prov = [[] for _ in range(n_dofs)]
        self._constraints = {}
        self.constraint_version = 0
        # Number of global-entry modifications since the last reset; proves
        # element updates are local and never traverse all elements.
        self.touch_count = 0

    # ------------------------------------------------------------------
    # element batch API (atomic)
    # ------------------------------------------------------------------

    def add_element(self, elem_id, dof_map, ke, fe):
        self.apply_batch([("add", elem_id, dof_map, ke, fe)])

    def update_element(self, elem_id, dof_map, ke, fe):
        self.apply_batch([("update", elem_id, dof_map, ke, fe)])

    def remove_element(self, elem_id):
        self.apply_batch([("remove", elem_id)])

    def apply_batch(self, ops):
        """Validate then apply a batch; any invalid op rejects everything."""
        prepared = []
        planned = set(self._elements)
        for op in ops:
            if not op:
                raise AssemblyError("empty operation")
            kind = op[0]
            if kind in ("add", "update"):
                if len(op) != 5:
                    raise AssemblyError(f"malformed {kind} operation: {op!r}")
                _, elem_id, dof_map, ke, fe = op
                elem = self._validate_element(elem_id, dof_map, ke, fe)
                if kind == "add" and elem_id in planned:
                    raise AssemblyError(f"duplicate element id {elem_id!r}")
                if kind == "update" and elem_id not in planned:
                    raise AssemblyError(f"unknown element id {elem_id!r}")
                planned.add(elem_id)
                prepared.append((kind, elem))
            elif kind == "remove":
                if len(op) != 2:
                    raise AssemblyError(f"malformed remove operation: {op!r}")
                _, elem_id = op
                if elem_id not in planned:
                    raise AssemblyError(f"unknown element id {elem_id!r}")
                planned.discard(elem_id)
                prepared.append((kind, elem_id))
            else:
                raise AssemblyError(f"unknown operation {kind!r}")
        for kind, payload in prepared:
            if kind == "add":
                self._elements[payload.elem_id] = payload
                self._apply(payload)
            elif kind == "update":
                self._withdraw(self._elements[payload.elem_id])
                self._elements[payload.elem_id] = payload
                self._apply(payload)
            else:
                self._withdraw(self._elements[payload])
                del self._elements[payload]

    def _validate_element(self, elem_id, dof_map, ke, fe):
        if elem_id is None:
            raise AssemblyError("element id must not be None")
        try:
            m = len(dof_map)
        except TypeError:
            raise AssemblyError("dof_map must be a sequence") from None
        if m == 0:
            raise AssemblyError("dof_map must not be empty")
        if len(set(dof_map)) != m:
            raise AssemblyError("dof_map contains duplicate dofs")
        for d in dof_map:
            _check_dof(d, self.n)
        try:
            if len(ke) != m or any(len(row) != m for row in ke):
                raise AssemblyError("stiffness matrix shape does not match dof_map")
            if len(fe) != m:
                raise AssemblyError("load vector length does not match dof_map")
            for row in ke:
                for v in row:
                    if not _is_number(v):
                        raise AssemblyError(f"non-numeric stiffness entry {v!r}")
            for v in fe:
                if not _is_number(v):
                    raise AssemblyError(f"non-numeric load entry {v!r}")
        except TypeError:
            raise AssemblyError("ke/fe must be sequences of numbers") from None
        return _Element(
            elem_id,
            tuple(dof_map),
            tuple(tuple(float(v) for v in row) for row in ke),
            tuple(float(v) for v in fe),
        )

    def _apply(self, elem):
        dm = elem.dof_map
        for li, gi in enumerate(dm):
            self.rhs[gi] += elem.fe[li]
            self._rhs_prov[gi].append(
                Contribution("element", elem.elem_id, (li,), 1.0, elem.fe[li], 0))
            self.touch_count += 1
            for lj, gj in enumerate(dm):
                self.matrix[gi][gj] += elem.ke[li][lj]
                self._matrix_prov[gi][gj].append(
                    Contribution("element", elem.elem_id, (li, lj), 1.0,
                                 elem.ke[li][lj], 0))
                self.touch_count += 1

    def _withdraw(self, elem):
        dm = elem.dof_map
        eid = elem.elem_id
        for li, gi in enumerate(dm):
            self.rhs[gi] -= elem.fe[li]
            prov = self._rhs_prov[gi]
            prov[:] = [c for c in prov
                       if not (c.kind == "element" and c.source == eid)]
            self.touch_count += 1
            for lj, gj in enumerate(dm):
                self.matrix[gi][gj] -= elem.ke[li][lj]
                prov = self._matrix_prov[gi][gj]
                prov[:] = [c for c in prov
                           if not (c.kind == "element" and c.source == eid)]
                self.touch_count += 1

    # ------------------------------------------------------------------
    # provenance access
    # ------------------------------------------------------------------

    def provenance(self, i, j=None):
        """Recorded contributions to matrix entry (i, j), or rhs[i] if j is None."""
        _check_dof(i, self.n)
        if j is None:
            return list(self._rhs_prov[i])
        _check_dof(j, self.n)
        return list(self._matrix_prov[i][j])

    def element_ids(self):
        return list(self._elements)

    def reset_touch_count(self):
        self.touch_count = 0

    # ------------------------------------------------------------------
    # constraints
    # ------------------------------------------------------------------

    def set_constraints(self, constraints=()):
        """Replace the constraint set and bump ``constraint_version``.

        The old constrained assembly is discarded; the reduced system is
        rebuilt lazily from the untouched element-level assembly, so a
        constraint change never re-visits element data.
        """
        new = {}
        for c in constraints:
            con = self._coerce_constraint(c)
            if con.dof in new:
                raise AssemblyError(f"dof {con.dof} constrained twice")
            new[con.dof] = con
        for con in new.values():
            for d, _ in con.terms:
                if d in new:
                    raise AssemblyError(
                        f"constraint on dof {con.dof} references constrained dof {d}")
        self._constraints = new
        self.constraint_version += 1

    def _coerce_constraint(self, c):
        if isinstance(c, Constraint):
            con = c
        else:
            try:
                dof, terms, *rest = c
            except (TypeError, ValueError):
                raise AssemblyError(f"malformed constraint {c!r}") from None
            constant = rest[0] if rest else 0.0
            con = Constraint(dof, tuple(terms), constant)
        _check_dof(con.dof, self.n)
        terms = []
        seen = set()
        for item in con.terms:
            try:
                d, coef = item
            except (TypeError, ValueError):
                raise AssemblyError(f"malformed constraint term {item!r}") from None
            _check_dof(d, self.n)
            if d == con.dof:
                raise AssemblyError("constraint references its own dof")
            if d in seen:
                raise AssemblyError(f"duplicate term for dof {d}")
            if not _is_number(coef):
                raise AssemblyError(f"non-numeric coefficient {coef!r}")
            seen.add(d)
            terms.append((d, float(coef)))
        if not _is_number(con.constant):
            raise AssemblyError(f"non-numeric constant {con.constant!r}")
        return Constraint(con.dof, tuple(terms), float(con.constant))

    # ------------------------------------------------------------------
    # reduced (constraint-eliminated) system
    # ------------------------------------------------------------------

    def constrained_system(self):
        """Eliminate constraints: u = T v + g over the free dofs v.

        A_red = T^T A T,  b_red = T^T (b - A g).  The second term is the
        right-hand-side correction from non-homogeneous constraints.
        """
        n = self.n
        free = [i for i in range(n) if i not in self._constraints]
        pos = {d: k for k, d in enumerate(free)}
        m = len(free)
        T = [[0.0] * m for _ in range(n)]
        g = [0.0] * n
        for k, d in enumerate(free):
            T[d][k] = 1.0
        for c, con in self._constraints.items():
            g[c] = con.constant
            for d, coef in con.terms:
                T[c][pos[d]] += coef

        nz = [[(p, v) for p, v in enumerate(row) if v != 0.0] for row in T]

        Ag = [0.0] * n
        for i in range(n):
            s = 0.0
            for c in self._constraints:
                if g[c] != 0.0:
                    s += self.matrix[i][c] * g[c]
            Ag[i] = s

        version = self.constraint_version
        A_red = [[0.0] * m for _ in range(m)]
        b_red = [0.0] * m
        prov_red = [[[] for _ in range(m)] for _ in range(m)]
        rhs_prov_red = [[] for _ in range(m)]

        for i in range(n):
            rhs_i = self.rhs[i] - Ag[i]
            for p, wip in nz[i]:
                b_red[p] += wip * rhs_i
            for c in self._rhs_prov[i]:
                for p, wip in nz[i]:
                    rhs_prov_red[p].append(Contribution(
                        "element", c.source, c.local_index, wip,
                        wip * c.value, version))
            for cdof in self._constraints:
                gc = g[cdof]
                a = self.matrix[i][cdof]
                if gc == 0.0 or a == 0.0:
                    continue
                for p, wip in nz[i]:
                    rhs_prov_red[p].append(Contribution(
                        "constraint", cdof, (i, cdof), -wip * gc,
                        -wip * a * gc, version))
            for j in range(n):
                aij = self.matrix[i][j]
                if aij != 0.0:
                    for p, wip in nz[i]:
                        for q, wjq in nz[j]:
                            A_red[p][q] += wip * aij * wjq
                for c in self._matrix_prov[i][j]:
                    for p, wip in nz[i]:
                        for q, wjq in nz[j]:
                            prov_red[p][q].append(Contribution(
                                "element", c.source, c.local_index,
                                wip * wjq, wip * wjq * c.value, version))
        return ReducedSystem(A_red, b_red, free, T, g,
                             prov_red, rhs_prov_red, version)

    def solve(self):
        """Solve the constrained system and recover all n dofs."""
        rs = self.constrained_system()
        v = solve_dense(rs.matrix, rs.rhs)
        u = list(rs.offset)
        for i in range(self.n):
            row = rs.transform[i]
            s = u[i]
            for p in range(len(v)):
                if row[p] != 0.0:
                    s += row[p] * v[p]
            u[i] = s
        return u
