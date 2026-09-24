"""Runnable samples: partial evaluation, domain preservation, certificates,
incremental retraction/GC, binding-order independence.

Run:  python3 demo.py
"""

from fractions import Fraction

from dag import Factory, canon_key, show
from engine import Engine
from interp import run
from parser import parse


def banner(title):
    print("\n" + "=" * 72)
    print(title)
    print("=" * 72)


def show_certs(engine, start=0):
    for c in engine.certificates[start:]:
        print("  cert", c)


def show_journal(engine, start=0):
    for e in engine.journal[start:]:
        print("  journal", e)


# ---------------------------------------------------------------- sample 1
banner("1. Partial evaluation: bound vars fold, unbound nodes stay symbolic")
f = Factory()
root = parse(f, "(x + 0) * 1 + (y - y) + 1/3 + 1/6")
eng = Engine(f, root)
print("expr           :", show(root))
print("no bindings    :", show(eng.result()))
show_certs(eng)
n = len(eng.certificates)
eng.set("x", Fraction(7, 2))
print("after x = 7/2  :", show(eng.result()))
show_certs(eng, n)

# ---------------------------------------------------------------- sample 2
banner("2. Domain preservation: 0 * undefined is NOT 0")
f = Factory()
root = parse(f, "0 * (1 / y)")
eng = Engine(f, root)
print("expr           :", show(root))
print("simplified     :", show(eng.result()), "  <- kept, y may make 1/y undefined")
show_journal(eng)
eng.set("y", 3)
print("after y = 3    :", show(eng.result()))
eng.set("y", 0)
r = eng.result()
print("after y = 0    :", show(r), "  <- undefined, positioned at the div")
print("interpreter on original with y=0:", run(root, {"y": Fraction(0)}))
print("interpreter on simplified with y=0:", run(r, {"y": Fraction(0)}))

f2 = Factory()
root2 = parse(f2, "0 * (y + 1)")
eng2 = Engine(f2, root2)
print("\nexpr           :", show(root2), "(denominator-free, total)")
print("simplified     :", show(eng2.result()), "  <- 0 * total = 0, with certificate")
show_certs(eng2)

# ---------------------------------------------------------------- sample 3
banner("3. Incremental update: stale constant conclusions are retracted")
f = Factory()
root = parse(f, "(x + 1) * 2")
eng = Engine(f, root)
eng.set("x", 2)
print("expr           :", show(root))
print("x = 2          :", show(eng.result()))
j = len(eng.journal)
eng.set("x", 5)
print("x = 5          :", show(eng.result()))
print("events during update x: 2 -> 5:")
show_journal(eng, j)

# ---------------------------------------------------------------- sample 4
banner("4. Conditional branch switch: unused dependencies are removed (GC)")
f = Factory()
root = parse(f, "cond(x, a + 1, b * 2)")
eng = Engine(f, root)
print("expr           :", show(root))
print("no bindings    :", show(eng.result()))
j = len(eng.journal)
eng.set("x", 1)
print("x = 1          :", show(eng.result()))
print("live src nodes :", eng.live())
show_journal(eng, j)
j = len(eng.journal)
eng.set("x", 0)
print("x = 0          :", show(eng.result()))
print("live src nodes :", eng.live())
show_journal(eng, j)

# ---------------------------------------------------------------- sample 5
banner("5. Binding order does not affect the canonical result")
expr = "((x * 2) + (y - y)) * cond(z, x, 3) + 0 * w"
orders = [("x", "y", "z", "w"), ("w", "z", "y", "x"), ("y", "x", "w", "z")]
keys = []
for order in orders:
    f = Factory()
    eng = Engine(f, parse(f, expr))
    for name in order:                       # same final bindings, other order
        eng.set(name, {"x": 4, "y": 9, "z": 0, "w": 7}[name])
    r = eng.result()
    keys.append(canon_key(r))
    print(f"order {order}: {show(r)}")
print("canonical keys identical:", len(set(keys)) == 1)

# ---------------------------------------------------------------- sample 6
banner("6. Error positions agree with the independent interpreter")
f = Factory()
root = parse(f, "(1 / (x - 2)) + (1 / (y - y))")
eng = Engine(f, root)
eng.set("x", 2)
eng.set("y", 9)
s = eng.result()
env = {"x": Fraction(2), "y": Fraction(9)}
print("expr           :", show(root))
print("simplified     :", show(s))
print("original       :", run(root, env))
print("simplified     :", run(s, env))
