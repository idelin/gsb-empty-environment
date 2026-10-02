"""Runnable tour of dualtag:  python3 examples.py

Every section prints the AD result next to an independent reference
(finite differences, symbolic differentiation, or bisection), so the
output doubles as a demonstration and a sanity check.
"""
import math

import dualtag as dt
from dualtag import expr, numeric, symbolic


def section(title):
    print("\n" + "=" * 68)
    print(title)
    print("=" * 68)


def main():
    section("1. f(x) = x^3: first and second derivatives at x = 2")
    value, d1, d2 = dt.second_derivative(lambda x: x ** 3, 2.0)
    print(f"  AD:        f={value}, f'={d1}, f''={d2}   (analytic: 8, 3x^2=12, 6x=12)")
    _ast = expr.parse('x^3')
    _d1 = symbolic.diff_source(_ast, 'x')
    _d2 = symbolic.diff_source(expr.parse(_d1), 'x')
    print(f"  symbolic:  f' = {_d1},  f'' = {_d2}")
    print(f"  finite diff: f'={numeric.fd_derivative(lambda x: x**3, 2.0):.8f},"
          f" f''={numeric.fd_second(lambda x: x**3, 2.0):.8f}")

    section("2. Expression interpreter + multi-direction JVP (one pass)")
    ast = expr.parse("x^3 * y + x * sin(y)")
    fn = lambda v: expr.evaluate(ast, {"x": v[0], "y": v[1]})
    x = [1.5, 0.7]
    _, push = dt.jvp(lambda v: [fn(v)], x, [[1.0, 0.0], [0.0, 1.0], [2.0, -1.0]])
    print(f"  g(x,y) = x^3 y + x sin(y) at {x}")
    print(f"  push [1,0] -> {push[0]},  push [0,1] -> {push[1]},  push [2,-1] -> {push[2]}")

    section("3. Hessian and mixed partials vs symbolic reference")
    value, grad, H = dt.hessian(fn, x)
    sym_xy = symbolic.diff_source(ast, "x")
    sym_xy = symbolic.to_source(symbolic.simplify(
        symbolic.diff(expr.parse(sym_xy), "y")))
    print(f"  grad = {[round(g, 10) for g in grad]}")
    print(f"  H    = [[{H[0][0]:.10f}, {H[0][1]:.10f}],")
    print(f"          [{H[1][0]:.10f}, {H[1][1]:.10f}]]")
    print(f"  d2g/dxdy analytic = 3x^2 + cos(y) = {3*x[0]**2 + math.cos(x[1]):.10f}"
          f"   (symbolic: {sym_xy})")
    print(f"  finite-diff mixed = {numeric.fd_mixed(fn, x, 0, 1):.10f}")

    section("4. Nested differentiation: no perturbation confusion")
    # d/dx [ x * d/dy (x*y) ] = 2x ; the inner derivative references the
    # outer, still-perturbed x.  Tag isolation keeps them apart.
    def outer(v):
        _, d = dt.derivative(lambda y: v[0] * y, 1.0)
        return v[0] * d
    _, val = dt.derivative(lambda z: outer([z]), 3.0)
    print(f"  d/dx [ x * d/dy (x*y) ] at x=3 -> {dt.primal_value(val)}  (correct: 2x = 6)")
    _, val2 = dt.derivative(lambda z: dt.derivative(lambda y: z + y, 0.0)[1] * z, 5.0)
    print(f"  d/dx [ x * d/dy (x+y) ] at x=5 -> {dt.primal_value(val2)}  (correct: 1)")

    section("5. Implicit differentiation: y^2 = x, y > 0")
    residual = lambda p, y: [y[0] ** 2 - p[0]]
    dydx, result = dt.implicit_jvp(residual, [4.0], [1.0], [[1.0]])
    print(f"  x=4: y*={result.y[0]:.12f}  converged={result.converged}"
          f" in {result.iterations} iters, residual={result.residual_norm:.2e}")
    print(f"  dy/dx = {dydx[0][0]:.12f}  (analytic 1/(2y) = 0.25)")
    root = lambda x: numeric.bisect_root(lambda y: y * y - x, 1e-12, 1e6)
    print(f"  independent check (bisection+FD): {numeric.fd_derivative(root, 4.0):.10f}")

    print("\n  x=0 (singular: J_y = 2y* = 0):")
    try:
        dt.implicit_jvp(residual, [0.0], [1.0], [[1.0]])
        print("  ERROR: should have failed!")
    except dt.SingularJacobianError as e:
        print(f"  explicit failure -> SingularJacobianError: {e}")

    print("\n  unconverged solve (max_iter=1) refuses to differentiate:")
    try:
        dt.implicit_jvp(residual, [4.0], [100.0], [[1.0]], max_iter=1)
        print("  ERROR: should have failed!")
    except dt.NonConvergenceError as e:
        print(f"  explicit failure -> NonConvergenceError: {e}")

    section("6. Non-smooth branches are flagged, not silently wrong")
    try:
        dt.derivative(dt.abs_, 0.0)
    except dt.NonDifferentiableError as e:
        print(f"  d/dx |x| at 0 -> NonDifferentiableError: {e}")
    kink = lambda p, y: [y[0] - dt.abs_(p[0])]
    try:
        dt.implicit_jvp(kink, [0.0], [0.5], [[1.0]])
    except dt.NonDifferentiableError as e:
        print(f"  implicit y = |x| at x=0 -> NonDifferentiableError: {e}")
    d, _ = dt.implicit_jvp(kink, [2.0], [0.5], [[1.0]])
    print(f"  implicit y = |x| at x=2 (smooth side): dy/dx = {d[0][0]}")

    section("7. Budgets and cancellation safety")
    try:
        dt.derivative(lambda x: x ** 3, 2.0,
                      budget=dt.Budget(max_steps=0, max_allocs=10**9))
    except dt.BudgetExceeded as e:
        print(f"  tiny budget -> BudgetExceeded: {e}")
    # The cancelled run leaked nothing: fresh results are still exact.
    _, d = dt.derivative(lambda x: x ** 3, 2.0)
    print(f"  after cancellation, d/dx x^3 at 2 = {d} (state clean)")

    print("\nAll demo sections completed.")


if __name__ == "__main__":
    main()
