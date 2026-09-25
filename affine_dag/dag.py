"""Error-propagation DAG over affine forms.

Nodes are created in topological order (inputs before outputs) and are
referenced by explicit integer indices.  Each node caches its affine
form; intervals are derived on demand from the shared NoiseSymbols
registry, so changing one noise bound only invalidates (and recomputes)
the affected successor nodes.

If a node's affine form would exceed the symbol budget ``max_terms``,
evaluation flags the result (and any certificate built from it) as
*incomplete* instead of silently condensing or truncating.
"""

from __future__ import annotations

from dataclasses import dataclass, field

from . import core
from .core import AffineForm, IntervalContainsZero, NoiseSymbols
from .piecewise import PiecewiseDomainError, PiecewiseLinear


class _UnavailableInput(ArithmeticError):
    """Internal: an input node has no usable form (upstream failure)."""


@dataclass
class Node:
    nid: int
    op: str
    inputs: tuple[int, ...]
    params: dict
    name: str = ""
    form: AffineForm | None = None
    dirty: bool = True
    sym_deps: frozenset[int] = frozenset()
    dependents: set[int] = field(default_factory=set)


@dataclass
class EvalResult:
    node: int
    form: AffineForm | None
    interval: tuple[float, float] | None
    complete: bool
    errors: list[str]
    recomputed: list[int]


class DAG:
    def __init__(self, max_terms: int | None = None):
        if max_terms is not None and max_terms < 0:
            raise ValueError("max_terms must be >= 0 or None")
        self.max_terms = max_terms
        self.symbols = NoiseSymbols()
        self.nodes: list[Node] = []

    # ------------------------------------------------------------------ build
    def _add(self, op: str, inputs, params, name: str = "") -> int:
        nid = len(self.nodes)
        inputs = tuple(int(i) for i in inputs)
        for i in inputs:
            if not 0 <= i < nid:
                raise IndexError(f"input node index {i} out of range")
        deps: set[int] = set()
        for i in inputs:
            deps |= self.nodes[i].sym_deps
        for key in ("sid", "rem_sid"):
            if key in params:
                deps.add(params[key])
        node = Node(nid, op, inputs, dict(params), name, sym_deps=frozenset(deps))
        self.nodes.append(node)
        for i in inputs:
            self.nodes[i].dependents.add(nid)
        return nid

    def var(self, lo: float, hi: float, name: str = "") -> int:
        lo, hi = float(lo), float(hi)
        if not lo <= hi:
            raise ValueError(f"empty variable domain [{lo}, {hi}]")
        sid = self.symbols.new((hi - lo) / 2.0)
        return self._add("var", (), {"lo": lo, "hi": hi, "sid": sid}, name)

    def const(self, value: float, name: str = "") -> int:
        return self._add("const", (), {"value": float(value)}, name)

    def add(self, a: int, b: int, name: str = "") -> int:
        return self._add("add", (a, b), {}, name)

    def sub(self, a: int, b: int, name: str = "") -> int:
        return self._add("sub", (a, b), {}, name)

    def scale(self, a: int, factor: float, name: str = "") -> int:
        return self._add("scale", (a,), {"factor": float(factor)}, name)

    def mul(self, a: int, b: int, name: str = "") -> int:
        return self._add("mul", (a, b), {"rem_sid": self.symbols.new(0.0)}, name)

    def recip(self, a: int, name: str = "") -> int:
        return self._add("recip", (a,), {"rem_sid": self.symbols.new(0.0)}, name)

    def div(self, a: int, b: int, name: str = "") -> int:
        r = self.recip(b, name=(name + "/recip") if name else "")
        return self.mul(a, r, name=name)

    def piecewise(self, a: int, pl: PiecewiseLinear, name: str = "") -> int:
        return self._add(
            "piecewise",
            (a,),
            {
                "rem_sid": self.symbols.new(0.0),
                "breakpoints": list(pl.breakpoints),
                "values": list(pl.values),
            },
            name,
        )

    # ------------------------------------------------------------- evaluation
    def _input_forms(self, node: Node) -> list[AffineForm]:
        forms = []
        for i in node.inputs:
            f = self.nodes[i].form
            if f is None:
                raise _UnavailableInput(
                    f"input node {i} has no affine form (upstream failure)"
                )
            forms.append(f)
        return forms

    def _assign_remainder(self, node: Node, form: AffineForm, rem: float) -> AffineForm:
        sid = node.params["rem_sid"]
        self.symbols.set_radius(sid, rem)
        if rem > 0.0:
            form.terms[sid] = 1.0
        else:
            form.terms.pop(sid, None)
        return form

    def _compute(self, node: Node) -> AffineForm:
        ins = self._input_forms(node)
        p = node.params
        if node.op == "const":
            return core.const(p["value"])
        if node.op == "var":
            return AffineForm((p["lo"] + p["hi"]) / 2.0, {p["sid"]: 1.0})
        if node.op == "add":
            return core.add(ins[0], ins[1])
        if node.op == "sub":
            return core.sub(ins[0], ins[1])
        if node.op == "scale":
            return core.scale(ins[0], p["factor"])
        if node.op == "mul":
            form, rem = core.mul(ins[0], ins[1], self.symbols)
            return self._assign_remainder(node, form, rem)
        if node.op == "recip":
            form, rem = core.recip(ins[0], self.symbols)
            return self._assign_remainder(node, form, rem)
        if node.op == "piecewise":
            pl = PiecewiseLinear(p["breakpoints"], p["values"])
            lo, hi = ins[0].interval(self.symbols)
            lin = pl.linear_on(lo, hi)  # raises PiecewiseDomainError if outside
            if lin is not None:
                slope, intercept = lin
                form = core.scale(ins[0], slope)
                form.center += intercept
                return self._assign_remainder(node, form, 0.0)
            rlo, rhi = pl.range_on(lo, hi)  # scans interior kinks, not just ends
            form = AffineForm((rlo + rhi) / 2.0)
            return self._assign_remainder(node, form, (rhi - rlo) / 2.0)
        raise ValueError(f"unknown op {node.op!r}")

    def evaluate(self, nid: int) -> EvalResult:
        if not 0 <= nid < len(self.nodes):
            raise IndexError(f"node index {nid} out of range")
        errors: list[str] = []
        recomputed: list[int] = []
        complete = True
        for node in self.nodes[: nid + 1]:
            if not node.dirty and node.form is not None:
                continue
            try:
                form = self._compute(node)
            except (IntervalContainsZero, PiecewiseDomainError, _UnavailableInput) as exc:
                errors.append(f"node {node.nid} ({node.op}): {exc}")
                node.form = None
                node.dirty = False
                complete = False
                recomputed.append(node.nid)
                continue
            if self.max_terms is not None and form.num_terms() > self.max_terms:
                errors.append(
                    f"node {node.nid} ({node.op}): noise-symbol budget exceeded "
                    f"({form.num_terms()} terms > budget {self.max_terms})"
                )
                complete = False
            node.form = form
            node.dirty = False
            recomputed.append(node.nid)
        target = self.nodes[nid]
        interval = target.form.interval(self.symbols) if target.form is not None else None
        return EvalResult(nid, target.form, interval, complete, errors, recomputed)

    def evaluate_all(self) -> EvalResult:
        return self.evaluate(len(self.nodes) - 1)

    # ------------------------------------------------------------- incremental
    def set_symbol_radius(self, sid: int, radius: float) -> list[int]:
        """Change one noise bound; returns the dirtied (affected) node ids."""
        self.symbols.set_radius(sid, radius)
        dirtied = []
        for node in self.nodes:
            if node.op == "var" and node.params["sid"] == sid:
                center = (node.params["lo"] + node.params["hi"]) / 2.0
                node.params["lo"] = center - radius
                node.params["hi"] = center + radius
            if sid in node.sym_deps and not node.dirty:
                node.dirty = True
                dirtied.append(node.nid)
        return dirtied

    def interval_of(self, nid: int) -> tuple[float, float]:
        form = self.nodes[nid].form
        if form is None:
            raise ValueError(f"node {nid} has no affine form; evaluate first")
        return form.interval(self.symbols)
