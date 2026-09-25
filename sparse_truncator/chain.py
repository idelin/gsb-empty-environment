"""Sparse truncation of matrix chains with conservative L-inf certificates.

Model
-----
A chain applies ``L`` layers to an input vector ``x`` known only to lie in
an axis-aligned box::

    z_0 = x,        z_i = M_i z_{i-1},        f(x) = z_L

Each layer matrix ``M_i`` is truncated to ``M_hat_i = M_i - E_i`` where
``E_i`` collects the dropped entries.  Writing ``z_{i-1}(x)`` for the exact
intermediate, the true deviation telescopes exactly as::

    f(x) - f_hat(x) = sum_i  M_hat_L ... M_hat_{i+1}  E_i  z_{i-1}(x)

so a sound per-layer bound is::

    |contribution_i| <= |M_hat_L| ... |M_hat_{i+1}|  |E_i|  mag(Z_{i-1})

where ``Z_{i-1}`` is an interval enclosure of the exact layer-``i`` input
(propagated with the *original* matrices, honouring signs), ``|.|`` on a
matrix is elementwise, and ``mag`` takes the interval magnitude.  The
global bound is the sum of the per-layer contributions -- layers are never
certified independently against the full budget.
"""
from __future__ import annotations

from dataclasses import dataclass

from .dense import abs_matrix, from_entries, identity, linf_norm, matmul, matvec
from .intervals import Interval

_TOL = 1e-12


@dataclass
class LayerCertificate:
    """Conservative error certificate for one layer."""

    layer: int
    kept_nnz: int
    dropped_nnz: int
    threshold: float            # min |kept| if anything dropped, else 0.0
    contribution: list[float]   # per-output-coordinate bound at chain output
    linf: float                 # L-inf norm of `contribution`


@dataclass
class Certificate:
    """Whole-chain conservative error-propagation certificate."""

    layers: list[LayerCertificate]
    total: list[float]          # sum of per-layer contribution vectors
    linf: float                 # global L-inf error bound
    input_box: tuple            # snapshot of the input intervals used

    def render(self) -> str:
        lines = ["layer |  kept | dropped |   threshold | layer L-inf contribution"]
        for lc in self.layers:
            lines.append(
                f"{lc.layer:5d} | {lc.kept_nnz:5d} | "
                f"{lc.dropped_nnz:7d} | {lc.threshold:11.4e} | {lc.linf:12.6e}"
            )
        lines.append(f"global L-inf bound: {self.linf:.6e}")
        return "\n".join(lines)


@dataclass
class AllocationResult:
    satisfied: bool
    certificate: Certificate
    error_budget: float
    max_total_nnz: int | None
    total_nnz: int
    message: str


class ChainTruncator:
    """Truncates a chain of matrices under a global L-inf error budget.

    Parameters
    ----------
    matrices:
        List of ``(nrows, ncols, entries)`` triples, one per layer, where
        ``entries`` is an iterable of explicit ``(row, col, value)`` tuples.
    input_box:
        Iterable of ``(lo, hi)`` pairs, one per input coordinate.
    """

    def __init__(self, matrices, input_box):
        if not matrices:
            raise ValueError("need at least one layer")
        self._shapes: list[tuple[int, int]] = []
        self._orig: list[dict] = []
        for nrows, ncols, entries in matrices:
            nrows, ncols = int(nrows), int(ncols)
            if nrows <= 0 or ncols <= 0:
                raise ValueError("layer sizes must be positive")
            d: dict = {}
            for r, c, v in entries:
                if not (0 <= r < nrows and 0 <= c < ncols):
                    raise IndexError(f"entry {(r, c)} outside {nrows}x{ncols}")
                if (r, c) in d:
                    raise ValueError(f"duplicate entry {(r, c)}")
                d[(r, c)] = float(v)
            self._shapes.append((nrows, ncols))
            self._orig.append(d)
        for i in range(1, len(self._shapes)):
            if self._shapes[i][1] != self._shapes[i - 1][0]:
                raise ValueError(
                    f"layer {i} expects {self._shapes[i][1]} inputs but layer "
                    f"{i - 1} emits {self._shapes[i - 1][0]}"
                )
        if len(input_box) != self._shapes[0][1]:
            raise ValueError("input box dimension does not match layer 0")
        self._box: list[Interval] = [
            Interval(float(lo), float(hi)) for lo, hi in input_box
        ]
        self._dropped: list[set] = [set() for _ in self._orig]
        self._ops: list[tuple] = []   # undo log ("drop"/"input", ...)

    # ------------------------------------------------------------------ views
    @property
    def shapes(self) -> list[tuple[int, int]]:
        return list(self._shapes)

    @property
    def input_box(self) -> tuple:
        return tuple(self._box)

    def original_entries(self, layer: int) -> dict:
        return dict(self._orig[layer])

    def kept_entries(self, layer: int) -> dict:
        dropped = self._dropped[layer]
        return {k: v for k, v in self._orig[layer].items() if k not in dropped}

    def dropped_entries(self, layer: int) -> dict:
        return {k: self._orig[layer][k] for k in self._dropped[layer]}

    def total_nnz(self) -> int:
        return sum(len(self._orig[i]) - len(self._dropped[i])
                   for i in range(len(self._orig)))

    # ------------------------------------------------------- range propagation
    def _forward_ranges(self) -> list[list[Interval]]:
        """Interval enclosures of the *exact* intermediates z_0 .. z_L.

        Uses the original matrices so the enclosure is valid no matter
        which entries are currently dropped.  Negative coefficients are
        handled by Interval.scale (endpoint swap).
        """
        ranges = [list(self._box)]
        z = list(self._box)
        for (nrows, _ncols), orig in zip(self._shapes, self._orig):
            rows: dict[int, list] = {}
            for (r, c), v in orig.items():
                rows.setdefault(r, []).append((c, v))
            out = []
            for r in range(nrows):
                acc = Interval(0.0, 0.0)
                for c, v in rows.get(r, ()):  # missing row -> exact zeros
                    acc = acc + z[c].scale(v)
                out.append(acc)
            z = out
            ranges.append(z)
        return ranges

    def _suffix_products(self) -> list[list[list[float]]]:
        """U[i] = |M_hat_L| |M_hat_{L-1}| ... |M_hat_i| (dense, nonnegative).

        U[i] has shape (final_dim, ncols_i); layer i's injected error
        vector ``e_i`` (dim nrows_i) reaches the output as U[i+1] @ e_i.
        """
        n_layers = len(self._orig)
        U: list = [None] * (n_layers + 1)
        U[n_layers] = identity(self._shapes[-1][0])
        for i in range(n_layers - 1, -1, -1):
            nrows, ncols = self._shapes[i]
            kept = from_entries(nrows, ncols, self.kept_entries(i))
            U[i] = matmul(U[i + 1], abs_matrix(kept))
        return U

    # ------------------------------------------------------------- certificate
    def certificate(self) -> Certificate:
        """A-priori conservative certificate for the current truncation.

        Never uses observed errors; purely interval/norm based.
        """
        ranges = self._forward_ranges()
        U = self._suffix_products()
        out_dim = self._shapes[-1][0]
        total = [0.0] * out_dim
        layer_certs = []
        for i in range(len(self._orig)):
            # Error box injected at layer i: e_i[r] = sum_c |E_i[r,c]| mag(Z_{i-1}[c])
            e = [0.0] * self._shapes[i][0]
            for (r, c) in self._dropped[i]:
                e[r] += abs(self._orig[i][(r, c)]) * ranges[i][c].magnitude()
            contribution = matvec(U[i + 1], e)  # propagate through later layers
            total = [a + b for a, b in zip(total, contribution)]
            kept = self.kept_entries(i)
            dropped = self._dropped[i]
            if dropped:
                threshold = min((abs(v) for v in kept.values()), default=0.0)
            else:
                threshold = 0.0
            layer_certs.append(LayerCertificate(
                layer=i,
                kept_nnz=len(kept),
                dropped_nnz=len(dropped),
                threshold=threshold,
                contribution=contribution,
                linf=linf_norm(contribution),
            ))
        return Certificate(
            layers=layer_certs,
            total=total,
            linf=linf_norm(total),
            input_box=tuple(self._box),
        )

    # ---------------------------------------------------------- greedy support
    def marginal_deltas(self) -> list[tuple[float, int, int, int]]:
        """Heuristic cost of dropping each kept entry, sorted stably.

        Entry ``(r, c)`` of layer ``i`` contributes roughly
        ``||U[i+1][:, r]||_inf * |v| * mag(Z_{i-1}[c])`` to the global bound.
        Ties (including equal magnitudes) are broken by stable coordinates
        ``(layer, row, col)`` so results are deterministic.
        """
        ranges = self._forward_ranges()
        U = self._suffix_products()
        out_dim = self._shapes[-1][0]
        out = []
        for i in range(len(self._orig)):
            nrows = self._shapes[i][0]
            colmax = [0.0] * nrows
            for r in range(nrows):
                colmax[r] = max(U[i + 1][row][r] for row in range(out_dim))
            for (r, c), v in self.kept_entries(i).items():
                delta = colmax[r] * abs(v) * ranges[i][c].magnitude()
                out.append((delta, i, r, c))
        out.sort(key=lambda t: (t[0], t[1], t[2], t[3]))
        return out

    # ------------------------------------------------------------- operations
    def drop(self, layer: int, row: int, col: int) -> None:
        """Truncate one entry (revocable)."""
        key = (row, col)
        if key not in self._orig[layer]:
            raise KeyError(f"no entry {key} in layer {layer}")
        if key in self._dropped[layer]:
            raise KeyError(f"entry {key} in layer {layer} already dropped")
        self._dropped[layer].add(key)
        self._ops.append(("drop", layer, row, col))

    def set_input_interval(self, j: int, lo: float, hi: float) -> None:
        """Local input modification; the certificate updates incrementally
        (only the forward interval propagation is re-run on next access)."""
        if not 0 <= j < len(self._box):
            raise IndexError(f"input coordinate {j} out of range")
        old = self._box[j]
        self._box[j] = Interval(float(lo), float(hi))
        self._ops.append(("input", j, old))

    def undo(self) -> tuple:
        """Revoke the most recent approximation choice / input edit."""
        if not self._ops:
            raise IndexError("nothing to undo")
        op = self._ops.pop()
        if op[0] == "drop":
            self._dropped[op[1]].discard((op[2], op[3]))
        elif op[0] == "input":
            self._box[op[1]] = op[2]
        else:  # pragma: no cover - defensive
            raise RuntimeError(f"unknown op {op!r}")
        return op

    def reset(self) -> None:
        """Roll back everything: original matrices and input box restored."""
        while self._ops:
            self.undo()

    # ------------------------------------------------------------- allocation
    def allocate(self, error_budget: float,
                 max_total_nnz: int | None = None) -> AllocationResult:
        """Greedily choose per-layer thresholds / nonzero budgets.

        Repeatedly drops the entry with the smallest marginal bound increase
        (stable coordinate tie-break), accepting a drop only if the exact
        recomputed certificate stays within ``error_budget``.  If
        ``max_total_nnz`` cannot be met, every drop made here is rolled
        back -- the exact part is kept -- and the result is reported as
        unsatisfied.
        """
        if error_budget < 0.0:
            return AllocationResult(
                False, self.certificate(), error_budget, max_total_nnz,
                self.total_nnz(), "negative error budget is unsatisfiable")

        mark = len(self._ops)
        while True:
            accepted = False
            for _delta, i, r, c in self.marginal_deltas():
                self.drop(i, r, c)
                if self.certificate().linf <= error_budget + _TOL:
                    accepted = True
                    break
                self.undo()
            if not accepted:
                break

        satisfied = True
        message = "ok"
        if max_total_nnz is not None and self.total_nnz() > max_total_nnz:
            satisfied = False
            message = (f"nonzero budget {max_total_nnz} unreachable within "
                       f"error budget {error_budget}; kept exact matrices")
            while len(self._ops) > mark:   # roll back: keep the exact part
                self.undo()
        cert = self.certificate()
        if cert.linf > error_budget + _TOL:
            satisfied = False
            message = (f"error budget {error_budget} unsatisfiable; "
                       "kept exact matrices")
        return AllocationResult(satisfied, cert, error_budget, max_total_nnz,
                                self.total_nnz(), message)
