"""Error-propagation DAG with shared noise symbols and incremental updates.

Each node caches a sparse AffineForm.  Linear steps (add/sub/scale) are exact.
Nonlinear steps (mul, recip, div, piecewise) use the public conservative
remainder bounds from ``errordag.bounds`` and fold the remainder into a fresh
shared noise symbol, so correlations through later linear combinations are
preserved.

Incremental changes: ``update_var`` re-binds one leaf's interval and marks
only its transitive dependents dirty; ``evaluate`` recomputes exactly those.
Certificates issued earlier that cover affected nodes are downgraded to
INCOMPLETE.  If the noise-symbol budget is exceeded mid-evaluation, the
evaluation stops and any certificate produced is INCOMPLETE.
"""

from . import bounds
from .affine import AffineForm, BudgetExceeded, NoiseSpace
from .certificate import Certificate, CertStatus, NodeRecord


class Node:
    __slots__ = ("nid", "op", "inputs", "params", "name", "form", "dirty",
                 "dependents")

    def __init__(self, nid, op, inputs, params, name):
        self.nid = nid
        self.op = op
        self.inputs = tuple(inputs)
        self.params = dict(params)
        self.name = name
        self.form = None
        self.dirty = True
        self.dependents = []

    def interval(self):
        return self.form.interval()

    def __repr__(self):
        return f"Node({self.nid}, {self.op!r}, name={self.name!r})"


class DAG:
    def __init__(self, max_symbols=None):
        self.space = NoiseSpace(max_symbols)
        self.nodes = []
        self.version = 0
        self.recomputed = []   # node ids recomputed during the last evaluate()
        self.error = None      # exception that aborted the last evaluate()
        self._issued = []

    # ------------------------------------------------------------------ build
    def _node(self, op, inputs, params, name=None):
        node = Node(len(self.nodes), op, inputs, params, name)
        for i in node.inputs:
            self.nodes[i].dependents.append(node.nid)
        self.nodes.append(node)
        return node

    def var(self, lo, hi, name=None):
        if hi < lo:
            raise ValueError(f"empty interval [{lo}, {hi}]")
        sid = self.space.fresh(name)
        return self._node("var", [], {"lo": float(lo), "hi": float(hi),
                                      "symbol": sid}, name)

    def const(self, value):
        return self._node("const", [], {"value": float(value)})

    def add(self, a, b):
        return self._node("add", [a.nid, b.nid], {})

    def sub(self, a, b):
        return self._node("sub", [a.nid, b.nid], {})

    def scale(self, a, factor):
        return self._node("scale", [a.nid], {"factor": float(factor)})

    def mul(self, a, b):
        return self._node("mul", [a.nid, b.nid], {})

    def recip(self, a):
        return self._node("recip", [a.nid], {})

    def div(self, a, b):
        """a / b, decomposed as a * recip(b) so each node carries one step."""
        r = self._node("recip", [b.nid], {})
        return self._node("mul", [a.nid, r.nid], {})

    def piecewise(self, a, breakpoints, values, left_slope, right_slope,
                  name=None):
        params = {
            "breakpoints": [float(v) for v in breakpoints],
            "values": [float(v) for v in values],
            "left_slope": float(left_slope),
            "right_slope": float(right_slope),
        }
        return self._node("piecewise", [a.nid], params, name)

    def abs(self, a, name=None):
        return self.piecewise(a, [0.0], [0.0], -1.0, 1.0, name or "abs")

    def relu(self, a, name=None):
        return self.piecewise(a, [0.0], [0.0], 0.0, 1.0, name or "relu")

    # -------------------------------------------------------------- evaluate
    def evaluate(self):
        """Recompute dirty nodes in topological (creation) order.

        Returns True on success; returns False and records ``self.error`` if
        the noise budget is exceeded or a divisor interval contains 0.
        """
        self.recomputed = []
        self.error = None
        try:
            for node in self.nodes:
                if node.dirty:
                    self._compute(node)
                    node.dirty = False
                    self.recomputed.append(node.nid)
        except (BudgetExceeded, bounds.DivisionAcrossZero) as exc:
            self.error = exc
            return False
        return True

    def _form(self, nid):
        return self.nodes[nid].form

    def _apply_envelope(self, form, alpha, zeta, delta, tag):
        out = form.scaled(alpha).shifted(zeta)
        if delta > 0.0:
            sid = self.space.fresh(tag)
            out = AffineForm(out.center, {**out.coeffs, sid: delta})
        return out

    def _mul_forms(self, a, b):
        center = a.center * b.center
        coeffs = {}
        for idx, coeff in a.coeffs.items():
            coeffs[idx] = coeffs.get(idx, 0.0) + coeff * b.center
        for idx, coeff in b.coeffs.items():
            coeffs[idx] = coeffs.get(idx, 0.0) + coeff * a.center
        rem = bounds.multiplication_remainder(a.radius(), b.radius())
        if rem > 0.0:
            coeffs[self.space.fresh("mul")] = rem
        return AffineForm(center, coeffs)

    def _compute(self, node):
        p = node.params
        if node.op == "var":
            center = (p["lo"] + p["hi"]) / 2.0
            radius = (p["hi"] - p["lo"]) / 2.0
            node.form = AffineForm(center, {p["symbol"]: radius})
        elif node.op == "const":
            node.form = AffineForm(p["value"])
        elif node.op == "add":
            node.form = AffineForm.add(self._form(node.inputs[0]),
                                       self._form(node.inputs[1]))
        elif node.op == "sub":
            node.form = AffineForm.add(self._form(node.inputs[0]),
                                       self._form(node.inputs[1]), -1.0)
        elif node.op == "scale":
            node.form = self._form(node.inputs[0]).scaled(p["factor"])
        elif node.op == "mul":
            node.form = self._mul_forms(self._form(node.inputs[0]),
                                        self._form(node.inputs[1]))
        elif node.op == "recip":
            src = self._form(node.inputs[0])
            alpha, zeta, delta = bounds.reciprocal_envelope(*src.interval())
            node.form = self._apply_envelope(src, alpha, zeta, delta, "recip")
        elif node.op == "piecewise":
            src = self._form(node.inputs[0])
            alpha, zeta, delta = bounds.piecewise_linear_envelope(
                p["breakpoints"], p["values"], p["left_slope"],
                p["right_slope"], *src.interval())
            node.form = self._apply_envelope(src, alpha, zeta, delta, "pw")
        else:
            raise ValueError(f"unknown op {node.op!r}")

    # ------------------------------------------------------------ incremental
    def update_var(self, node, lo, hi):
        """Re-bind one leaf's noise interval; dirty only its dependents.

        Returns the set of affected node ids.  Previously issued COMPLETE
        certificates covering any affected node are downgraded to INCOMPLETE.
        """
        if node.op != "var":
            raise ValueError("update_var only applies to var nodes")
        if hi < lo:
            raise ValueError(f"empty interval [{lo}, {hi}]")
        node.params["lo"], node.params["hi"] = float(lo), float(hi)
        affected = self._mark_dirty(node.nid)
        self.version += 1
        for cert in self._issued:
            if cert.status is CertStatus.COMPLETE and affected & cert.node_ids:
                cert.status = CertStatus.INCOMPLETE
                cert.reason = (f"stale after incremental update of node "
                               f"{node.nid} (dag version {self.version})")
        return affected

    def _mark_dirty(self, nid):
        affected = set()
        stack = [nid]
        while stack:
            cur = stack.pop()
            if cur in affected:
                continue
            affected.add(cur)
            self.nodes[cur].dirty = True
            stack.extend(self.nodes[cur].dependents)
        return affected

    # ------------------------------------------------------------ certificate
    def certificate(self, outputs):
        """Issue a certificate for ``outputs`` (nodes or ids).

        COMPLETE only if every ancestor was evaluated without error; budget
        overflow or a zero-crossing divisor yields an INCOMPLETE certificate
        carrying the reason.
        """
        out_ids = [o.nid if isinstance(o, Node) else int(o) for o in outputs]
        needed = set()
        stack = list(out_ids)
        while stack:
            cur = stack.pop()
            if cur in needed:
                continue
            needed.add(cur)
            stack.extend(self.nodes[cur].inputs)

        records = []
        missing = []
        for node in self.nodes:
            if node.nid not in needed:
                continue
            if node.form is None:
                missing.append(node.nid)
                continue
            lo, hi = node.form.interval()
            records.append(NodeRecord(
                nid=node.nid, op=node.op, inputs=node.inputs,
                params=dict(node.params), center=node.form.center,
                coeffs=dict(node.form.coeffs), lo=lo, hi=hi,
            ))

        reason = None
        if self.error is not None:
            reason = f"evaluation aborted: {self.error}"
        if missing:
            reason = (reason + "; " if reason else "") + \
                     f"nodes never evaluated: {missing}"
        status = CertStatus.COMPLETE if reason is None else CertStatus.INCOMPLETE
        cert = Certificate(
            version=self.version, budget=self.space.max_symbols,
            records=records, outputs=out_ids, status=status, reason=reason,
        )
        self._issued.append(cert)
        return cert
