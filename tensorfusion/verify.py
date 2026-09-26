"""Consistency verification between unfused and fused execution.

compare_tensors distinguishes three verdicts and never upgrades a merely
close result to "bitwise identical":
  BITWISE  - identical IEEE-754 bit patterns in every element;
  CLOSE    - numerically close but bit patterns differ (e.g. a reduction
             whose accumulation order was changed);
  MISMATCH - values differ beyond tolerance.

verify_gradients checks forward values and reverse-mode gradients before
and after fusion, and additionally validates the gradients against an
independent central finite-difference estimate.
"""

from __future__ import annotations

import math
import struct
from dataclasses import dataclass
from enum import Enum

from .autodiff import backward
from .interp import run_fused, run_unfused


class Verdict(Enum):
    BITWISE = "bitwise identical"
    CLOSE = "numerically close (NOT bitwise identical)"
    MISMATCH = "mismatch"


@dataclass
class Comparison:
    verdict: Verdict
    max_abs: float
    first_index: tuple | None
    note: str = ""

    def __str__(self):
        where = f" first diff at {self.first_index}," if self.first_index else ""
        return f"{self.verdict.value}:{where} max|diff|={self.max_abs:.3e} {self.note}".strip()


def _bits(value):
    return struct.pack(">d", value)


def compare_tensors(a, b, rtol=1e-9, atol=1e-12):
    if a.shape != b.shape:
        return Comparison(Verdict.MISMATCH, math.inf, None,
                          f"shape {a.shape} vs {b.shape}")
    bitwise = True
    close = True
    max_abs = 0.0
    first = None
    for idx in a.indices():
        x, y = a.get(idx), b.get(idx)
        if _bits(x) != _bits(y):
            bitwise = False
            if first is None:
                first = idx
        if x == y or (math.isnan(x) and math.isnan(y)):
            continue
        diff = abs(x - y)
        if math.isnan(diff):
            close = False
            continue
        max_abs = max(max_abs, diff)
        if diff > atol + rtol * abs(y):
            close = False
    if bitwise:
        return Comparison(Verdict.BITWISE, 0.0, None)
    if close:
        return Comparison(Verdict.CLOSE, max_abs, first,
                          "bit patterns differ; not claiming bitwise equality")
    return Comparison(Verdict.MISMATCH, max_abs, first)


@dataclass
class GradientReport:
    forward: dict
    gradients: dict
    finite_difference: dict

    def ok(self):
        return (all(c.verdict is Verdict.BITWISE for c in self.forward.values())
                and all(c.verdict is Verdict.BITWISE for c in self.gradients.values())
                and all(ok for ok, _ in self.finite_difference.values()))


def _sum_of(values, name):
    tensor = values[name]
    total = 0.0
    for value in tensor.to_flat():
        total += value
    return total


def finite_difference(program, env, wrt, seed_var, h=1e-6, tol=1e-4):
    """Independent numerical check of the hand-rolled gradients."""
    values, _ = run_unfused(program, env)
    analytic = backward(program, values, seed_var)
    report = {}
    for var in wrt:
        base = env[var]
        max_err = 0.0
        for idx in base.indices():
            original = base.get(idx)
            base._write(idx, original + h)
            plus = _sum_of(run_unfused(program, env)[0], seed_var)
            base._write(idx, original - h)
            minus = _sum_of(run_unfused(program, env)[0], seed_var)
            base._write(idx, original)
            fd = (plus - minus) / (2.0 * h)
            got = analytic[var].get(idx) if var in analytic else 0.0
            max_err = max(max_err, abs(fd - got))
        report[var] = (max_err <= tol, max_err)
    return report


def verify_gradients(program, env, wrt, seed_var):
    values_u, _ = run_unfused(program, env)
    values_f, _ = run_fused(program, env, trace=True)
    forward = {}
    for name, tensor in values_u.items():
        if name in env:
            continue
        if name in values_f:
            forward[name] = compare_tensors(tensor, values_f[name])
    grads_u = backward(program, values_u, seed_var)
    grads_f = backward(program, values_f, seed_var)
    gradients = {var: compare_tensors(grads_u[var], grads_f[var]) for var in wrt}
    fd = finite_difference(program, env, wrt, seed_var)
    return GradientReport(forward=forward, gradients=gradients, finite_difference=fd)
