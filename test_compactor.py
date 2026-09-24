"""unittest suite for boundary-aware change compaction."""

import json
import os
import unittest

from compactor import (
    CertificateError,
    ChainError,
    Change,
    build_certificate,
    check_equivalence,
    compact,
    exhaustive_verify,
    find_counterexample,
    load_fixture,
    replay_checkpoints,
    verify_certificate,
)

FIX = os.path.join(os.path.dirname(os.path.abspath(__file__)), "fixtures")


def fix(name):
    return load_fixture(os.path.join(FIX, name))


class CompactionTests(unittest.TestCase):

    def test_merge_within_transaction_keeps_earliest_before(self):
        # A->B->C merges to A->C; earliest before-value is preserved.
        changes, visible = fix("merge_within_tx.json")
        out = compact(changes, visible)["output"]
        self.assertEqual(len(out), 1)
        self.assertEqual((out[0].before, out[0].after), ("A", "C"))
        self.assertEqual(out[0].origin, (0, 1))
        self.assertTrue(check_equivalence(changes, out, visible).equivalent)

    def test_insert_then_delete_annihilates(self):
        changes, visible = fix("insert_then_delete.json")
        result = compact(changes, visible)
        self.assertEqual(result["output"], [])
        self.assertEqual(result["merges"], 1)

    def test_audit_mode_retains_evidence(self):
        changes, visible = fix("insert_then_delete.json")
        result = compact(changes, visible, audit=True)
        self.assertEqual(result["output"], [])
        evidence = result["audit_log"]
        self.assertEqual(len(evidence), 1)
        ev = evidence[0]
        self.assertEqual(ev["event"], "annihilated_insert_delete")
        self.assertEqual((ev["table"], ev["pk"], ev["value"]),
                         ("acct", "a2", "X"))
        self.assertEqual(ev["insert_origin"], [0])
        self.assertEqual(ev["delete_origin"], [1])

    def test_delete_recreate_preserves_identity(self):
        # delete followed by insert must NOT merge (new entity identity).
        changes, visible = fix("delete_recreate.json")
        out = compact(changes, visible)["output"]
        self.assertEqual(len(out), 2)
        self.assertEqual([c.kind for c in out], ["delete", "insert"])

    def test_visible_commit_blocks_cross_commit_merge(self):
        changes, visible = fix("cross_commit_visible.json")
        out = compact(changes, visible)["output"]
        self.assertEqual(len(out), 2)  # intermediate version stays visible
        report = check_equivalence(changes, out, visible)
        self.assertTrue(report.equivalent)

    def test_invisible_commit_allows_merge(self):
        changes, visible = fix("cross_commit_invisible.json")
        out = compact(changes, visible)["output"]
        self.assertEqual(len(out), 1)
        self.assertEqual((out[0].before, out[0].after), (None, "B"))
        self.assertEqual(out[0].commit_seq, 2)
        self.assertTrue(check_equivalence(changes, out, visible).equivalent)

    def test_schema_barrier_blocks_merge(self):
        changes, visible = fix("schema_barrier.json")
        out = compact(changes, visible)["output"]
        self.assertEqual(len(out), 2)
        self.assertEqual({c.schema for c in out}, {"v1", "v2"})

    def test_budget_exceeded_keeps_remainder_uncompressed(self):
        changes, visible = fix("budget.json")
        result = compact(changes, visible, budget=1)
        self.assertEqual(result["merges"], 1)
        self.assertEqual(len(result["output"]), 3)
        self.assertEqual((result["output"][0].before,
                          result["output"][0].after), (0, 2))
        # Uncompressed tail still reproduces the original semantics.
        self.assertTrue(
            check_equivalence(changes, result["output"], visible).equivalent)

    def test_before_value_mismatch_raises(self):
        bad = [
            Change("tx1", "t", "k", None, "A", 1),
            Change("tx1", "t", "k", "WRONG", "B", 1),
        ]
        with self.assertRaises(ChainError):
            compact(bad, [1])

    def test_non_monotonic_commits_raise(self):
        bad = [
            Change("tx2", "t", "k", None, "A", 2),
            Change("tx1", "t", "k", "A", "B", 1),
        ]
        with self.assertRaises(ChainError):
            compact(bad, [1, 2])

    def test_annihilate_then_reinsert_keeps_intermediate_invisible(self):
        # ins@1 del@1 (commit 1 visible, same tx -> net nothing), ins@2.
        stream = [
            Change("tx1", "t", "k", None, 0, 1),
            Change("tx1", "t", "k", 0, None, 1),
            Change("tx2", "t", "k", None, 0, 2),
        ]
        out = compact(stream, [1, 2])["output"]
        self.assertEqual(len(out), 1)  # only the commit-2 insert survives
        self.assertTrue(check_equivalence(stream, out, [1, 2]).equivalent)
        cps = replay_checkpoints(out, [1, 2])
        self.assertEqual(cps[0]["state"], {})      # absent at commit 1
        self.assertEqual(cps[1]["state"], {"t|k": 0})

    def test_three_commit_chain_partial_visibility(self):
        # c1 invisible, c2 visible: c1/c2 merge, c3 merge blocked.
        stream = [
            Change("tx1", "t", "k", None, "A", 1),
            Change("tx2", "t", "k", "A", "B", 2),
            Change("tx3", "t", "k", "B", "C", 3),
        ]
        out = compact(stream, [2, 3])["output"]
        self.assertEqual(len(out), 2)
        self.assertEqual((out[0].before, out[0].after, out[0].commit_seq),
                         (None, "B", 2))
        self.assertEqual(out[1].after, "C")
        self.assertTrue(check_equivalence(stream, out, [2, 3]).equivalent)


class CertificateTests(unittest.TestCase):

    def test_certificate_roundtrip(self):
        changes, visible = fix("aggregates.json")
        cert = build_certificate(changes, visible, audit=True)
        self.assertTrue(cert["equivalent"])
        blob = json.dumps(cert)
        self.assertTrue(verify_certificate(json.loads(blob)))

    def test_certificate_checkpoints_cover_all_visible_points(self):
        changes, visible = fix("aggregates.json")
        cert = build_certificate(changes, visible)
        labels = [cp["commit_seq"] for cp in cert["checkpoints"]]
        self.assertEqual(labels, [1, 2, "final"])
        self.assertEqual(cert["checkpoints"][0]["aggregates"],
                         {"row_count": 2, "total": 15.0})
        self.assertEqual(cert["checkpoints"][1]["state"], {"acct|n1": 7})

    def test_certificate_tampered_output_detected(self):
        changes, visible = fix("aggregates.json")
        cert = build_certificate(changes, visible)
        cert["output"][0]["after"] = 999
        with self.assertRaises(CertificateError):
            verify_certificate(cert)

    def test_certificate_tampered_option_detected(self):
        # Claiming a commit was invisible when it was not invalidates proof.
        changes, visible = fix("cross_commit_visible.json")
        cert = build_certificate(changes, visible)
        cert["options"]["visible_commit_points"] = [2]
        # digest recomputed by attacker -> replay itself must still reject it
        from compactor import _digest
        cert["certificate_digest"] = _digest(
            {k: v for k, v in cert.items() if k != "certificate_digest"})
        with self.assertRaises(CertificateError):
            verify_certificate(cert)


class CounterexampleAndExhaustiveTests(unittest.TestCase):

    def test_counterexample_same_final_different_intermediate(self):
        ce = find_counterexample()
        self.assertEqual(ce["final_state_a"], ce["final_state_b"])
        self.assertFalse(ce["naive_merge_equivalent"])
        self.assertTrue(any("commit 1" in d for d in ce["naive_merge_diffs"]))
        self.assertTrue(ce["boundary_aware_equivalent"])
        self.assertEqual(len(ce["boundary_aware_output"]), 2)

    def test_exhaustive_small_sequences(self):
        summary = exhaustive_verify(max_len=3, values=(0, 1), commits=(1, 2))
        self.assertEqual(summary["failures"], [])
        self.assertEqual(summary["cases"], 408)
        self.assertGreater(summary["naive_violations"], 0)


class IncrementalAggregationTests(unittest.TestCase):

    def test_aggregates_preserved_through_merge(self):
        # Same-commit merges must preserve downstream row_count/total effects.
        stream = [
            Change("tx1", "t", "a", None, 1, 1),
            Change("tx1", "t", "a", 1, 4, 1),   # insert+update -> insert 4
            Change("tx1", "t", "b", 2, 5, 1),
            Change("tx2", "t", "a", 4, None, 2),
        ]
        out = compact(stream, [1, 2])["output"]
        for p in ([1], [2], [1, 2]):
            self.assertEqual(
                replay_checkpoints(stream, p),
                replay_checkpoints(out, p),
            )


if __name__ == "__main__":
    unittest.main(verbosity=2)
