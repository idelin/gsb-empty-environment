"""Checkpoint/resume tests: arbitrary stop points, binding, cache reuse."""

import json
import os
import tempfile
import unittest

from dfa_learning import (
    BUDGET_EXHAUSTED,
    EQUIVALENT_IN_DOMAIN,
    BoundedEquivalenceOracle,
    CheckpointMismatchError,
    Learner,
    MembershipOracle,
    iter_words,
)

from hidden_machines import ALPHABET, long_suffix_machine

VERSION = "hidden-v1"
MAX_LEN = 12


def fresh_run(budget=10 ** 9):
    hidden = long_suffix_machine()
    membership = MembershipOracle(hidden, MAX_LEN, version=VERSION)
    equivalence = BoundedEquivalenceOracle(hidden, ALPHABET, MAX_LEN, version=VERSION)
    learner = Learner(membership, ALPHABET, MAX_LEN, budget)
    result = learner.learn(equivalence)
    return learner, equivalence, result


def segmented_run(cut, tmpdir):
    hidden = long_suffix_machine()
    membership = MembershipOracle(hidden, MAX_LEN, version=VERSION)
    equivalence = BoundedEquivalenceOracle(hidden, ALPHABET, MAX_LEN, version=VERSION)

    first = Learner(membership, ALPHABET, MAX_LEN, budget=cut)
    stopped = first.learn(equivalence)
    assert stopped.status == BUDGET_EXHAUSTED
    path = os.path.join(tmpdir, f"checkpoint-{cut}.json")
    first.save_checkpoint(path)

    resumed = Learner.load_checkpoint(path, membership, budget=10 ** 9)
    result = resumed.learn(equivalence)
    return resumed, result


class ResumeTest(unittest.TestCase):
    def test_resume_at_arbitrary_query_points_matches_fresh_run(self):
        fresh_learner, _, fresh_result = fresh_run()
        self.assertEqual(fresh_result.status, EQUIVALENT_IN_DOMAIN)
        total = fresh_result.membership_queries
        self.assertGreater(total, 10)

        cuts = sorted({1, 2, 3, total // 3, total // 2, total - 1})
        with tempfile.TemporaryDirectory() as tmpdir:
            for cut in cuts:
                with self.subTest(cut=cut):
                    resumed, result = segmented_run(cut, tmpdir)
                    self.assertEqual(result.status, EQUIVALENT_IN_DOMAIN)
                    # Final hypotheses behave identically on the whole domain.
                    for word in iter_words(ALPHABET, MAX_LEN):
                        self.assertEqual(
                            fresh_result.hypothesis.accepts(word),
                            result.hypothesis.accepts(word),
                            msg=f"word {word!r} differs after resume at {cut}",
                        )
                    # Deterministic replay: identical evidence, identical
                    # counters and per-round records; the cache was reused
                    # instead of re-asking the oracle.
                    self.assertEqual(
                        fresh_result.membership_queries, result.membership_queries
                    )
                    self.assertEqual(fresh_result.cache_hits, result.cache_hits)
                    self.assertEqual(fresh_result.rounds, result.rounds)
                    self.assertEqual(fresh_learner.cache, resumed.cache)

    def test_resume_mid_counterexample_processing(self):
        # Find a budget cut that lands inside counterexample processing, i.e.
        # the checkpoint still carries pending suffixes.
        hidden = long_suffix_machine()
        membership = MembershipOracle(hidden, MAX_LEN, version=VERSION)
        equivalence = BoundedEquivalenceOracle(hidden, ALPHABET, MAX_LEN, version=VERSION)
        for cut in range(1, 60):
            learner = Learner(membership, ALPHABET, MAX_LEN, budget=cut)
            if learner.learn(equivalence).status == EQUIVALENT_IN_DOMAIN:
                break
            if learner.pending_suffixes:
                break
        self.assertTrue(learner.pending_suffixes)
        with tempfile.TemporaryDirectory() as tmpdir:
            path = os.path.join(tmpdir, "mid-cex.json")
            learner.save_checkpoint(path)
            resumed = Learner.load_checkpoint(path, membership, budget=10 ** 9)
            result = resumed.learn(equivalence)
            self.assertEqual(result.status, EQUIVALENT_IN_DOMAIN)
            for word in iter_words(ALPHABET, MAX_LEN):
                self.assertEqual(hidden.accepts(word), result.hypothesis.accepts(word))


class BindingTest(unittest.TestCase):
    def setUp(self):
        self.hidden = long_suffix_machine()
        self.membership = MembershipOracle(self.hidden, MAX_LEN, version=VERSION)
        equivalence = BoundedEquivalenceOracle(self.hidden, ALPHABET, MAX_LEN, VERSION)
        learner = Learner(self.membership, ALPHABET, MAX_LEN, budget=5)
        learner.learn(equivalence)
        self.tmpdir = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmpdir.cleanup)
        self.path = os.path.join(self.tmpdir.name, "checkpoint.json")
        learner.save_checkpoint(self.path)

    def test_matching_checkpoint_loads(self):
        learner = Learner.load_checkpoint(
            self.path, self.membership, alphabet=ALPHABET, max_len=MAX_LEN
        )
        self.assertEqual(learner.alphabet, ALPHABET)
        self.assertEqual(learner.max_len, MAX_LEN)

    def test_oracle_version_mismatch_rejects_old_evidence(self):
        other = MembershipOracle(self.hidden, MAX_LEN, version="hidden-v2")
        with self.assertRaises(CheckpointMismatchError):
            Learner.load_checkpoint(self.path, other)

    def test_alphabet_mismatch_is_rejected(self):
        with self.assertRaises(CheckpointMismatchError):
            Learner.load_checkpoint(
                self.path, self.membership, alphabet=("b", "a"), max_len=MAX_LEN
            )

    def test_domain_bound_mismatch_is_rejected(self):
        with self.assertRaises(CheckpointMismatchError):
            Learner.load_checkpoint(
                self.path, self.membership, alphabet=ALPHABET, max_len=MAX_LEN + 1
            )

    def test_tampered_binding_is_detected(self):
        with open(self.path, "r", encoding="utf-8") as handle:
            data = json.load(handle)
        data["binding"]["max_len"] = MAX_LEN + 5
        tampered = os.path.join(self.tmpdir.name, "tampered.json")
        with open(tampered, "w", encoding="utf-8") as handle:
            json.dump(data, handle)
        with self.assertRaises(CheckpointMismatchError):
            Learner.load_checkpoint(
                tampered, self.membership, alphabet=ALPHABET, max_len=MAX_LEN
            )


if __name__ == "__main__":
    unittest.main()
