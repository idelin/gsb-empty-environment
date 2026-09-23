"""End-to-end demo: learn a hidden DFA, then compare fresh vs resumed runs.

Run from the repository root:  python3 examples/demo.py
"""

import os
import sys
import tempfile

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))
sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", "tests"))

from dfa_learning import (  # noqa: E402
    BUDGET_EXHAUSTED,
    EQUIVALENT_IN_DOMAIN,
    BoundedEquivalenceOracle,
    Learner,
    MembershipOracle,
    domain_equivalent,
)
from hidden_machines import ALPHABET, long_suffix_machine  # noqa: E402

MAX_LEN = 12
VERSION = "demo-v1"


def make_oracles(hidden):
    membership = MembershipOracle(hidden, MAX_LEN, version=VERSION)
    equivalence = BoundedEquivalenceOracle(hidden, ALPHABET, MAX_LEN, version=VERSION)
    return membership, equivalence


def print_rounds(result):
    print(f"{'round':>5} {'states':>6} {'counterexample':>14} "
          f"{'new_mq':>6} {'cache_hits':>10}")
    for record in result.rounds:
        cex = record["counterexample"]
        print(f"{record['index']:>5} {record['states']:>6} "
              f"{repr(cex):>14} {record['new_membership_queries']:>6} "
              f"{record['cache_hits']:>10}")


def main():
    hidden = long_suffix_machine()
    print(f"hidden machine: {hidden.num_states} states, "
          f"alphabet {ALPHABET}, domain L={MAX_LEN}")

    # ---- fresh run ------------------------------------------------------
    membership, equivalence = make_oracles(hidden)
    learner = Learner(membership, ALPHABET, MAX_LEN, budget=10 ** 9)
    fresh = learner.learn(equivalence)
    print(f"\nfresh run: {fresh.status}")
    print_rounds(fresh)
    print(f"totals: membership={fresh.membership_queries} "
          f"cache_hits={fresh.cache_hits} equivalence={fresh.equivalence_queries}")
    print(f"oracle actually answered {membership.query_count} membership queries")
    assert fresh.status == EQUIVALENT_IN_DOMAIN
    assert domain_equivalent(hidden, fresh.hypothesis, ALPHABET, MAX_LEN)
    print("enumerator check: hypothesis agrees with hidden machine on all "
          f"{sum(2 ** i for i in range(MAX_LEN + 1))} words of length <= {MAX_LEN}")

    # ---- budget stop + checkpoint resume --------------------------------
    cut = 7
    membership2, equivalence2 = make_oracles(hidden)
    first = Learner(membership2, ALPHABET, MAX_LEN, budget=cut)
    stopped = first.learn(equivalence2)
    print(f"\nsegment 1 with budget {cut}: {stopped.status} "
          f"(membership queries used: {stopped.membership_queries})")
    assert stopped.status == BUDGET_EXHAUSTED

    with tempfile.TemporaryDirectory() as tmpdir:
        path = os.path.join(tmpdir, "checkpoint.json")
        first.save_checkpoint(path)
        print(f"checkpoint saved: S={len(first.S)} rows, E={len(first.E)} columns, "
              f"cache={len(first.cache)} words, "
              f"pending suffixes={first.pending_suffixes}")
        resumed = Learner.load_checkpoint(path, membership2, budget=10 ** 9)
        continued = resumed.learn(equivalence2)

    print(f"segment 2 after resume: {continued.status}")
    print_rounds(continued)
    print(f"totals: membership={continued.membership_queries} "
          f"cache_hits={continued.cache_hits} "
          f"equivalence={continued.equivalence_queries}")
    print(f"oracle actually answered {membership2.query_count} membership queries")

    # ---- comparison ------------------------------------------------------
    same = all(
        fresh.hypothesis.accepts(w) == continued.hypothesis.accepts(w)
        for w in __import__("dfa_learning").iter_words(ALPHABET, MAX_LEN)
    )
    print(f"\nfresh vs resumed: identical behaviour on the domain -> {same}")
    print(f"same membership query count -> "
          f"{fresh.membership_queries == continued.membership_queries}")
    print(f"same per-round records -> {fresh.rounds == continued.rounds}")
    assert same
    assert fresh.rounds == continued.rounds


if __name__ == "__main__":
    main()
