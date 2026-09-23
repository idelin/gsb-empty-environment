"""Runnable showcase of the interval_arith library (Python 3.11, stdlib only)."""
import os
import sys
from fractions import Fraction as F

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

from interval_arith import (
    AffineForm, Budget, Certificate, DivisionByZeroInterval, Interval,
    NoiseRegistry, analyze, dependency_note, find_counterexample,
)

line = lambda t: print(f"\n=== {t} ===")

line("1. square is tight; plain product is conservative (dependency noted)")
x = Interval(-1, 1)
print("[-1,1].square()      =", x.square())
print("[-1,1] * [-1,1]      =", x * x)
print("note:", dependency_note(x, x))

line("2. affine form keeps the correlation of x*x")
reg = NoiseRegistry()
cert = Certificate("range of x*x for x in [-1,1]")
ax = AffineForm.from_interval(x, reg, cert=cert)
print("affine x*x via mul   =", ax.mul(ax, reg).to_interval(), "(conservative)")
print("affine x.square()    =", ax.square(reg, cert=cert).to_interval(), "(tight)")
print(cert.render())

line("3. division across 0: error, then split into a discontinuous domain")
try:
    Interval(1, 2) / Interval(-1, 2)
except DivisionByZeroInterval as e:
    print("raised:", e)
for sub, img in Interval(1, 2).div_split(Interval(-1, 2)):
    print(f"  divisor on {sub}  ->  quotient {img}")

line("4. monotonic functions on exact rationals")
print("x^3 on [-2,3]        =", Interval(-2, 3).cube())
print("1/x on [1,2]         =", Interval(1, 2).reciprocal())

line("5. singularity split + budget exhaustion keeps unanalyzed pieces")
res = analyze(lambda iv: iv.reciprocal(), Interval(-2, 3), [0], Budget(1), cert=None)
for sub, img in res.pieces:
    print(f"  analyzed   {sub} -> {img}")
for sub in res.unanalyzed:
    print(f"  UNANALYZED {sub} (budget exhausted, kept for later)")

line("6. sampling falsifies; the certificate proves")
bad = find_counterexample(lambda t: t * t, x, Interval(0, F(1, 2)), n=8)
print("claimed bound [0,1/2] for x^2: counterexample at", bad)
good = find_counterexample(lambda t: t * t, x, Interval(0, 1), n=8)
print("claimed bound [0,1]  : no counterexample on grid (NOT a proof):", good)
cert2 = Certificate("x^2 on [-1,1] lies in [0,1]")
print("certificate verify   =", cert2.verify(lambda iv: iv.square(), x, Interval(0, 1)))
print(cert2.render())

line("7. conservative outward rounding")
iv = Interval(F(1, 3), F(2, 3))
print("[1/3, 2/3] rounded outward to grid 1/10 =", iv.round_outward(F(1, 10)))
