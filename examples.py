"""Runnable examples for the DFA regular constraint.

Run with:  python3.11 examples.py
"""

from __future__ import annotations

from automaton import DFA
from independent_checker import check_witness, exhaustive_support
from regular_constraint import RegularConstraint


def even_ones_dfa() -> DFA:
    """Binary words with an even number of 1s."""
    transitions = {
        ("even", 0): "even",
        ("even", 1): "odd",
        ("odd", 0): "odd",
        ("odd", 1): "even",
    }
    return DFA({"even", "odd"}, {0, 1}, transitions, "even", {"even"})


def ends_with_a_dfa() -> DFA:
    """Words over {a, b} that end with 'a'."""
    transitions = {
        ("expect", "a"): "got_a",
        ("expect", "b"): "expect",
        ("got_a", "a"): "got_a",
        ("got_a", "b"): "expect",
    }
    return DFA({"expect", "got_a"}, {"a", "b"}, transitions, "expect", {"got_a"})


def show(title, constraint: RegularConstraint) -> None:
    print(f"--- {title} ---")
    sat = constraint.is_satisfiable()
    print(f"satisfiable: {sat}")
    if sat:
        witness = constraint.witness()
        ok = check_witness(constraint.dfa, constraint.original_domains, witness)
        print(f"witness: {witness}  (independent check: {ok})")
    kept = [sorted(d, key=repr) for d in constraint.domains]
    print(f"domains after filtering: {kept}")
    truth_sat, truth = exhaustive_support(constraint.dfa, constraint.original_domains)
    truth_str = [sorted(s, key=repr) for s in truth]
    print(f"exhaustive ground truth: sat={truth_sat}, support={truth_str}")
    print()


def main() -> None:
    c1 = RegularConstraint(even_ones_dfa(), [{0, 1}] * 4)
    c1.filter()
    show("even number of 1s, n=4, domains {0,1}", c1)

    c2 = RegularConstraint(even_ones_dfa(), [{1}, {0, 1}, {0, 1}])
    c2.filter()
    show("even number of 1s, n=3, X0={1}", c2)

    c3 = RegularConstraint(ends_with_a_dfa(), [{"a", "b"}] * 3)
    c3.filter()
    show("ends with 'a', n=3", c3)

    c4a = RegularConstraint(even_ones_dfa(), [])
    print("--- empty sequence, accepting start state ---")
    print(f"satisfiable: {c4a.is_satisfiable()}, witness: {c4a.witness()}")
    print()
    c4b = RegularConstraint(ends_with_a_dfa(), [])
    print("--- empty sequence, non-accepting start state ---")
    print(f"satisfiable: {c4b.is_satisfiable()}, witness: {c4b.witness()}")
    print()

    print("--- incremental updates on 'ends with a', n=3 ---")
    c5 = RegularConstraint(ends_with_a_dfa(), [{"a", "b"}] * 3)
    print(f"initial sat: {c5.is_satisfiable()}")
    print(f"remove 'b' at pos 0 -> sat: {c5.remove_value(0, 'b')}")
    print(f"assign pos 1 to {{'b'}} -> sat: {c5.set_domain(1, {'b'})}")
    witness = c5.witness()
    ok = check_witness(c5.dfa, c5.original_domains, witness)
    print(f"witness: {witness}  (independent check: {ok})")
    print(f"supported at pos 2 now: {sorted(c5.supported_values(2))}")
    print(f"remove 'a' at pos 2 -> sat: {c5.remove_value(2, 'a')}  (now unsat)")


if __name__ == "__main__":
    main()
