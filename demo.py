"""Deterministic demo of the bounded_sampler library (Python 3.11, stdlib only).

Run:  python3.11 demo.py
"""

import random
from fractions import Fraction

from bounded_sampler import (
    BoundedSampler,
    BudgetExhaustedError,
    Grammar,
    PartitionTable,
    Production,
    ResourceBudget,
    randbelow_from_random,
    string_mass,
    validate_tree,
)


def main() -> None:
    grammar = Grammar.create("S", (
        Production("S", ("S", "S"), 2, group="struct"),
        Production("S", ("a",), 3, group="leaf"),
        Production("S", (), 1, group="leaf"),
    ))
    L, D = 6, 9
    table = PartitionTable(grammar, L, D)

    print(f"grammar: S -> SS (w2, struct) | a (w3, leaf) | eps (w1, leaf);  L={L}, D={D}")
    print(f"exact total mass Z = {table.total_mass()}")
    print("\njoint partition mass per (length, steps):")
    for (l, d), mass in table.joint_partition().items():
        print(f"  length={l} steps={d}: {mass}")

    struct_bit = table.group_bit["struct"]
    cond_total = table.total_mass(require_mask=struct_bit)
    print(f"\nconditional total mass given group 'struct' used: {cond_total}")

    print("\nstring masses (summed over all derivations, ambiguity counts):")
    for word in ("", "a", "aa", "aaa"):
        print(f"  {word!r}: {string_mass(table, word)}")

    seed = 20260923
    rng = random.Random(seed)
    sampler = BoundedSampler(table, randbelow_from_random(rng))

    print(f"\n5 unconditional samples (seed={seed}):")
    for _ in range(5):
        s = sampler.sample()
        v = validate_tree(s.tree, grammar, L, D)
        print(f"  terminals={''.join(s.terminals)!r:12} steps={s.steps} "
              f"P={s.probability}  validator_ok={v.ok}")

    print(f"\n5 samples with length exactly 3 (seed continues):")
    for _ in range(5):
        s = sampler.sample(length=3)
        v = validate_tree(s.tree, grammar, L, D)
        print(f"  terminals={''.join(s.terminals)!r:12} steps={s.steps} "
              f"P={s.probability}  validator_ok={v.ok}")

    print(f"\n5 samples conditioned on group 'struct' (seed continues):")
    for _ in range(5):
        s = sampler.sample(require_groups=("struct",))
        v = validate_tree(s.tree, grammar, L, D)
        print(f"  terminals={''.join(s.terminals)!r:12} steps={s.steps} "
              f"P={s.probability}  validator_ok={v.ok} groups={sorted(v.used_groups)}")

    print("\ninfeasible requests return None:")
    print("  length=2, steps=1 ->", sampler.sample(length=2, steps=1))
    print("  length=99        ->", sampler.sample(length=99))

    print("\nresource budget exhaustion aborts instead of truncating:")
    try:
        PartitionTable(grammar, L, D, budget=ResourceBudget(max_ops=10))
    except BudgetExhaustedError as exc:
        print(f"  BudgetExhaustedError: {exc}")


if __name__ == "__main__":
    main()
