import unittest

from causaldiff import (
    FACT_REMOVED,
    GROUP_INVALIDATED,
    GROUP_NEW_FACT,
    NEW_FACT,
    Snapshot,
    diff_snapshots,
    replay_derives,
)
from engine import Fact, Rule, run_engine
from sample_data import R2_V1, R2_V2, snapshot_t1, snapshot_t2

GRANT = "grant"
ALICE_REPO = (GRANT, ("alice", "repo_access"))
ALICE_PROD = (GRANT, ("alice", "prod_deploy"))
BOB_REPO = (GRANT, ("bob", "repo_access"))
BOB_ELIGIBLE = ("eligible_eng_tools", ("bob",))


class EngineTests(unittest.TestCase):
    def test_t1_derives_expected_conclusions(self):
        s = snapshot_t1()
        d = run_engine(s.facts, s.rules)
        self.assertIn(ALICE_REPO, d.supports)
        self.assertIn(ALICE_PROD, d.supports)
        self.assertNotIn(BOB_REPO, d.supports)

    def test_t2_derives_expected_conclusions(self):
        s = snapshot_t2()
        d = run_engine(s.facts, s.rules)
        self.assertIn(ALICE_REPO, d.supports)       # 保留（经 r2 v2）
        self.assertNotIn(ALICE_PROD, d.supports)    # f5 删除后失效
        self.assertIn(BOB_REPO, d.supports)         # 新事实推得

    def test_step_ids_stable_across_runs(self):
        s = snapshot_t1()
        d1 = run_engine(s.facts, s.rules)
        d2 = run_engine(s.facts, s.rules)
        self.assertEqual(sorted(d1.steps), sorted(d2.steps))
        self.assertTrue(all(sid.startswith("step-") for sid in d1.steps))

    def test_same_rule_id_different_version_distinguished(self):
        self.assertEqual(R2_V1.key[1], R2_V2.key[1])   # 同规则 ID
        self.assertNotEqual(R2_V1.key, R2_V2.key)      # 按版本区分


class DiffTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.a = snapshot_t1()
        cls.b = snapshot_t2()
        cls.report = diff_snapshots(cls.a, cls.b)

    def _gained(self, atom):
        return next(c for c in self.report.gained if c.atom == atom)

    def test_gained_conclusions_set(self):
        self.assertEqual(
            {c.atom for c in self.report.gained}, {BOB_REPO, BOB_ELIGIBLE}
        )

    def test_gained_repo_access_caused_by_new_facts_only(self):
        ch = self._gained(BOB_REPO)
        self.assertEqual(ch.groups, [GROUP_NEW_FACT])
        self.assertEqual(
            {e.key for e in ch.causal_slice},
            {("fact", "f7", 1), ("fact", "f8", 1), ("fact", "f9", 1)},
        )
        self.assertTrue(all(e.kind == NEW_FACT for e in ch.causal_slice))
        # 最小性：规则变更 r2v2 与新事实 f10 均非必要，不进入切片
        self.assertNotIn(("rule", "r2", 2), {e.key for e in ch.causal_slice})
        self.assertNotIn(("fact", "f10", 1), {e.key for e in ch.causal_slice})

    def test_lost_conclusion_caused_by_evidence_invalidation(self):
        self.assertEqual([c.atom for c in self.report.lost], [ALICE_PROD])
        ch = self.report.lost[0]
        self.assertEqual(ch.groups, [GROUP_INVALIDATED])
        self.assertEqual(len(ch.causal_slice), 1)
        elem = ch.causal_slice[0]
        self.assertEqual(elem.kind, FACT_REMOVED)
        self.assertEqual(elem.key, ("fact", "f5", 1))

    def test_same_result_support_changed_listed_separately(self):
        self.assertNotIn(ALICE_REPO, {c.atom for c in self.report.gained})
        self.assertNotIn(ALICE_REPO, {c.atom for c in self.report.lost})
        changes = [s for s in self.report.support_changed if s.atom == ALICE_REPO]
        self.assertEqual(len(changes), 1)
        sc = changes[0]
        self.assertEqual(sc.rule_version_changes, (("r2", 1, 2),))
        self.assertIn(("rule", "r2", 1), sc.removed_support)
        self.assertIn(("rule", "r2", 2), sc.added_support)
        self.assertIn(("fact", "f4", 1), sc.added_support)

    def test_irrelevant_inputs_excluded_from_explanations(self):
        explained = set()
        for ch in self.report.gained + self.report.lost:
            explained |= {e.key for e in ch.causal_slice}
        for key in [("fact", "f6", 1), ("fact", "f6", 2), ("fact", "f11", 1)]:
            self.assertNotIn(key, explained)
        self.assertEqual(len(self.report.irrelevant_inputs), 3)
        text = "\n".join(self.report.irrelevant_inputs)
        self.assertIn("f6", text)
        self.assertIn("f11", text)

    def test_all_slices_replayable(self):
        self.assertTrue(self.report.gained)
        self.assertTrue(self.report.lost)
        for ch in self.report.gained + self.report.lost:
            self.assertTrue(ch.replay_ok)
            self.assertTrue(ch.replay_steps)

    def test_slices_are_minimal(self):
        # 去掉切片中任一元素后，重放不再成立
        for ch in self.report.gained:
            objs = [e.obj for e in ch.causal_slice]
            for i in range(len(objs)):
                trial = objs[:i] + objs[i + 1:]
                self.assertFalse(replay_derives(self.a, trial, ch.atom))
        for ch in self.report.lost:
            objs = [e.obj for e in ch.causal_slice]
            for i in range(len(objs)):
                trial = objs[:i] + objs[i + 1:]
                self.assertFalse(replay_derives(self.b, trial, ch.atom))

    def test_deterministic_across_runs(self):
        r1 = diff_snapshots(self.a, self.b)
        r2 = diff_snapshots(self.a, self.b)
        self.assertEqual(
            [(c.atom, [e.key for e in c.causal_slice]) for c in r1.gained],
            [(c.atom, [e.key for e in c.causal_slice]) for c in r2.gained],
        )
        self.assertEqual(
            [(c.atom, [e.key for e in c.causal_slice]) for c in r1.lost],
            [(c.atom, [e.key for e in c.causal_slice]) for c in r2.lost],
        )


class RuleChangeCauseTests(unittest.TestCase):
    """规则变更（同 ID 新版本）作为新增结论的独立原因。"""

    def test_rule_change_causes_new_conclusion(self):
        rule_v1 = Rule("rr", 1, (("p", ("?x",)),), ("q", ("?x",)))
        rule_v2 = Rule("rr", 2, (("p", ("?x",)),), ("r", ("?x",)))
        fact = Fact("ff", 1, "p", ("a",))
        a = Snapshot("u1", (fact,), (rule_v1,))
        b = Snapshot("u2", (fact,), (rule_v2,))
        report = diff_snapshots(a, b)
        self.assertEqual([c.atom for c in report.gained], [("r", ("a",))])
        elem = report.gained[0].causal_slice[0]
        self.assertEqual(elem.kind, "rule_changed")
        self.assertEqual(elem.key, ("rule", "rr", 2))
        self.assertEqual(report.gained[0].groups, ["规则变更"])
        self.assertEqual([c.atom for c in report.lost], [("q", ("a",))])
        lost_elem = report.lost[0].causal_slice[0]
        self.assertEqual(lost_elem.key, ("rule", "rr", 1))


if __name__ == "__main__":
    unittest.main(verbosity=2)
