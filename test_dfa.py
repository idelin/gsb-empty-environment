"""unittest suite for the DFA minimization toolkit (Python 3.11, stdlib only)."""

import subprocess
import sys
import unittest
from pathlib import Path

from dfa import (
    DEAD,
    DFA,
    canonical_key,
    distinguishing_string,
    minimize,
    normalize,
    reachable_states,
    relabel,
    repair_partition,
    run,
    verify_partition,
)


def sample_dfa() -> DFA:
    # s5 unreachable; (s3, "b") missing -> dead state; s1 ~ s2 redundant.
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


class TestNormalization(unittest.TestCase):
    def test_unreachable_removed(self):
        nd = normalize(sample_dfa())
        self.assertNotIn("s5", nd.states)
        self.assertEqual(reachable_states(nd), set(nd.states))

    def test_missing_transitions_get_dead_state(self):
        nd = normalize(sample_dfa())
        self.assertIn(DEAD, nd.states)
        self.assertEqual(nd.transitions[("s3", "b")], DEAD)
        for a in nd.alphabet:
            self.assertEqual(nd.transitions[(DEAD, a)], DEAD)
        for s in nd.states:
            for a in nd.alphabet:
                self.assertIn((s, a), nd.transitions)

    def test_dead_state_not_added_when_complete(self):
        dfa = DFA(
            states=frozenset({"x"}),
            alphabet=frozenset({"a"}),
            transitions={("x", "a"): "x"},
            start="x",
            accepting=frozenset(),
        )
        self.assertNotIn(DEAD, normalize(dfa).states)


class TestMinimization(unittest.TestCase):
    def setUp(self):
        self.result = minimize(sample_dfa())

    def test_accepting_and_nonaccepting_never_merge(self):
        for block in self.result.blocks:
            has_acc = any(s in self.result.normalized.accepting for s in block)
            for s in block:
                self.assertEqual(s in self.result.normalized.accepting, has_acc)

    def test_redundant_states_merge(self):
        self.assertEqual(self.result.mapping["s1"], self.result.mapping["s2"])

    def test_mapping_covers_all_normalized_states(self):
        self.assertEqual(set(self.result.mapping), set(self.result.normalized.states))

    def test_certificate_witnesses_actually_distinguish(self):
        q = self.result.quotient
        for (x, y), w in self.result.certificate.items():
            self.assertIsNotNone(w)
            acc_x = run(q, x, w) in q.accepting
            acc_y = run(q, y, w) in q.accepting
            self.assertNotEqual(acc_x, acc_y, f"bad witness for {x},{y}")

    def test_certificate_covers_all_quotient_pairs(self):
        names = sorted(self.result.quotient.states)
        expected = {(names[i], names[j])
                    for i in range(len(names)) for j in range(i + 1, len(names))}
        self.assertEqual(set(self.result.certificate), expected)

    def test_witnesses_are_shortest(self):
        # q0 (={s0}) vs q1 (={s1,s2}): no length-1 string distinguishes them,
        # so the certificate witness must have length 2.
        q = self.result.quotient
        w = self.result.certificate[("q0", "q1")]
        self.assertEqual(len(w), 2)
        for a in q.alphabet:
            self.assertEqual(
                run(q, "q0", a) in q.accepting, run(q, "q1", a) in q.accepting)

    def test_empty_alphabet(self):
        dfa = DFA(
            states=frozenset({"p", "q"}),
            alphabet=frozenset(),
            transitions={},
            start="p",
            accepting=frozenset({"q"}),
        )
        # q is unreachable; only p remains, no dead state (no symbols).
        result = minimize(dfa)
        self.assertEqual(result.blocks, [frozenset({"p"})])
        self.assertEqual(result.certificate, {})

    def test_empty_alphabet_with_accepting_and_rejecting(self):
        dfa = DFA(
            states=frozenset({"p", "q", "r"}),
            alphabet=frozenset(),
            transitions={},
            start="p",
            accepting=frozenset({"p", "r"}),
        )
        # Only p reachable; r unreachable -> trimmed.
        result = minimize(dfa)
        self.assertEqual(result.blocks, [frozenset({"p"})])

    def test_empty_alphabet_two_reachable(self):
        dfa = DFA(
            states=frozenset({"p"}),
            alphabet=frozenset(),
            transitions={},
            start="p",
            accepting=frozenset({"p"}),
        )
        result = minimize(dfa)
        self.assertEqual(len(result.blocks), 1)


class TestDistinguishingString(unittest.TestCase):
    def test_equivalent_states_return_none(self):
        nd = normalize(sample_dfa())
        self.assertIsNone(distinguishing_string(nd, "s1", "s2"))

    def test_accept_vs_reject_is_empty_string(self):
        nd = normalize(sample_dfa())
        self.assertEqual(distinguishing_string(nd, "s4", "s3"), "")


class TestIncrementalRepair(unittest.TestCase):
    def setUp(self):
        self.base = sample_dfa()
        self.old_blocks = minimize(self.base).blocks

    def test_split_after_local_change(self):
        dfa = sample_dfa()
        dfa.transitions[("s2", "b")] = "s4"  # breaks s1 ~ s2
        rep = repair_partition(dfa, self.old_blocks)
        self.assertGreaterEqual(rep.splits, 1)
        full = minimize(dfa)
        self.assertEqual(set(rep.partition), set(full.blocks))
        self.assertNotEqual(full.mapping["s1"], full.mapping["s2"])

    def test_merge_after_local_change(self):
        dfa = sample_dfa()
        dfa.transitions[("s1", "a")] = "s4"   # s1 gets s3's transitions...
        del dfa.transitions[("s1", "b")]      # ...b now missing -> dead
        rep = repair_partition(dfa, self.old_blocks)
        self.assertGreaterEqual(rep.merges, 1)
        full = minimize(dfa)
        self.assertEqual(set(rep.partition), set(full.blocks))
        self.assertEqual(full.mapping["s1"], full.mapping["s3"])

    def test_no_change_is_stable(self):
        rep = repair_partition(sample_dfa(), self.old_blocks)
        self.assertEqual(rep.splits, 0)
        self.assertEqual(rep.merges, 0)
        self.assertEqual(set(rep.partition), set(self.old_blocks))

    def test_pairs_touched_reported(self):
        dfa = sample_dfa()
        dfa.transitions[("s2", "b")] = "s4"
        rep = repair_partition(dfa, self.old_blocks)
        self.assertGreater(rep.pairs_touched, 0)


class TestVerificationAndCanonicity(unittest.TestCase):
    def test_product_verification_agrees(self):
        dfa = sample_dfa()
        result = minimize(dfa)
        ok, checked, cex = verify_partition(dfa, result.blocks)
        self.assertTrue(ok, f"counterexample: {cex}")
        n = len(result.normalized.states)
        self.assertEqual(checked, n * (n - 1) // 2)

    def test_verification_detects_wrong_partition(self):
        dfa = sample_dfa()
        bad = [frozenset({"s0", "s1", "s2", "s3", "s4"}), frozenset({DEAD})]
        ok, _, cex = verify_partition(dfa, bad)
        self.assertFalse(ok)
        self.assertIsNotNone(cex)

    def test_relabeling_invariance(self):
        dfa = sample_dfa()
        renamed = relabel(dfa, lambda s: f"z_{s}" if isinstance(s, str) else s)
        self.assertEqual(canonical_key(dfa), canonical_key(renamed))

    def test_relabeling_invariance_with_permutation(self):
        dfa = sample_dfa()
        perm = {"s0": "t3", "s1": "t0", "s2": "t4", "s3": "t1",
                "s4": "t2", "s5": "t5"}
        renamed = relabel(dfa, lambda s: perm.get(s, s))
        self.assertEqual(canonical_key(dfa), canonical_key(renamed))


class TestCounterexampleScript(unittest.TestCase):
    def test_script_runs_standalone(self):
        proc = subprocess.run(
            [sys.executable, str(Path(__file__).parent / "counterexample.py")],
            capture_output=True, text=True, timeout=60,
        )
        self.assertEqual(proc.returncode, 0, proc.stderr)
        self.assertIn("counterexample", proc.stdout)

    def test_find_counterexample_function(self):
        from counterexample import find_counterexample
        dfa = sample_dfa()
        self.assertIsNone(find_counterexample(dfa, "s1", "s2"))
        witness = find_counterexample(dfa, "s1", "s3")
        self.assertIsNotNone(witness)
        nd = normalize(dfa)
        self.assertNotEqual(run(nd, "s1", witness) in nd.accepting,
                            run(nd, "s3", witness) in nd.accepting)


if __name__ == "__main__":
    unittest.main()
