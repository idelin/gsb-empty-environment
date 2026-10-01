"""Sample run: unreachable removal, dead-state completion, minimization,
certificate, incremental repair, relabeling invariance, verification.

    python3.11 demo.py
"""

from dfa import (DFA, minimize, repair_partition, verify_partition,
                 canonical_key, relabel)


def sample_dfa() -> DFA:
    # s5 is unreachable; (s3, "b") is missing -> dead state; s1 ~ s2 redundant.
    return DFA(
        states=frozenset({"s0", "s1", "s2", "s3", "s4", "s5"}),
        alphabet=frozenset({"a", "b"}),
        transitions={
            ("s0", "a"): "s1", ("s0", "b"): "s2",
            ("s1", "a"): "s1", ("s1", "b"): "s3",
            ("s2", "a"): "s1", ("s2", "b"): "s3",
            ("s3", "a"): "s4",
            ("s4", "a"): "s4", ("s4", "b"): "s4",
            ("s5", "a"): "s5", ("s5", "b"): "s5",
        },
        start="s0",
        accepting=frozenset({"s4"}),
    )


def show_result(title, result):
    print(f"== {title} ==")
    print("normalized states:", sorted(map(repr, result.normalized.states)))
    print("blocks:", [sorted(map(repr, b)) for b in result.blocks])
    print("old -> new mapping:")
    for old in sorted(result.mapping, key=repr):
        print(f"  {old!r:>6} -> {result.mapping[old]}")
    print("quotient transitions:")
    for (s, a), t in sorted(result.quotient.transitions.items()):
        print(f"  ({s}, {a}) -> {t}")
    print("accepting:", sorted(result.quotient.accepting))
    print("certificate (shortest distinguishing string per quotient-state pair):")
    for (x, y), w in sorted(result.certificate.items()):
        print(f"  {x} vs {y}: {w!r}")
    print("pairs touched:", result.pairs_touched)
    print()


def main() -> None:
    dfa = sample_dfa()
    result = minimize(dfa)
    show_result("Minimization", result)

    ok, checked, cex = verify_partition(dfa, result.blocks)
    print(f"== Independent product-space verification ==")
    print(f"ok={ok}, pairs checked={checked}, counterexample={cex}\n")

    permuted = relabel(dfa, lambda s: f"renamed_{s}" if isinstance(s, str) else s)
    print("== Relabeling invariance ==")
    print("canonical keys equal:", canonical_key(dfa) == canonical_key(permuted), "\n")

    # Incremental repair: split. s2.b: s3 -> s4 breaks the {s1, s2} block.
    split_dfa = sample_dfa()
    split_dfa.transitions[("s2", "b")] = "s4"
    rep = repair_partition(split_dfa, result.blocks)
    print("== Incremental repair after local change (s2,b)->s4 ==")
    print("partition:", [sorted(map(repr, b)) for b in rep.partition])
    print(f"splits={rep.splits}, merges={rep.merges}, pairs touched={rep.pairs_touched}")
    full = minimize(split_dfa)
    print("matches full re-minimization:",
          set(rep.partition) == set(full.blocks), "\n")

    # Incremental repair: merge. s1.a: s1 -> s4 and s1.b removed (-> dead)
    # give s1 exactly s3's transitions, so {s1} and {s3} merge.
    merge_dfa = sample_dfa()
    merge_dfa.transitions[("s1", "a")] = "s4"
    del merge_dfa.transitions[("s1", "b")]
    rep = repair_partition(merge_dfa, result.blocks)
    print("== Incremental repair after local changes (s1,a)->s4, (s1,b) removed ==")
    print("partition:", [sorted(map(repr, b)) for b in rep.partition])
    print(f"splits={rep.splits}, merges={rep.merges}, pairs touched={rep.pairs_touched}")
    full = minimize(merge_dfa)
    print("matches full re-minimization:",
          set(rep.partition) == set(full.blocks))


if __name__ == "__main__":
    main()
