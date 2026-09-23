"""Run samples for interval_arith (Python 3.11, stdlib only)."""

from fractions import Fraction as F

from interval_arith import (
    Interval, divide, Certificate, certify_range,
    var, const, Add, Sub, Mul, Square,
    analyze, find_counterexample,
)

line = lambda t: print(f"\n=== {t} ===")

line("1. [-1, 1]^2 = [0, 1] (square is exact)")
x = Interval(-1, 1)
print("square:      ", x.square())
print("naive x*x:   ", x * x, " (conservative: treats factors as independent)")
env = {"x": x}
print("affine x*x:  ", Mul(var("x"), var("x")).affine(env).range(),
      " (correlation tracked: e^2 in [0, 1])")

line("2. Division by an interval straddling 0 -> disconnected domain")
print("1 / [-1, 2] =", divide(Interval.point(1), Interval(-1, 2)))
print("1 / [0, 2]  =", divide(Interval.point(1), Interval(0, 2)))
print("1 / [0, 0]  =", divide(Interval.point(1), Interval(0, 0)))

line("3. Monotone function (cube, exact)")
print("[-2, 3]^3 =", Interval(-2, 3).apply_monotone(lambda v: v ** 3))

line("4. Conservative outward rounding when importing floats")
print("from_float(0.1) =", Interval.from_float(0.1))
print("from_decimal('0.1') =", Interval.from_decimal("0.1"), "(exact)")

line("5. Affine correlation: x - x and x*(x+1)")
print("x - x, affine:  ", Sub(var("x"), var("x")).affine(env).range())
print("x - x, interval:", Sub(var("x"), var("x")).interval(env))
env01 = {"x": Interval(0, 1)}
expr = Mul(var("x"), Add(var("x"), const(1)))
print("x*(x+1) on [0,1], affine:  ", expr.affine(env01).range())
print("x*(x+1) on [0,1], interval:", expr.interval(env01))

line("6. Certificate: every step states its applicable domain")
cert = Certificate()
form = Mul(var("x"), var("x")).affine(env, cert)
r = certify_range(form, Interval(0, 1), cert)
print(cert.explain())
print("certified range:", r, "| verify():", cert.verify())

line("7. Splitting at the singularity of 1/x, with a budget")
res = analyze(lambda p: divide(Interval.point(1), p), Interval(-2, 3), [F(0)])
for piece, out in res.analyzed:
    print(f"  1/x on {piece}: {out.domain}")
res2 = analyze(lambda p: divide(Interval.point(1), p), Interval(-2, 3), [F(0)],
               budget=1)
print("budget=1 -> analyzed:", [str(p) for p, _ in res2.analyzed],
      "| unanalyzed (kept):", [str(p) for p in res2.unanalyzed])

line("8. Rational sampling: counterexamples only, never a proof")
bad = Interval(F(-1, 2), F(1, 2))
ce = find_counterexample(lambda v: v * v, Interval(-1, 1), bad)
print(f"claimed {bad} for x^2 on [-1,1]: counterexample x = {ce}, "
      f"x^2 = {ce * ce}")
good = Interval(0, 1)
ce2 = find_counterexample(lambda v: v * v, Interval(-1, 1), good)
print(f"claimed {good}: no counterexample found ({ce2}) "
      "-> NOT a proof; inclusion is proved by the certificate in (6)")
