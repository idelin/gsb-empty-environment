"""Standalone counterexample finder.

Given a DFA and a claim that two states are language-equivalent, produce a
concrete counterexample string accepted from exactly one of them (or report
that the claim holds). Runs independently:

    python3.11 counterexample.py
"""

from dfa import DFA, normalize, distinguishing_string, run


def find_counterexample(dfa: DFA, p, q):
    """Return a string distinguishing p and q, or None if equivalent."""
    return distinguishing_string(normalize(dfa), p, q)


def main() -> None:
    dfa = DFA(
        states=frozenset({"s0", "s1", "s2", "s3"}),
        alphabet=frozenset({"a", "b"}),
        transitions={
            ("s0", "a"): "s1", ("s0", "b"): "s2",
            ("s1", "a"): "s1", ("s1", "b"): "s3",
            ("s2", "a"): "s1", ("s2", "b"): "s3",
            ("s3", "a"): "s3", ("s3", "b"): "s3",
        },
        start="s0",
        accepting=frozenset({"s3"}),
    )
    claim = ("s0", "s1")  # claimed equivalent; in fact "b" distinguishes them
    witness = find_counterexample(dfa, *claim)
    if witness is None:
        print(f"claim holds: {claim[0]} and {claim[1]} are equivalent")
    else:
        nd = normalize(dfa)
        print(f"counterexample for {claim}: {witness!r}")
        print(f"  from {claim[0]}: ends at {run(nd, claim[0], witness)}"
              f" (accepting={run(nd, claim[0], witness) in nd.accepting})")
        print(f"  from {claim[1]}: ends at {run(nd, claim[1], witness)}"
              f" (accepting={run(nd, claim[1], witness) in nd.accepting})")


if __name__ == "__main__":
    main()
