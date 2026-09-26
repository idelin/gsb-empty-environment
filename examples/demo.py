"""Runnable end-to-end demonstration.

Run with:  python3.11 examples/demo.py

It exercises every feature on two explicit constraints over alphabet {a,b}:

  1. strings with an even number of 'a';
  2. strings containing no "bb" substring.
"""

import os
import sys

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

from dfa_intersection import (  # noqa: E402
    Checkpoint,
    DFA,
    ExactCounter,
    ModCounter,
    accepts_all,
)

ALPHABET = ("a", "b")
MOD = 1_000_000_007


def build_constraints():
    even_a = DFA(
        ALPHABET, start=0, accepting={0},
        transitions={
            (0, "a"): 1, (1, "a"): 0,
            (0, "b"): 0, (1, "b"): 1,
        },
    )
    no_bb = DFA(
        ALPHABET, start=0, accepting={0, 1},
        transitions={(0, "a"): 0, (0, "b"): 1, (1, "a"): 0},
    )
    return even_a, no_bb


def section(title):
    print()
    print("=" * 12, title, "=" * 12)


def main():
    even_a, no_bb = build_constraints()
    constraints = [even_a, no_bb]
    exact = ExactCounter(constraints)

    section("exact counting (big integers)")
    print("sorted alphabet:", "".join(exact.alphabet))
    print("|L|, lengths 0..8:", [exact.count_length(n) for n in range(9)])
    print("|L|, lengths 3..8:", exact.count_interval(3, 8))

    section("k-th string and rank (0-based, exact counts only)")
    text = exact.kth_string(6, 10)
    print("11th accepted string of length 6:", text)
    print("its rank:", exact.rank(text))
    print("independent simulation accepts it:", accepts_all(constraints, text))
    print("rank -> string round trip:", exact.kth_string(6, exact.rank(text)))
    print("4th in length interval [4, 6]:", exact.kth_in_interval(4, 6, 3))

    section("modular counting is a separate mode")
    mod = ModCounter(constraints, MOD)
    big = 100_000
    print(f"|L_{big}| mod {MOD}:", mod.count_length(big))
    print("matches exact % mod:",
          mod.count_length(big) == exact.count_length(big) % MOD)
    try:
        mod.kth_string(6, 0)
    except Exception as exc:  # noqa: BLE001
        print("mod.kth_string refused:", type(exc).__name__, "-", exc)

    section("large length handled by reusable power blocks")
    unary = DFA("a", start=0, accepting={0},
                transitions={(0, "a"): 1, (1, "a"): 0})
    unary_counter = ExactCounter([unary])
    huge = 2 ** 60
    print(f"even unary lengths only; n = 2^60 = {huge}")
    print("count(n) =", unary_counter.count_length(huge),
          " count(n+1) =", unary_counter.count_length(huge + 1))
    print("count over [0, n] =", unary_counter.count_interval(0, huge),
          "(= 2^59 + 1)")

    section("infinitude witness: reachable cycle that reaches acceptance")
    witness = exact.infinite_witness()
    print("witness:", witness)
    print("witness.verify(constraints):", witness.verify(constraints))
    for repeats in range(4):
        sample = witness.sample(repeats)
        print(f"  prefix + cycle^{repeats} + suffix = {sample!r} accepted:",
              accepts_all(constraints, sample))

    finite_dfa = DFA(
        "ab", start="s", accepting={"z"},
        transitions={("s", "a"): "c", ("c", "a"): "c", ("s", "b"): "z"},
    )
    finite_counter = ExactCounter([finite_dfa])
    print("reachable rejecting loop with no path to acceptance -> "
          "infinite?", finite_counter.is_infinite())

    section("resumable enumeration with an output budget")
    token = None
    gathered = []
    for page in range(1, 5):
        enumerator = exact.enumerator(7, budget=6, checkpoint=token)
        batch = list(enumerator)
        gathered.extend(batch)
        print(f"page {page}: produced={enumerator.produced} "
              f"total={enumerator.total} done={enumerator.done} batch={batch}")
        if enumerator.done:
            break
        token = Checkpoint.loads(enumerator.checkpoint().dumps())
    print("all strings of length 7:", gathered)
    print("count matches:", len(gathered) == exact.count_length(7))


if __name__ == "__main__":
    main()
