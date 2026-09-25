"""Certificates and an independent verifier.

A certificate records, per DAG node, the op, explicit inputs and params, and
the claimed affine form (center + sparse coeffs) with its interval.  The
verifier replays the recorded structure from scratch using the *dense*
implementation (explicit sizes and indices, no NumPy) and the public
remainder bounds from ``errordag.bounds``; it does not trust any recorded
remainder -- every delta is recomputed and every fresh symbol index must
follow the sequential allocation discipline.
"""

from dataclasses import dataclass, field
from enum import Enum

from . import bounds
from .dense import DenseAffineForm

TOL = 1e-9


class CertStatus(Enum):
    COMPLETE = "complete"
    INCOMPLETE = "incomplete"


@dataclass
class NodeRecord:
    nid: int
    op: str
    inputs: tuple
    params: dict
    center: float
    coeffs: dict
    lo: float
    hi: float


@dataclass
class Certificate:
    version: int
    budget: object          # int or None
    records: list
    outputs: list
    status: CertStatus
    reason: str = None

    @property
    def node_ids(self):
        return {r.nid for r in self.records}


@dataclass
class VerificationResult:
    ok: bool
    errors: list = field(default_factory=list)


def _close(a, b):
    return abs(a - b) <= TOL * (1.0 + abs(a) + abs(b))


class _Replay:
    """Independent re-derivation of every recorded affine form (dense).

    Freshness discipline: every newly introduced noise symbol (leaf variable
    or nonlinear remainder) must use an index strictly greater than any
    symbol allocated earlier in replay order and unused by prior forms.
    """

    def __init__(self, size):
        self.size = size
        self.forms = {}
        self.last_symbol = -1
        self.errors = []

    def _check_fresh(self, rec, sid, what):
        if not 0 <= sid < self.size:
            self.errors.append(
                f"node {rec.nid}: {what} symbol {sid} outside declared size "
                f"{self.size}"
            )
            return False
        if sid <= self.last_symbol:
            self.errors.append(
                f"node {rec.nid}: {what} symbol {sid} breaks allocation order"
            )
            return False
        for nid, prev in self.forms.items():
            if sid < prev.size and prev.coeffs[sid] != 0.0:
                self.errors.append(
                    f"node {rec.nid}: {what} symbol {sid} already used by node {nid}"
                )
                return False
        self.last_symbol = sid
        return True

    def _place_remainder(self, rec, out, delta, what):
        """Place an independently recomputed remainder on its fresh symbol.

        The symbol index is taken from the recorded coeffs (the one extra
        index beyond the linear part) and must satisfy freshness.
        """
        if delta <= 0.0:
            return out
        tol = TOL * (1.0 + abs(delta))
        candidates = [
            i for i, c in rec.coeffs.items()
            if 0 <= i < self.size and out.coeffs[i] == 0.0 and abs(c - delta) <= tol
        ]
        if len(candidates) != 1:
            self.errors.append(
                f"node {rec.nid}: {what} remainder {delta} not carried by a "
                f"unique recorded symbol (candidates: {candidates})"
            )
            return out
        sid = candidates[0]
        if self._check_fresh(rec, sid, what):
            out.coeffs[sid] = delta
        return out

    def replay(self, rec):
        op = rec.op
        ins = [self.forms[i] for i in rec.inputs]
        if op == "var":
            lo, hi, sid = rec.params["lo"], rec.params["hi"], rec.params["symbol"]
            out = DenseAffineForm(self.size, (lo + hi) / 2.0)
            if self._check_fresh(rec, sid, "var"):
                out.coeffs[sid] = (hi - lo) / 2.0
            return out
        if op == "const":
            return DenseAffineForm(self.size, rec.params["value"])
        if op == "add":
            return ins[0].add(ins[1])
        if op == "sub":
            return ins[0].add(ins[1], -1.0)
        if op == "scale":
            return ins[0].scaled(rec.params["factor"])
        if op == "mul":
            return self._replay_mul(rec, ins[0], ins[1])
        if op == "recip":
            alpha, zeta, delta = bounds.reciprocal_envelope(*ins[0].interval())
            return self._apply_envelope(rec, ins[0], alpha, zeta, delta)
        if op == "piecewise":
            p = rec.params
            alpha, zeta, delta = bounds.piecewise_linear_envelope(
                p["breakpoints"], p["values"], p["left_slope"],
                p["right_slope"], *ins[0].interval())
            return self._apply_envelope(rec, ins[0], alpha, zeta, delta)
        self.errors.append(f"node {rec.nid}: unknown op {op!r}")
        return DenseAffineForm(self.size)

    def _apply_envelope(self, rec, form, alpha, zeta, delta):
        out = form.scaled(alpha).shifted(zeta)
        return self._place_remainder(rec, out, delta, "envelope")

    def _replay_mul(self, rec, a, b):
        out = DenseAffineForm(
            self.size, a.center * b.center,
            [a.coeffs[i] * b.center + b.coeffs[i] * a.center
             for i in range(self.size)],
        )
        rem = bounds.multiplication_remainder(a.radius(), b.radius())
        return self._place_remainder(rec, out, rem, "multiplication")


def verify(cert, dag=None):
    """Independently verify a certificate.  Returns VerificationResult."""
    errors = []
    if cert.status is not CertStatus.COMPLETE:
        return VerificationResult(False, [f"certificate incomplete: {cert.reason}"])
    if dag is not None and cert.version != dag.version:
        errors.append(
            f"stale certificate: version {cert.version} != dag version {dag.version}"
        )

    # Explicit size derived from recorded indices.
    size = 0
    for rec in cert.records:
        for idx in rec.coeffs:
            size = max(size, idx + 1)
        if rec.op == "var":
            size = max(size, rec.params["symbol"] + 1)
    if cert.budget is not None and size > cert.budget:
        errors.append(f"symbol budget exceeded: {size} > {cert.budget}")

    replay = _Replay(size)
    for rec in cert.records:
        for i in rec.inputs:
            if i not in replay.forms:
                errors.append(f"node {rec.nid}: input {i} missing or forward reference")
        if any(i not in replay.forms for i in rec.inputs):
            continue
        try:
            form = replay.replay(rec)
        except (bounds.DivisionAcrossZero, ValueError, IndexError) as exc:
            errors.append(f"node {rec.nid} ({rec.op}): {exc}")
            continue
        replay.forms[rec.nid] = form

        if not _close(form.center, rec.center):
            errors.append(
                f"node {rec.nid}: center {rec.center} != recomputed {form.center}"
            )
        for idx in set(rec.coeffs) | set(range(size)):
            claimed = rec.coeffs.get(idx, 0.0)
            actual = form.coeffs[idx] if idx < form.size else 0.0
            if not _close(claimed, actual):
                errors.append(
                    f"node {rec.nid}: coeff[{idx}] {claimed} != recomputed {actual}"
                )
        lo, hi = form.interval()
        if rec.lo > lo + TOL * (1 + abs(lo)) or rec.hi < hi - TOL * (1 + abs(hi)):
            errors.append(
                f"node {rec.nid}: recorded interval [{rec.lo}, {rec.hi}] does not "
                f"contain recomputed [{lo}, {hi}]"
            )
        if rec.op == "recip":
            src = replay.forms[rec.inputs[-1]]
            s_lo, s_hi = src.interval()
            if s_lo <= 0.0 <= s_hi:
                errors.append(f"node {rec.nid}: divisor interval contains 0")

    errors.extend(replay.errors)
    for out in cert.outputs:
        if out not in replay.forms:
            errors.append(f"output node {out} not covered by certificate")
    return VerificationResult(not errors, errors)
