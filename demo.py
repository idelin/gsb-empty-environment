"""Runnable samples for the integer bound-propagation solver."""

from solver import Contradiction, Solver
from checker import check_assignment, enumerate_solutions


def show(title):
    print(f"\n=== {title} ===")


def main():
    show("2x <= 3  ->  x <= 1   (positive coeff, floor rounding)")
    s = Solver()
    s.add_var("x", -10, 10)
    s.add_le({"x": 2}, 3)
    print("domain of x:", s.domain("x"))
    print("explanation:", s.explanations["x"]["detail"])

    show("-2x <= 3  ->  x >= -1  (negative coeff, ceil rounding)")
    s = Solver()
    s.add_var("x", -10, 10)
    s.add_le({"x": -2}, 3)
    print("domain of x:", s.domain("x"))
    print("explanation:", s.explanations["x"]["detail"])

    show("zero coefficient does not touch the domain")
    s = Solver()
    s.add_var("x", -10, 10)
    s.add_var("y", -10, 10)
    s.add_le({"x": 0, "y": 1}, 5)
    print("domain of x:", s.domain("x"), " domain of y:", s.domain("y"))

    show("constant contradiction fails immediately")
    try:
        s = Solver()
        s.add_var("x", -10, 10)
        s.add_le({"x": 0}, -1)  # 0 <= -1 is false
    except Contradiction as e:
        print("Contradiction:", e.explanation)

    show("equality splits into both directions: x + y == 4")
    s = Solver()
    s.add_var("x", 0, 10)
    s.add_var("y", 0, 10)
    s.add_eq({"x": 1, "y": 1}, 4)
    print("domain of x:", s.domain("x"), " domain of y:", s.domain("y"))

    show("congruence catches what interval propagation misses")
    print("2x + 2y + 2z == 3 over [0,1]^3: interval bounds stay [0,1],")
    print("but gcd(2,2,2)=2 does not divide 3 ->")
    try:
        s = Solver()
        for v in "xyz":
            s.add_var(v, 0, 1)
        s.add_eq({"x": 2, "y": 2, "z": 2}, 3)
    except Contradiction as e:
        print("Contradiction:", e.explanation)

    show("...but propagation is still NOT complete (no claim otherwise)")
    print("x + y == 1 and x - y == 0 over [0,1] force 2x == 1:")
    s = Solver()
    s.add_var("x", 0, 1)
    s.add_var("y", 0, 1)
    s.add_eq({"x": 1, "y": 1}, 1)
    s.add_eq({"x": 1, "y": -1}, 0)
    print("after propagate: x =", s.domain("x"), " y =", s.domain("y"),
          " (no contradiction derived, yet no integer solution exists)")
    print("solve() (complete search):", s.solve())
    specs = [({"x": 1, "y": 1}, "==", 1), ({"x": 1, "y": -1}, "==", 0)]
    print("reference enumeration:",
          enumerate_solutions({"x": (0, 1), "y": (0, 1)}, specs))

    show("shared variables: queue propagation to fixpoint")
    s = Solver()
    for v in "xyz":
        s.add_var(v, 0, 10)
    s.add_le({"x": 1, "y": -1}, -1)  # x <= y - 1
    s.add_le({"y": 1, "z": -1}, -1)  # y <= z - 1
    s.add_le({"z": 1}, 5)
    print("x:", s.domain("x"), " y:", s.domain("y"), " z:", s.domain("z"))

    show("retracting a constraint restores domains")
    s = Solver()
    s.add_var("x", 0, 10)
    s.add_var("y", 0, 10)
    s.add_le({"x": 1, "y": 1}, 8)
    cid = s.add_le({"x": 1}, 5)
    print("with x<=5:   x:", s.domain("x"), " y:", s.domain("y"))
    s.retract(cid)
    print("after retract: x:", s.domain("x"), " y:", s.domain("y"))

    show("big integers: no overflow (10^50 coefficients)")
    s = Solver()
    s.add_var("x", -(10**60), 10**60)
    s.add_le({"x": 10**50}, 3 * 10**50 + 7)
    print("x <= 3*10^50+7 with coeff 10^50 ->", s.domain("x"))
    s.add_le({"x": -(10**50)}, 2 * 10**50 + 3)
    print("then -10^50 x <= 2*10^50+3     ->", s.domain("x"))

    show("solve() result verified by the independent checker")
    s = Solver()
    s.add_var("x", -5, 5)
    s.add_var("y", -5, 5)
    s.add_le({"x": 2, "y": 1}, 4)
    s.add_ge({"x": 1, "y": -1}, 0)
    sol = s.solve()
    print("solution:", sol)
    print("independent check:",
          check_assignment(s.active_specs(), sol))


if __name__ == "__main__":
    main()
