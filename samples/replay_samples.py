"""Deterministic, replayable end-to-end sample.

Run:  python -m samples.replay_samples
Every run prints the exact same transcript; tests/test_treeauto.py pins it.
"""

from __future__ import annotations

from typing import List

from treeauto import (Alphabet, AttrCondition, Automaton, Node, Rule,
                      Session, check_expectation, evaluate)


def build_automaton() -> Automaton:
    alphabet = Alphabet.of({
        "num": {0}, "true": {0}, "false": {0},
        "neg": {1}, "plus": {2}, "lt": {2}, "and": {2},
    })
    rules = [
        Rule.of("r_num_nat", "num", [], "q_nat",
                [AttrCondition("value", "ge", 0)]),
        Rule.of("r_num_int", "num", [], "q_int"),
        Rule.of("r_neg", "neg", ["q_int"], "q_int"),
        Rule.of("r_plus_nat", "plus", ["q_nat", "q_nat"], "q_nat"),
        Rule.of("r_plus_int", "plus", ["q_int", "q_int"], "q_int"),
        Rule.of("r_lt", "lt", ["q_int", "q_int"], "q_bool"),
        Rule.of("r_and", "and", ["q_bool", "q_bool"], "q_bool"),
        Rule.of("r_true", "true", [], "q_bool"),
        Rule.of("r_false", "false", [], "q_bool"),
    ]
    return Automaton(alphabet, {"q_nat", "q_int", "q_bool"}, rules,
                     accepting={"q_bool"})


def build_tree():
    shared = Node.make("num", attrs={"value": 3})
    ten = Node.make("num", attrs={"value": 10})
    plus = Node.make("plus", [shared, shared])       # shared subtree, 2 parents
    lt = Node.make("lt", [plus, ten])
    leaf = Node.make("true")
    chain = Node.make("and", [leaf, Node.make("and", [leaf, leaf])])
    root = Node.make("and", [lt, chain])
    return root, shared


def fmt(states) -> str:
    return "[" + ", ".join(sorted(states)) + "]"


def run_sample() -> str:
    lines: List[str] = []
    automaton = build_automaton()
    root, shared = build_tree()
    session = Session(automaton, root)

    snap0 = session.snapshot()
    lines.append(f"STEP 01 build: root_states={fmt(snap0.root_states)} "
                 f"accepted={snap0.accepted}")

    stats = snap0.stats
    lines.append(f"STEP 02 shared-subtree: occurrences={list(snap0.occurrences(shared))} "
                 f"cache_evaluations={stats.evaluations_of(shared)}")

    proof_a = snap0.proof("q_nat", path=(0, 0, 0))
    proof_b = snap0.proof("q_nat", path=(0, 0, 1))
    lines.append(f"STEP 03 path-evidence: first={proof_a.path} "
                 f"second={proof_b.path} distinct={proof_a.path != proof_b.path}")

    deep = (1, 1, 1)
    report = session.edit(deep, label="num", attrs={"value": 1})
    lines.append(f"STEP 04 deep-leaf-edit: path={deep} "
                 f"recomputed={report.recomputed_count} "
                 f"accepted {report.accepted_before}->{report.accepted_after}")

    lines.append(f"STEP 05 old-snapshot: states_at{deep}="
                 f"{fmt(snap0.states_at(deep))} accepted={snap0.accepted} "
                 f"(still queryable)")

    revoke = session.delete_rules(["r_num_nat"])
    revoked = [f"{n.label}{n.attrs_dict()}:{s}" for n, s in revoke.revoked]
    lines.append(f"STEP 06 delete-rule r_num_nat: revoked={revoked}")

    current = session.snapshot()
    cx = check_expectation(session.automaton, session.root, True,
                           note="expected the edited tree to stay accepted")
    replay = cx.replay()
    lines.append(f"STEP 07 counterexample: expected={replay.expected_accepted} "
                 f"actual={replay.actual_accepted} "
                 f"confirmed={replay.confirmed}")
    lines.append(f"STEP 08 final: root_states={fmt(current.root_states)} "
                 f"accepted={current.accepted} snapshots={len(session.snapshots)}")
    return "\n".join(lines)


def main() -> None:
    print(run_sample())


if __name__ == "__main__":
    main()
