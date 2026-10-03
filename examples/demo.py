"""Runnable tour of dualtag: nested AD, mixed derivatives, implicit roots,
non-smooth detection, and budgets. Run: python3 examples/demo.py"""
import os
import sys

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

from dualtag import (Budget, active_tags, compile_expression, derivative,
                     implicit_derivative, jvp, mixed_directional,
                     mixed_partial, second_derivative, solve_scalar)
from dualtag import dual as D
from dualtag.errors import (BudgetExceeded, NonDifferentiableError,
                            NotConvergedError, SingularJacobianError)


def show(title):
    print(f"\n=== {title} ===")


def main():
    show("f(x) = x^3: analytic first/second/mixed-directional derivatives")
    x, u, v = 2.0, 3.0, -0.5
    print(f"f'(2)  = {derivative(lambda t: t**3, x)}   (want 3x^2 = 12)")
    print(f"f''(2) = {second_derivative(lambda t: t**3, x)}   (want 6x = 12)")
    m = mixed_directional(lambda t: t**3, x, u, v)
    print(f"mixed  = {m}   (want 6xuv = {6*x*u*v})")
    mp = mixed_partial(lambda a, b: a**3 * b**2, (2.0, 3.0), 0, 1)
    print(f"d2/dxdy x^3 y^2 at (2,3) = {mp}   (want 6x^2 y = 72)")

    show("Nested differentiation: no perturbation confusion")
    got = derivative(lambda x: x * derivative(lambda y: x + y, 5.0), 3.0)
    print(f"d/dx [x * d/dy(x+y)] = {got}   (want 1; a confused engine says 2)")
    got = derivative(lambda x: x * derivative(lambda y: x * y, 2.0), 3.0)
    print(f"d/dx [x * d/dy(x*y)] = {got}   (want 2x = 6)")
    print(f"active tags after nested calls: {active_tags()}")

    show("Expression interpreter + finite-difference check")
    f = compile_expression("x**3 - 2*sin(x)", ["x"])
    ad = derivative(f, 0.7)
    h = 1e-5
    fd = (f(0.7 + h) - f(0.7 - h)) / (2 * h)
    print(f"AD d/dx(x^3 - 2 sin x) at 0.7 = {ad:.12f}")
    print(f"central finite difference       = {fd:.12f}")

    show("Multi-directional JVP (3 directions in one pass)")
    primal, tangents = jvp(lambda t: t**3, 2.0, (1.0, 2.0, -1.0))
    print(f"primal={primal}, tangents={tangents}   (want 12 * dirs)")

    show("Implicit differentiation: y^2 = x, y > 0")
    F = lambda x, y: y * y - x
    res = solve_scalar(F, 4.0, 1.5)
    print(f"root={res.root:.12f} converged={res.converged} "
          f"iters={res.iterations} residual={res.residual:.2e} "
          f"dF/dy={res.jacobian:.6f}")
    print(f"dy/dx at x=4 = {implicit_derivative(F, 4.0, 1.5)}   (want 1/(2 sqrt 4) = 0.25)")

    show("Singular point x=0: explicit failure, no fake derivative")
    res = solve_scalar(F, 0.0, 1.0)
    print(f"converged={res.converged} singular={res.singular} msg={res.message!r}")
    try:
        implicit_derivative(F, 0.0, 1.0)
    except SingularJacobianError as exc:
        print(f"SingularJacobianError: {exc}")

    show("Non-converged solve: derivative is refused")
    try:
        implicit_derivative(F, 4.0, 1.0e6, max_iter=3)
    except NotConvergedError as exc:
        print(f"NotConvergedError: {exc}")

    show("Non-smooth points are flagged")
    for label, fn in [("abs(x) at 0", lambda: derivative(D.dual_abs, 0.0)),
                      ("branch kink", lambda: derivative(
                          compile_expression("x*x if x >= 0 else -x", ["x"]), 0.0))]:
        try:
            fn()
        except NonDifferentiableError as exc:
            print(f"{label}: NonDifferentiableError: {exc}")

    show("Budgets bound steps and memory")
    try:
        with Budget(max_steps=3):
            derivative(lambda t: (((t * t + t) * t + t) * t + t) * t, 2.0)
    except BudgetExceeded as exc:
        print(f"caught: {exc}")
    try:
        with Budget(max_memory=2):
            jvp(lambda t: t * t, 2.0, (1.0, 1.0, 1.0, 1.0))
    except BudgetExceeded as exc:
        print(f"caught: {exc}")
    print(f"tags still clean after budget aborts: {active_tags() == ()}")
    print(f"engine still works afterwards: d/dx x^2 at 3 = {derivative(lambda t: t*t, 3.0)}")


if __name__ == "__main__":
    main()
