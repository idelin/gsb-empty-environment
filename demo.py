"""Deterministic demo: exact bounded sampling on a loopy, ambiguous grammar.

Run:  python3.11 demo.py
"""

import random
import sys
from fractions import Fraction

sys.path.insert(0, "src")

from bounded_cfg import BoundedModel, Grammar, Production
from independent_check import string_weights

grammar = Grammar(
    [
        Production("S", ("A",), Fraction(1)),
        Production("A", ("A",), Fraction(1, 2)),               # unit loop
        Production("A", ("A", "A"), Fraction(1, 3), group="branch"),
        Production("A", (), Fraction(1, 5), group="eps"),      # epsilon loop exit
        Production("A", ("a",), Fraction(1)),
        Production("A", ("b",), Fraction(0)),                  # zero weight
        Production("U", ("u",), Fraction(7)),                  # unreachable
    ],
    start="S",
)

L, D = 2, 4
model = BoundedModel(grammar, L, D, tracked_groups=("branch", "eps"))

print(f"grammar: A -> A | A A | eps | a   (L={L}, D={D})")
print(f"exact partition Z = {model.partition()}  (~{float(model.partition()):.4f})")
print("\nZ[length, steps] (exact):")
for (length, steps), z in sorted(model.partition_by_length_steps().items()):
    if z:
        print(f"  len={length} steps={steps}: {z}")

print("\nexact string probabilities (ambiguous strings sum over trees):")
total = model.partition()
for string, w in sorted(string_weights(grammar, L, D).items()):
    if w:
        print(f"  {string!r}: {w / total}  (~{float(w / total):.4f})")

rng = random.Random(20240924)  # fixed seed -> reproducible output
print("\n10 samples (seed=20240924):")
for i in range(10):
    r = model.sample(rng)
    print(f"  {r.terminals!s:12} steps={r.steps} groups={sorted(r.groups)}"
          f"  p={r.probability} (~{float(r.probability):.4f})")

print("\n5 samples conditioned on group 'branch' used at least once:")
z_cond = model.partition(required_groups=("branch",))
print(f"  conditional partition = {z_cond}")
for i in range(5):
    r = model.sample(rng, required_groups=("branch",))
    print(f"  {r.terminals!s:12} steps={r.steps} groups={sorted(r.groups)}"
          f"  p={r.probability} (~{float(r.probability):.4f})")

print("\n5 samples of length exactly 1:")
for i in range(5):
    r = model.sample(rng, length=1)
    print(f"  {r.terminals!s:12} steps={r.steps}  p={r.probability}")

empty = model.sample(random.Random(0), length=2, steps=1)
print(f"\ninfeasible request (len=2, steps=1) -> {empty!r} (explicit empty domain)")
