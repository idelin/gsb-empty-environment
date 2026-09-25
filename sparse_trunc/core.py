"""Sparse truncation of a chain of matrix-vector products.

The chain computes

    z_1 = A_0 z_0, z_2 = A_1 z_1, ..., z_L = A_{L-1} z_{L-1}

with interval input z_0 in [lo_0, hi_0].  Each layer may drop matrix
entries (stored as exact structural zeros thereafter).  The module issues a
*conservative* componentwise L_inf error certificate for the whole chain:

    e_0 = 0
    e_{l+1}[i] = sum_j |A~_l[i,j]| e_l[j]
                 + sum_{(i,j) dropped in layer l} |A_l[i,j]| M_l[j]

where M_l is a componentwise magnitude bound on the *exact* stage l input
obtained by sign-exact interval propagation through the exact matrices, and
A~_l is the matrix actually used after truncation.  Negative coefficients are
propagated exactly (intervals flip on negative multipliers).  The bound at one
layer is multiplied through every later layer, so a large downstream norm
amplifies an early truncation in the certificate instead of being hidden by
independently certifying each layer.

Only the Python standard library is used.
"""

from __future__ import annotations

from dataclasses import dataclass, field
from typing import Dict, Iterable, List, Optional, Sequence, Tuple

EPS_REL = 1e-12


class SparseMatrix:
    """Explicitly shaped sparse matrix keyed by ``(row, col)``.

    Shapes and indices are explicit inputs; zero values are structural zeros
    and can never be truncated.  Entries are stored in a dict mapping
    ``(r, c) -> value``; dropped entries live in a second dict so they can be
    restored (revocable approximation).
    """

    def __init__(self, rows: int, cols: int,
                 entries: Iterable[Tuple[int, int, float]],
                 name: str = "A") -> None:
        if not isinstance(rows, int) or not isinstance(cols, int):
            raise ValueError("rows/cols must be ints (explicit dimensions)")
        if rows <= 0 or cols <= 0:
            raise ValueError("matrix dimensions must be positive")
        self.rows = rows
        self.cols = cols
        self.name = name
        kept: Dict[Tuple[int, int], float] = {}
        for entry in entries:
            if len(entry) != 3:
                raise ValueError("entries must be triples (row, col, value)")
            r, c, v = entry
            if not isinstance(r, int) or not isinstance(c, int):
                raise ValueError("indices must be ints")
            if not (0 <= r < rows and 0 <= c < cols):
                raise ValueError(
                    f"entry ({r},{c}) out of range for {rows}x{cols}")
            if v != 0.0:
                kept[(r, c)] = float(v)
            else:
                kept.pop((r, c), None)
        self.entries: Dict[Tuple[int, int], float] = kept
        self.dropped: Dict[Tuple[int, int], float] = {}

    def clone(self) -> "SparseMatrix":
        other = SparseMatrix(self.rows, self.cols, [], self.name)
        other.entries = dict(self.entries)
        other.dropped = dict(self.dropped)
        return other

    @property
    def kept_nnz(self) -> int:
        return len(self.entries)

    @property
    def exact_nnz(self) -> int:
        return len(self.entries) + len(self.dropped)

    def get(self, r: int, c: int) -> float:
        return self.entries.get((r, c), 0.0)

    def exact_get(self, r: int, c: int) -> float:
        if (r, c) in self.entries:
            return self.entries[(r, c)]
        return self.dropped.get((r, c), 0.0)

    def row_map(self) -> Dict[int, Dict[int, float]]:
        out: Dict[int, Dict[int, float]] = {}
        for (r, c), v in self.entries.items():
            out.setdefault(r, {})[c] = v
        return out

    def exact_row_map(self) -> Dict[int, Dict[int, float]]:
        out: Dict[int, Dict[int, float]] = {}
        for (r, c), v in self.entries.items():
            out.setdefault(r, {})[c] = v
        for (r, c), v in self.dropped.items():
            out.setdefault(r, {})[c] = v
        return out

    def restore(self, r: int, c: int) -> bool:
        """Revoke a truncation; return True if a dropped entry came back."""
        key = (r, c)
        if key in self.dropped:
            self.entries[key] = self.dropped.pop(key)
            return True
        return False

    def restore_all(self) -> int:
        n = len(self.dropped)
        self.entries.update(self.dropped)
        self.dropped.clear()
        return n

    def drop(self, r: int, c: int) -> bool:
        key = (r, c)
        if key in self.entries:
            self.dropped[key] = self.entries.pop(key)
            return True
        return False


def abs_matvec(mat: SparseMatrix, vec: Sequence[float]) -> List[float]:
    """Return ``|mat| vec`` using kept entries only."""
    if len(vec) != mat.cols:
        raise ValueError(f"{mat.name}: vector length {len(vec)} != cols {mat.cols}")
    out = [0.0] * mat.rows
    for (r, c), v in mat.entries.items():
        out[r] += abs(v) * vec[c]
    return out


def interval_matvec(mat: SparseMatrix, lo: Sequence[float],
                    hi: Sequence[float]) -> Tuple[List[float], List[float]]:
    """Tight sign-exact interval image using kept entries only.

    A negative coefficient swaps the endpoint of its input interval, so
    negative coefficients and input uncertainty are both propagated.
    """
    if len(lo) != mat.cols or len(hi) != mat.cols:
        raise ValueError(f"{mat.name}: interval width mismatch with cols {mat.cols}")
    new_lo = [0.0] * mat.rows
    new_hi = [0.0] * mat.rows
    rows = mat.row_map()
    for r in range(mat.rows):
        lo_sum = 0.0
        hi_sum = 0.0
        for c, v in rows.get(r, {}).items():
            if v >= 0.0:
                lo_sum += v * lo[c]
                hi_sum += v * hi[c]
            else:
                lo_sum += v * hi[c]
                hi_sum += v * lo[c]
        new_lo[r] = lo_sum
        new_hi[r] = hi_sum
    return new_lo, new_hi


def matvec(mat: SparseMatrix, vec: Sequence[float]) -> List[float]:
    """Deterministic ``mat vec`` using kept entries only."""
    if len(vec) != mat.cols:
        raise ValueError(f"{mat.name}: vector length {len(vec)} != cols {mat.cols}")
    out = [0.0] * mat.rows
    for (r, c), v in mat.entries.items():
        out[r] += v * vec[c]
    return out


@dataclass
class LayerCertificate:
    layer: int
    shape: Tuple[int, int]
    threshold: float
    kept_nnz: int
    exact_nnz: int
    injected_linf: float
    final_contribution_linf: float
    dropped: List[Tuple[int, int, float]]


@dataclass
class Certificate:
    budget: float
    final_linf_bound: float
    feasible: bool
    within_budget: bool
    within_caps: bool
    total_kept_nnz: int
    total_exact_nnz: int
    total_cap: Optional[int]
    layer_caps: List[Optional[int]]
    unsatisfied_layers: List[int]
    layers: List[LayerCertificate] = field(default_factory=list)
    stage_lo: List[List[float]] = field(default_factory=list)
    stage_hi: List[List[float]] = field(default_factory=list)
    stage_bound: List[List[float]] = field(default_factory=list)

    def lines(self) -> List[str]:
        out: List[str] = []
        out.append("chain L_inf certificate")
        out.append(f"  global budget            : {self.budget:.6g}")
        out.append(f"  final |error|_inf bound  : {self.final_linf_bound:.6g}")
        out.append(f"  within budget            : {self.within_budget}")
        out.append(f"  within nnz caps          : {self.within_caps}")
        out.append(f"  feasible (both)          : {self.feasible}")
        cap = "none" if self.total_cap is None else str(self.total_cap)
        out.append(f"  nnz kept / exact / cap   : "
                   f"{self.total_kept_nnz} / {self.total_exact_nnz} / {cap}")
        for lc in self.layers:
            out.append(
                f"  layer {lc.layer} {lc.shape[0]}x{lc.shape[1]}: "
                f"threshold={lc.threshold:.6g} "
                f"nnz={lc.kept_nnz}/{lc.exact_nnz} "
                f"(cap={self.layer_caps[lc.layer]}) "
                f"injected={lc.injected_linf:.6g} "
                f"final_contrib={lc.final_contribution_linf:.6g}")
        if self.unsatisfied_layers:
            out.append("  UNSATISFIED layers (kept exact): "
                       + ", ".join(str(i) for i in self.unsatisfied_layers))
        return out

    def render(self) -> str:
        return "\n".join(self.lines())


class TruncatedChain:
    """Chain of sparse matrices with an interval input and an error ledger."""

    def __init__(self, matrices: Sequence[SparseMatrix],
                 input_lo: Sequence[float], input_hi: Sequence[float]) -> None:
        if not matrices:
            raise ValueError("at least one layer required")
        self.layers: List[SparseMatrix] = [m.clone() for m in matrices]
        dim = self.layers[0].cols
        if len(input_lo) != dim or len(input_hi) != dim:
            raise ValueError("input interval length must match layer 0 columns")
        self.input_lo: List[float] = [float(x) for x in input_lo]
        self.input_hi: List[float] = [float(x) for x in input_hi]
        if any(a > b for a, b in zip(self.input_lo, self.input_hi)):
            raise ValueError("input_lo must be <= input_hi componentwise")
        for l in range(1, len(self.layers)):
            if self.layers[l].cols != self.layers[l - 1].rows:
                raise ValueError(
                    f"dimension mismatch between layers {l - 1} and {l}")
        self.ledger: List[str] = []
        self._snapshots: Dict[str, dict] = {}

    def snapshot(self, name: str) -> None:
        self._snapshots[name] = self._state()

    def rollback(self, name: str) -> None:
        # Restoring the snapshot also truncates the ledger at the snapshot
        # point, so later undo() calls replay pre-snapshot events correctly.
        if name not in self._snapshots:
            raise KeyError(f"no snapshot named {name!r}")
        self._restore(self._snapshots[name])

    def undo(self, n: int = 1) -> int:
        """Undo up to ``n`` mutating ledger events.

        Returns the number actually undone.  Restores exact matrices and the
        error ledger entry.
        """
        undone = 0
        for _ in range(n):
            if not self.ledger:
                break
            event = self.ledger.pop()
            self._invert(event)
            undone += 1
        return undone

    def restore_entry(self, layer: int, r: int, c: int) -> bool:
        self._check_index(layer, r, c)
        mat = self.layers[layer]
        if mat.restore(r, c):
            self.ledger.append(f"restore:{layer}:{r}:{c}")
            return True
        return False

    def restore_layer(self, layer: int) -> int:
        if not 0 <= layer < len(self.layers):
            raise IndexError("layer out of range")
        before = [f"{r},{c}" for (r, c) in self.layers[layer].dropped]
        n = self.layers[layer].restore_all()
        if n or before:
            self.ledger.append(
                f"restore_layer:{layer}:{';'.join(before)}")
        return n

    def edit_exact_entry(self, layer: int, r: int, c: int,
                         value: float) -> None:
        """Modify the exact matrix at an index (local input modification).

        The edit hits the kept value if the entry is kept, or the hidden exact
        value if it is currently dropped; a zero edit removes it as a
        structural zero.
        """
        self._check_index(layer, r, c)
        mat = self.layers[layer]
        key = (r, c)
        if key in mat.entries:
            old = ("kept", mat.entries[key])
        elif key in mat.dropped:
            old = ("dropped", mat.dropped[key])
        else:
            old = ("absent", 0.0)
        if value == 0.0:
            mat.entries.pop(key, None)
            mat.dropped.pop(key, None)
        elif old[0] == "dropped":
            mat.dropped[key] = float(value)
        else:
            mat.entries[key] = float(value)
        self.ledger.append(
            f"edit:{layer}:{r}:{c}:{old[0]}:{old[1]}:{value}")

    def update_input(self, lo: Sequence[float], hi: Sequence[float]) -> None:
        """Local input modification: replace the input interval."""
        if len(lo) != len(self.input_lo) or len(hi) != len(self.input_hi):
            raise ValueError("updated interval length mismatch")
        old_lo = list(self.input_lo)
        old_hi = list(self.input_hi)
        self.input_lo = [float(x) for x in lo]
        self.input_hi = [float(x) for x in hi]
        if any(a > b for a, b in zip(self.input_lo, self.input_hi)):
            self.input_lo, self.input_hi = old_lo, old_hi
            raise ValueError("input_lo must be <= input_hi componentwise")
        self.ledger.append(
            "input:" + ";".join(map(repr, old_lo))
            + "|" + ";".join(map(repr, old_hi)))

    def _check_index(self, layer: int, r: int, c: int) -> None:
        if not 0 <= layer < len(self.layers):
            raise IndexError("layer out of range")
        mat = self.layers[layer]
        if not (0 <= r < mat.rows and 0 <= c < mat.cols):
            raise IndexError(f"({r},{c}) out of range for layer {layer}")

    def _state(self) -> dict:
        return {
            "layers": [
                {"entries": dict(m.entries), "dropped": dict(m.dropped)}
                for m in self.layers
            ],
            "input_lo": list(self.input_lo),
            "input_hi": list(self.input_hi),
            "ledger_len": len(self.ledger),
        }

    def _restore(self, state: dict) -> None:
        for mat, s in zip(self.layers, state["layers"]):
            mat.entries = dict(s["entries"])
            mat.dropped = dict(s["dropped"])
        self.input_lo = list(state["input_lo"])
        self.input_hi = list(state["input_hi"])
        del self.ledger[state["ledger_len"]:]

    def _invert(self, event: str) -> None:
        head, _, rest = event.partition(":")
        if head == "drop":
            layer_s, r_s, c_s = rest.split(":")
            self.layers[int(layer_s)].restore(int(r_s), int(c_s))
        elif head == "restore":
            layer_s, r_s, c_s = rest.split(":")
            self.layers[int(layer_s)].drop(int(r_s), int(c_s))
        elif head == "restore_layer":
            layer_s, token = rest.split(":", 1)
            layer = int(layer_s)
            for chunk in token.split(";"):
                if chunk:
                    r, c = (int(x) for x in chunk.split(","))
                    self.layers[layer].drop(r, c)
        elif head == "edit":
            layer_s, r_s, c_s, where, old_s, _ = rest.split(":")
            layer, r, c = int(layer_s), int(r_s), int(c_s)
            mat = self.layers[layer]
            key = (r, c)
            mat.entries.pop(key, None)
            mat.dropped.pop(key, None)
            old = float(old_s)
            if where == "kept":
                mat.entries[key] = old
            elif where == "dropped":
                mat.dropped[key] = old
        elif head == "input":
            old_lo_s, old_hi_s = rest.split("|")
            self.input_lo = [float(x) for x in old_lo_s.split(";")]
            self.input_hi = [float(x) for x in old_hi_s.split(";")]
        elif head == "schedule":
            before = rest.split("#", 1)[0]
            self._apply_dropsets(before)
        # rollback events are bookkeeping only

    def _encode_dropsets(self) -> str:
        parts: List[str] = []
        for l, mat in enumerate(self.layers):
            for (r, c) in mat.dropped:
                parts.append(f"{l},{r},{c}")
        return ";".join(parts)

    def _apply_dropsets(self, token: str) -> None:
        wanted = {l: set() for l in range(len(self.layers))}
        for chunk in token.split(";"):
            if chunk:
                l, r, c = (int(x) for x in chunk.split(","))
                wanted[l].add((r, c))
        for l, mat in enumerate(self.layers):
            for key in list(mat.dropped):
                mat.restore(*key)
            for key in wanted[l]:
                mat.drop(*key)

    def exact_propagation(
        self
    ) -> Tuple[List[List[float]], List[List[float]], List[List[float]]]:
        """Sign-exact interval propagation through the *exact* matrices.

        Negative coefficients flip interval endpoints.  The returned
        componentwise bound ``bounds[l]`` drives every injected-error term.
        """
        los = [list(self.input_lo)]
        his = [list(self.input_hi)]
        bounds = [[max(abs(a), abs(b)) for a, b in zip(los[0], his[0])]]
        for mat in self._exact_matrices():
            lo, hi = self._interval_matvec_exact(mat, los[-1], his[-1])
            los.append(lo)
            his.append(hi)
            bounds.append([max(abs(a), abs(b)) for a, b in zip(lo, hi)])
        return los, his, bounds

    def _exact_matrices(self) -> List[SparseMatrix]:
        out: List[SparseMatrix] = []
        for mat in self.layers:
            full = SparseMatrix(mat.rows, mat.cols, [], mat.name)
            full.entries = {**mat.entries, **mat.dropped}
            out.append(full)
        return out

    @staticmethod
    def _interval_matvec_exact(mat: SparseMatrix, lo: Sequence[float],
                               hi: Sequence[float]
                               ) -> Tuple[List[float], List[float]]:
        new_lo = [0.0] * mat.rows
        new_hi = [0.0] * mat.rows
        rows = mat.row_map()
        for r in range(mat.rows):
            lo_sum = 0.0
            hi_sum = 0.0
            for c, v in rows.get(r, {}).items():
                if v >= 0.0:
                    lo_sum += v * lo[c]
                    hi_sum += v * hi[c]
                else:
                    lo_sum += v * hi[c]
                    hi_sum += v * lo[c]
            new_lo[r] = lo_sum
            new_hi[r] = hi_sum
        return new_lo, new_hi

    def _cert_state(self) -> dict:
        """Bound computation through the current (possibly truncated) chain."""
        _, _, bounds = self.exact_propagation()
        n = len(self.layers)
        err = [0.0] * self.layers[0].cols
        injected: List[List[float]] = []
        errors: List[List[float]] = [list(err)]
        for l, mat in enumerate(self.layers):
            d = [0.0] * mat.rows
            for (r, c), v in mat.dropped.items():
                d[r] += abs(v) * bounds[l][c]
            injected.append(d)
            carried = abs_matvec(mat, err)
            err = [carried[i] + d[i] for i in range(mat.rows)]
            errors.append(list(err))
        return {"bounds": bounds, "injected": injected, "errors": errors}

    def propagate_intervals_current(
        self
    ) -> Tuple[List[List[float]], List[List[float]]]:
        """Sign-exact interval propagation through the truncated matrices."""
        los = [list(self.input_lo)]
        his = [list(self.input_hi)]
        for mat in self.layers:
            lo, hi = interval_matvec(mat, los[-1], his[-1])
            los.append(lo)
            his.append(hi)
        return los, his

    def _candidate_cost(self, layer: int, key: Tuple[int, int],
                        state: dict) -> float:
        """Final ||e||_inf if one extra kept entry were dropped at ``layer``."""
        mat = self.layers[layer]
        r, c = key
        value = mat.entries[key]
        bounds = state["bounds"]
        err = [0.0] * self.layers[0].cols
        for l, m in enumerate(self.layers):
            d = list(state["injected"][l])
            carried = abs_matvec(m, err)
            if l == layer:
                carried[r] -= abs(value) * err[c]
                d[r] += abs(value) * bounds[l][c]
            err = [carried[i] + d[i] for i in range(m.rows)]
        return max(err) if err else 0.0

    @staticmethod
    def _norm_key(mat: SparseMatrix,
                  key: Tuple[int, int]) -> Tuple[float, int, int]:
        return (abs(mat.entries[key]), key[0], key[1])

    def schedule_truncate(self, budget: float,
                          nnz_caps: Sequence[Optional[int]],
                          total_cap: Optional[int] = None) -> Certificate:
        """Decide per-layer thresholds and kept-nnz under the global budget.

        Per layer, candidates are kept entries ordered by
        ``(|value|, row, col)``; equal magnitudes follow the stable coordinate
        order, so results are deterministic.  Each step drops the candidate
        whose resulting *final* chain bound is smallest across all layers, so
        early error amplification by large later-layer norms is priced into
        the choice.  A step is rejected when its cost would exceed the budget
        or a cap; when no layer can move while a cap is unmet, the exact part
        is retained and the layer is reported unsatisfied.
        """
        if budget < 0.0:
            raise ValueError("budget must be non-negative")
        caps = list(nnz_caps)
        if len(caps) != len(self.layers):
            raise ValueError("one nnz cap per layer required (use None)")
        for l, cap in enumerate(caps):
            if cap is not None and cap < 0:
                raise ValueError("nnz cap must be non-negative or None")
        self.snapshot("__before_schedule__")
        before = self._encode_dropsets()
        # `before` captured here is the pre-schedule dropset.
        while True:
            state = self._cert_state()
            best = None
            for l, mat in enumerate(self.layers):
                cap = caps[l]
                if cap is not None and len(mat.entries) <= cap:
                    continue
                if total_cap is not None:
                    total_kept = sum(len(m.entries) for m in self.layers)
                    if total_kept <= total_cap:
                        continue
                if not mat.entries:
                    continue
                key = min(mat.entries,
                          key=lambda k, m=mat: self._norm_key(m, k))
                cost = self._candidate_cost(l, key, state)
                tie = (cost, l, key[0], key[1])
                if best is None or tie < best[0]:
                    best = (tie, l, key, cost)
            if best is None:
                break
            _, layer, key, cost = best
            if cost > budget * (1.0 + EPS_REL) + EPS_REL:
                break
            self.layers[layer].drop(*key)
        after = self._encode_dropsets()
        self.ledger.append(f"schedule:{before}#{after}")
        # Note: undo applies `before` to revoke the whole schedule.
        return self.certify(budget, caps, total_cap)

    def exact_outputs(self, points: Iterable[Sequence[float]]
                      ) -> List[List[float]]:
        """Exact chain output at concrete input points (for validation)."""
        exact = self._exact_matrices()
        results: List[List[float]] = []
        for point in points:
            x = [float(v) for v in point]
            for mat in exact:
                x = matvec(mat, x)
            results.append(x)
        return results

    def approximate_outputs(self, points: Iterable[Sequence[float]]
                            ) -> List[List[float]]:
        """Truncated-chain output at concrete input points (for validation)."""
        results: List[List[float]] = []
        for point in points:
            x = [float(v) for v in point]
            for mat in self.layers:
                x = matvec(mat, x)
            results.append(x)
        return results

    def certify(self, budget: float,
                nnz_caps: Sequence[Optional[int]],
                total_cap: Optional[int] = None) -> Certificate:
        """Build the layer-by-layer conservative error propagation certificate."""
        caps = list(nnz_caps)
        state = self._cert_state()
        bounds = state["bounds"]
        injected = state["injected"]
        errors = state["errors"]
        final_bound = max(errors[-1]) if errors[-1] else 0.0

        layer_certs: List[LayerCertificate] = []
        unsatisfied: List[int] = []
        total_kept = 0
        total_exact = 0
        within_caps = True
        for l, mat in enumerate(self.layers):
            cap = caps[l]
            kept = len(mat.entries)
            exact_nnz = kept + len(mat.dropped)
            total_kept += kept
            total_exact += exact_nnz
            if cap is not None and kept > cap:
                within_caps = False
                unsatisfied.append(l)
            dropped_rows = sorted(
                (r, c, v) for (r, c), v in mat.dropped.items())
            if dropped_rows:
                threshold = max(abs(v) for _, _, v in dropped_rows)
            else:
                threshold = 0.0
            injected_linf = max(injected[l]) if injected[l] else 0.0
            # This layer alone: propagate its injected vector through the
            # suffix of truncated matrices (the shared-coupling certificate
            # uses all injections at once; this is attribution only).
            d = list(injected[l])
            for m in self.layers[l + 1:]:
                d = abs_matvec(m, d)
            contrib = max(d) if d else 0.0
            layer_certs.append(LayerCertificate(
                layer=l, shape=(mat.rows, mat.cols), threshold=threshold,
                kept_nnz=kept, exact_nnz=exact_nnz,
                injected_linf=injected_linf,
                final_contribution_linf=contrib,
                dropped=dropped_rows))
        if total_cap is not None and total_kept > total_cap:
            within_caps = False
        within_budget = final_bound <= budget * (1.0 + EPS_REL) + EPS_REL
        return Certificate(
            budget=budget, final_linf_bound=final_bound,
            feasible=within_budget and within_caps,
            within_budget=within_budget, within_caps=within_caps,
            total_kept_nnz=total_kept, total_exact_nnz=total_exact,
            total_cap=total_cap, layer_caps=caps,
            unsatisfied_layers=unsatisfied, layers=layer_certs,
            stage_lo=[], stage_hi=[],
            stage_bound=bounds)
