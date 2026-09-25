import os
import sys
import unittest

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

from shardstitch.model import Assumption, Fact, Proof, Rule, Shard, Step
from shardstitch.replay import ReplayError, replay
from shardstitch.stitch import (
    ASSUMPTION_CYCLE_NO_ROOT,
    BOUNDARY_DIGEST_MISMATCH,
    CONFLICTING_SHARD,
    REPLAY_FAILURE,
    UNGROUNDED_PROVIDER,
    UNSATISFIED_ASSUMPTION,
    Stitcher,
    _find_cycles,
)
from shardstitch.synthetic import (
    build_analytics,
    build_chain,
    build_ingest,
    build_mutual_assumption_pair,
    build_reporting,
)


def reasons(report):
    return [r.reason for r in report.rejections]


class ChainTest(unittest.TestCase):
    def test_chain_grounds_all_conclusions(self):
        stitcher = Stitcher()
        for shard in build_chain():
            stitcher.submit(shard)
        report = stitcher.stitch()
        self.assertEqual(report.rejections, [])
        self.assertEqual(
            set(report.grounded),
            {("batch.normalized", 1), ("summary.daily", 1), ("report.daily", 1)},
        )
        self.assertEqual(
            report.contributions, {"ingest": 1, "analytics": 1, "reporting": 1}
        )
        self.assertEqual(report.total_unique_proofs, 3)

    def test_evidence_is_replayable_and_digest_stable(self):
        ingest = build_ingest()
        proof = ingest.proofs[0]
        self.assertEqual(replay(proof, ingest.rules), "21.5C|22.0C")
        self.assertEqual(proof.digest(), proof.digest())
        rebuilt = build_ingest()
        self.assertEqual(rebuilt.proofs[0].digest(), proof.digest())
        self.assertEqual(rebuilt.content_digest(), ingest.content_digest())

    def test_versioned_entities_select_correct_rule_version(self):
        facts = (Fact("x", 1, "7"), Fact("y", 1, "8"))
        rule_v1 = Rule("rule.multi", 1, "join", "out", 1)
        rule_v2 = Rule("rule.multi", 2, "sum", "out", 2)
        step = Step("s", 1, "rule.multi", 2, (("x", 1), ("y", 1)), ("out", 2))
        proof = Proof("p", 1, ("out", 2), facts=facts, steps=(step,))
        shard = Shard("s", 1, rules=(rule_v1, rule_v2), proofs=(proof,))
        self.assertEqual(replay(proof, shard.rules), "15")
        stitcher = Stitcher()
        stitcher.submit(shard)
        self.assertIn(("out", 2), stitcher.stitch().grounded)


class RejectionTest(unittest.TestCase):
    def test_digest_mismatch_rejected(self):
        ingest = build_ingest()
        bad = build_analytics(ingest, expected_digest="f" * 64)
        stitcher = Stitcher()
        stitcher.submit(ingest)
        stitcher.submit(bad)
        report = stitcher.stitch()
        self.assertIn(BOUNDARY_DIGEST_MISMATCH, reasons(report))
        self.assertNotIn(("summary.daily", 1), report.grounded)
        self.assertEqual(report.contributions, {"ingest": 1})

    def test_unsatisfied_assumption_rejected(self):
        stitcher = Stitcher()
        stitcher.submit(build_analytics(build_ingest()))  # 未提交 ingest
        report = stitcher.stitch()
        self.assertIn(UNSATISFIED_ASSUMPTION, reasons(report))
        self.assertEqual(report.grounded, {})

    def test_mutual_assumptions_without_root_rejected(self):
        stitcher = Stitcher()
        for shard in build_mutual_assumption_pair():
            stitcher.submit(shard)
        report = stitcher.stitch()
        self.assertEqual(report.grounded, {})
        self.assertEqual(report.total_unique_proofs, 0)
        self.assertTrue(report.rejections)
        self.assertNotIn(UNSATISFIED_ASSUMPTION, reasons(report))

    def test_ungrounded_provider_propagates(self):
        # reporting 的摘要匹配，但提供方 analytics 自身假设未被满足
        analytics = build_analytics(build_ingest())
        reporting = build_reporting(analytics)
        stitcher = Stitcher()
        stitcher.submit(analytics)  # 故意不提交 ingest
        stitcher.submit(reporting)
        report = stitcher.stitch()
        self.assertIn(UNSATISFIED_ASSUMPTION, reasons(report))
        self.assertIn(UNGROUNDED_PROVIDER, reasons(report))
        self.assertEqual(report.grounded, {})

    def test_replay_failure_rejected(self):
        fact = Fact("x", 1, "1")
        rule = Rule("r", 1, "sum", "out", 1)
        bad_step = Step("s", 1, "r", 1, (("x", 1), ("ghost", 9)), ("out", 1))
        proof = Proof("p", 1, ("out", 1), facts=(fact,), steps=(bad_step,))
        stitcher = Stitcher()
        stitcher.submit(Shard("broken", 1, rules=(rule,), proofs=(proof,)))
        report = stitcher.stitch()
        self.assertIn(REPLAY_FAILURE, reasons(report))
        self.assertEqual(report.grounded, {})

    def test_conflicting_shard_version_rejected(self):
        stitcher = Stitcher()
        stitcher.submit(build_ingest())
        tampered = Shard("ingest", 1, rules=(), proofs=())
        stitcher.submit(tampered)
        report = stitcher.stitch()
        self.assertIn(CONFLICTING_SHARD, reasons(report))
        self.assertEqual(report.contributions, {"ingest": 1})


class DeduplicationTest(unittest.TestCase):
    def test_duplicate_shard_not_double_counted(self):
        chain = build_chain()
        stitcher = Stitcher()
        for shard in chain:
            stitcher.submit(shard)
        stitcher.submit(chain[0])
        stitcher.submit(build_ingest())  # 同内容重建也算重复
        report = stitcher.stitch()
        self.assertEqual(len(report.duplicates_ignored), 2)
        self.assertEqual(report.contributions["ingest"], 1)
        self.assertEqual(report.total_unique_proofs, 3)

    def test_identical_proof_in_two_shards_counted_once(self):
        ingest = build_ingest()
        mirror = Shard("mirror", 1, rules=ingest.rules, proofs=ingest.proofs)
        stitcher = Stitcher()
        stitcher.submit(ingest)
        stitcher.submit(mirror)
        report = stitcher.stitch()
        self.assertEqual(report.total_unique_proofs, 1)
        self.assertEqual(sum(report.contributions.values()), 1)


class CycleDetectionTest(unittest.TestCase):
    def test_two_node_cycle(self):
        self.assertEqual(
            _find_cycles({"a": {"b"}, "b": {"a"}}), [("a", "b")]
        )

    def test_self_loop(self):
        self.assertEqual(_find_cycles({"a": {"a"}}), [("a",)])

    def test_acyclic_graph_has_no_cycles(self):
        self.assertEqual(_find_cycles({"a": {"b"}, "b": {"c"}, "c": set()}), [])

    def test_cycle_with_rooted_member_is_grounded(self):
        # A 有局部根证明，B 假设 A，A 的另一证明再假设 B：有根，不构成无根环
        ingest = build_ingest()
        analytics = build_analytics(ingest)
        audit_rule = Rule("rule.audit", 1, "join", "batch.audit", 1)
        extra_proof = Proof(
            "proof.backedge", 1, ("batch.audit", 1),
            facts=(Fact("audit.tag", 1, "audited"),),
            assumptions=(Assumption(
                "assume.summary", "summary.daily", 1,
                "21.5C|22.0C|25C", analytics.proofs[0].digest(),
            ),),
            steps=(Step(
                "step.audit", 1, "rule.audit", 1,
                (("audit.tag", 1), ("summary.daily", 1)),
                ("batch.audit", 1),
            ),),
        )
        enriched = Shard(
            "ingest", 1,
            rules=ingest.rules + (audit_rule,),
            proofs=ingest.proofs + (extra_proof,),
        )
        stitcher = Stitcher()
        stitcher.submit(enriched)
        stitcher.submit(analytics)
        report = stitcher.stitch()
        self.assertIn(("batch.audit", 1), report.grounded)
        self.assertEqual(report.cycles, [])


if __name__ == "__main__":
    unittest.main()
