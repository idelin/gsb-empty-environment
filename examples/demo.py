"""End-to-end demo of symdag: partial binding, certified simplification,
domain preservation, error localization, incremental updates, and a
differential check against the independent reference interpreter.

Run from the repository root:  python3 examples/demo.py
"""
from __future__ import annotations

import pathlib
import random
import sys
from fractions import Fraction

sys.path.insert(0, str(pathlib.Path(__file__).resolve().parent.parent))

from symdag import Factory, Session, UndefinedError, interpret, simplify_once, to_str
from symdag.gen import DEFAULT_CONSTS, random_spec

C = lambda v: ("const", v)  # noqa: E731
V = lambda n: ("var", n)    # noqa: E731


def show_certs(title, certs):
    print(f"  certificates ({title}):")
    for c in certs:
        print(f"    [{c.rule}] {c.before}  ~>  {c.after}")
        print(f"        domain: {c.domain}")


def main():
    print("=" * 72)
    print("1. Partial binding: unbound nodes are kept, rationals are exact")
    print("=" * 72)
    f = Factory()
    expr = f.parse(("add", ("mul", ("add", V("x"), C(0)), C(1)), ("div", C(1), C(3))))
    result, certs, _ = simplify_once(expr, {"y": 2}, f)
    print(f"  expr      : {to_str(expr)}")
    print(f"  simplified: {to_str(result)}   (y was bound but does not occur)")
    show_certs("one per applied rule", certs)

    print()
    print("=" * 72)
    print("2. Domain preservation: 0 * undefined is NOT 0")
    print("=" * 72)
    f = Factory()
    bad = f.parse(("mul", C(0), ("div", C(1), ("sub", V("x"), V("x")))))
    result, _, _ = simplify_once(bad, factory=f)
    print(f"  expr      : {to_str(bad)}")
    print(f"  simplified: {to_str(result)}   (undefined, never 0)")
    maybe = f.parse(("mul", C(0), ("div", C(1), V("y"))))
    r2, _, s2 = simplify_once(maybe, factory=f)
    print(f"  expr      : {to_str(maybe)}")
    print(f"  simplified: {to_str(r2)}   (kept: y might be 0)")
    for c in s2.suppressed:
        print(f"    [suppressed {c.rule}] {c.before}")
        print(f"        reason: {c.domain}")

    print()
    print("=" * 72)
    print("3. Error localization: undefined results carry the source position")
    print("=" * 72)
    f = Factory()
    expr = f.parse(("add", C(1), ("div", C(1), ("sub", V("x"), C(2)))))
    result, _, _ = simplify_once(expr, {"x": 2}, f)
    print(f"  expr      : {to_str(expr)} with x := 2")
    print(f"  simplified: {to_str(result)}")
    try:
        interpret(expr, {"x": Fraction(2)})
    except UndefinedError as err:
        print(f"  reference interpreter agrees: undefined at path {err.path} ({err.reason})")

    print()
    print("=" * 72)
    print("4. Incremental updates: stale constants retracted, live deps kept")
    print("=" * 72)
    f = Factory()
    expr = f.parse(("mul", ("add", V("x"), C(1)),
                    ("if", V("y"), V("z"), ("mul", V("z"), C(2)))))
    s = Session(f)
    s.set("x", 1)
    s.set("y", 0)
    print(f"  expr: {to_str(expr)}")
    print(f"  x=1, y=0            -> {to_str(s.simplify(expr))}")
    print(f"  root deps: reads={sorted(s.cache[expr].reads)} frees={sorted(s.cache[expr].frees)}")
    s.set("z", 5)
    print(f"  z=5                 -> {to_str(s.simplify(expr))}")
    s.set("y", 3)
    print(f"  y=3 (branch switch) -> {to_str(s.simplify(expr))}")
    print(f"  root deps: reads={sorted(s.cache[expr].reads)} frees={sorted(s.cache[expr].frees)}")
    s.set("x", 10)
    print(f"  x=10                -> {to_str(s.simplify(expr))}")
    print(f"  stats: {dict(s.stats)}")

    print()
    print("=" * 72)
    print("5. Binding order does not change the canonical result")
    print("=" * 72)
    xy = ("mul", V("x"), V("y"))
    frac = ("div", V("z"), ("add", V("x"), C(1)))
    spec = ("add", xy, frac)
    final = {"x": Fraction(3), "y": Fraction(4), "z": Fraction(5)}
    outcomes = []
    for order in (("x", "y", "z"), ("z", "y", "x"), ("y", "z", "x")):
        f = Factory()
        s = Session(f)
        for name in order:
            s.set(name, final[name])
        outcomes.append(s.simplify(s.f.parse(spec)))
    print(f"  orders x,y,z / z,y,x / y,z,x all give: {to_str(outcomes[0])}")
    print(f"  identical canonical keys: {len({n.key for n in outcomes}) == 1}")

    print()
    print("=" * 72)
    print("6. Differential check vs. the independent reference interpreter")
    print("=" * 72)
    rng = random.Random(20260923)
    checked = undefined_seen = 0
    for _ in range(150):
        f = Factory()
        original = f.parse(random_spec(rng, depth=4))
        variables = sorted(f.free_vars(original))
        bindings = {v: rng.choice(DEFAULT_CONSTS) for v in variables if rng.random() < 0.5}
        simplified, _, _ = simplify_once(original, bindings, f)
        for _ in range(20):
            env = dict(bindings)
            for v in variables:
                env.setdefault(v, rng.choice(DEFAULT_CONSTS))
            try:
                expected = ("ok", interpret(original, env))
            except UndefinedError as err:
                expected = ("undef", err.path)
            try:
                got = ("ok", interpret(simplified, env))
            except UndefinedError as err:
                got = ("undef", err.path)
            assert expected == got, (to_str(original), to_str(simplified), env, expected, got)
            checked += 1
            undefined_seen += expected[0] == "undef"
    print(f"  {checked} evaluations compared, {undefined_seen} undefined cases,")
    print("  values AND error positions all match. mismatches: 0")


if __name__ == "__main__":
    main()
