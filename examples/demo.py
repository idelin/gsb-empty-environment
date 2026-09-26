"""Runnable demo: intersection counting, k-th word, ranking, infinitude,
modular counting and resumable enumeration.

Run from the repository root:  python3 examples/demo.py
"""

import os
import sys

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

from automata import (Automaton, Counter, LexEnumerator, ProductAutomaton,
                      find_infinite_evidence, verify_evidence)


def build_constraints():
    """Two explicitly constructed constraints over the alphabet {a, b}."""
    # constraint 1: an even number of a's
    even_a = Automaton(
        alphabet="ab",
        states=["even", "odd"],
        initial="even",
        accepting={"even"},
        transitions={
            ("even", "a"): "odd", ("odd", "a"): "even",
            ("even", "b"): "even", ("odd", "b"): "odd",
        },
    )
    # constraint 2: the word ends with b
    ends_b = Automaton(
        alphabet="ab",
        states=["no", "yes"],
        initial="no",
        accepting={"yes"},
        transitions={
            ("no", "a"): "no", ("no", "b"): "yes",
            ("yes", "a"): "no", ("yes", "b"): "yes",
        },
    )
    return ProductAutomaton([even_a, ends_b])


def main():
    product = build_constraints()
    lo, hi = 1, 8

    print("== exact big-integer counting ==")
    counter = Counter(product)  # exact mode
    total = counter.count(lo, hi)
    print(f"words with length in [{lo}, {hi}]: {total}")
    print(f"words of exact length 40: {counter.count_length(40)}")

    print("\n== modular counting (separate mode) ==")
    modulus = 1_000_000_007
    mod_counter = Counter(product, mode="mod", modulus=modulus)
    n = 10**12
    print(f"words of length 10**12 (mod {modulus}): {mod_counter.count_length(n)}")

    print("\n== k-th word and ranking (lexicographic) ==")
    k = 100
    word = counter.kth(k, lo, hi)
    print(f"word #{k}: {word!r}")
    print(f"rank({word!r}) = {counter.rank(word, lo, hi)}  (round-trips to k)")

    print("\n== infinitude with cycle evidence ==")
    ev = find_infinite_evidence(product)
    print(f"evidence: prefix={ev.prefix!r} loop={ev.loop!r} suffix={ev.suffix!r}")
    print(f"independently verified: {verify_evidence(product, ev)}")
    print(f"witness with 3 loops: {ev.witness(3)!r} accepted={product.accepts(ev.witness(3))}")

    print("\n== budgeted, resumable enumeration ==")
    enum = LexEnumerator(counter, lo, 3)
    batch, done = enum.take(5)
    print(f"first batch (budget 5): {batch}, exhausted={done}")
    cp = enum.checkpoint()
    print(f"checkpoint: versions={cp.versions} alphabet={cp.alphabet} emitted={cp.emitted}")
    resumed = LexEnumerator.resume(counter, lo, 3, cp)
    rest, done = resumed.take(100)
    print(f"resumed batch: {rest}, exhausted={done}")

    print("\n== cache invalidation on mutation ==")
    before = counter.count(lo, hi)
    product.automata[1].add_transition("yes", "a", "yes")  # relax constraint 2
    after = counter.count(lo, hi)
    print(f"count before mutation: {before}, after: {after} (cache rebuilt)")


if __name__ == "__main__":
    main()
