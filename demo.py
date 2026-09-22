"""Demo: parsing, interpreting, error recovery and incremental re-parsing."""
import minilang as ml


def section(title):
    print("\n" + "=" * 60)
    print(title)
    print("=" * 60)


# --- 1. Parse and interpret -------------------------------------------------
section("1. Parse and interpret")
src = """\
# factorial, recursively
fn fact(n) = if n < 2 then 1 else n * fact(n - 1)
fn square(x) = x * x
let result = fact(5) + square(3)
"""
tree = ml.parse(src)
print("source:")
print(src)
print("syntax tree (spans are UTF-8 byte ranges):")
print(ml.dump(tree))
env = ml.interpret(tree)
print("result =", env.get("result"))
print("fact(10) =", ml.evaluate_expression("fact(10)", env))

# --- 2. Error recovery ------------------------------------------------------
section("2. Error recovery: unclosed paren, explicit error nodes")
broken = "fn f(x) = (x + 1\nfn g(y) = y * 2\n"
tree = ml.parse(broken)
print("source:")
print(broken)
print(ml.dump(tree))
for e in ml.collect_errors(tree):
    print(f"error: {e.message} at bytes [{e.start}, {e.end})")
print("-> the declaration after the unclosed paren still parsed:",
      tree.decls[1].name, "with body", type(tree.decls[1].body).__name__)

# --- 3. Incremental re-parsing ----------------------------------------------
section("3. Incremental edit: change a number inside one function")
src = "fn a(x) = x * 2\nfn b(y) = y + 10\nfn c(z) = z - 1\n"
session = ml.Session(src)
old_a, old_b, old_c = session.tree.decls
old_c_span = old_c.span
print("source:")
print(src)
pos = src.index("10")
tree = session.edit(pos, pos + 2, "200")  # b: y + 10  ->  y + 200
print("after edit `10` -> `200` in fn b:")
print(session.src.decode())
print("fn a node reused (same object):", tree.decls[0] is old_a)
print("fn c node reused (same object):", tree.decls[2] is old_c)
print("fn b rebuilt:", tree.decls[1] is not old_b)
print("unchanged `y` inside fn b reused:", tree.decls[1].body.left is old_b.body.left)
print(f"fn c span shifted by edit delta: {old_c_span} -> {tree.decls[2].span}")
print("incremental tree == full re-parse:", tree == ml.parse(session.src))

# --- 4. Fix the broken source incrementally, then run it --------------------
section("4. Edit broken source until it runs")
session = ml.Session(broken)
try:
    ml.interpret(session.tree)
except ml.LanguageError as e:
    print("interpret refused:", e)
pos = broken.index("\nfn g")
tree = session.edit(pos, pos, ")")  # close the paren
print("inserted `)`, errors now:", ml.collect_errors(tree))
env = ml.interpret(tree)
print("g(21) =", ml.evaluate_expression("g(21)", env))
print("incremental tree == full re-parse:", tree == ml.parse(session.src))

# --- 5. UTF-8 byte offsets ---------------------------------------------------
section("5. UTF-8 byte positions")
src = "# 注释\nfn f() = 1\n"
tree = ml.parse(src)
f = tree.decls[0]
print(f"source: {src!r}")
print(f"fn f starts at byte {f.start} (after the 9-byte comment line)")
session = ml.Session(src)
try:
    session.edit(3, 4, "x")
except ValueError as e:
    print("edit cutting a multi-byte character is rejected:", e)
