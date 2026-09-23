"""Demo experiment.

Learns a hidden DFA from membership queries and bounded equivalence
queries, prints per-round statistics, demonstrates checkpoint/resume
under a sliced query budget, and cross-checks everything with the
brute-force verifier on a small domain.
"""
from __future__ import annotations

import os
import tempfile

from learner import (ObservationTableLearner, STATUS_BUDGET_UNDECIDED,
                     STATUS_EQUIVALENT_IN_DOMAIN)
from machines import ends_with_dfa
from oracle import BoundedEquivalenceOracle, MembershipOracle
from verifier import BruteForceCheckedOracle, domain_equivalent

ALPHABET = ("a", "b")
L = 6
SLICE = 7  # membership queries granted per session in the resume demo


def print_rounds(rounds):
    print(f"  {'round':>5} {'states':>6} {'new MQ':>7} {'cache hits':>10}  counterexample")
    for r in rounds:
        ce = repr(r.counterexample) if r.counterexample is not None else "-"
        print(f"  {r.round_index:>5} {r.num_states:>6} "
              f"{r.new_membership_queries:>7} {r.cache_hits:>10}  {ce}")


def main():
    hidden = ends_with_dfa("abb", ALPHABET)
    print(f"hidden machine: {hidden!r}  (language: words ending in 'abb')")
    print(f"domain: all words over {ALPHABET} of length <= {L}\n")

    # ---- fresh run with unlimited budget -------------------------------
    print("[1] fresh run, unlimited budget")
    mq = MembershipOracle(hidden)
    oracle = BoundedEquivalenceOracle(hidden, ALPHABET, L)
    checked = BruteForceCheckedOracle(oracle, hidden, ALPHABET, L)
    learner = ObservationTableLearner(ALPHABET, L, mq, oracle.version)
    result = learner.learn(checked)
    print_rounds(result.rounds)
    print(f"  status: {result.status}  ({result.note})")
    print(f"  total new membership queries: {learner.new_queries}, "
          f"cache hits: {learner.cache_hits}, "
          f"equivalence queries: {oracle.num_queries}")
    assert result.status == STATUS_EQUIVALENT_IN_DOMAIN
    assert domain_equivalent(hidden, result.hypothesis, ALPHABET, L)
    print("  brute-force verifier: hypothesis equivalent to hidden on the domain\n")

    # ---- same input, sliced budget with checkpoint/resume --------------
    print(f"[2] same input, budget slices of {SLICE} queries with checkpoints")
    mq2 = MembershipOracle(hidden)
    oracle2 = BoundedEquivalenceOracle(hidden, ALPHABET, L)
    checked2 = BruteForceCheckedOracle(oracle2, hidden, ALPHABET, L)
    fd, path = tempfile.mkstemp(prefix="learner-checkpoint-", suffix=".json")
    os.close(fd)
    try:
        session = 0
        learner2 = ObservationTableLearner(ALPHABET, L, mq2, oracle2.version,
                                           budget=SLICE)
        while True:
            session += 1
            result2 = learner2.learn(checked2)
            print(f"  session {session}: used {learner2.used} queries -> "
                  f"{result2.status}")
            if result2.status != STATUS_BUDGET_UNDECIDED:
                break
            learner2.save_checkpoint(path)
            learner2 = ObservationTableLearner.resume_from_checkpoint(
                path, mq2, oracle2.version, budget=SLICE)
        print_rounds(result2.rounds)
        print(f"  total new membership queries: {learner2.new_queries}, "
              f"cache hits: {learner2.cache_hits}")
    finally:
        os.unlink(path)

    # ---- fresh vs resumed comparison -----------------------------------
    same = domain_equivalent(result.hypothesis, result2.hypothesis, ALPHABET, L)
    same_queries = learner.new_queries == learner2.new_queries
    print(f"[3] fresh vs resumed: identical in-domain behaviour: {same}, "
          f"identical new-query count: {same_queries}")
    assert same and same_queries
    print("done.")


if __name__ == "__main__":
    main()
