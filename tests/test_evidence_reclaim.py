"""证据回收引擎测试（Python 3.11，仅标准库 unittest）。"""

import unittest

from evidence_reclaim.engine import ReclaimEngine, replay_certificate
from evidence_reclaim.events import EventError, EventLog
from evidence_reclaim.interleave import (
    OP_COMMIT,
    OP_NEW_REF,
    OP_UNFREEZE,
    enumerate_interleavings,
)
from evidence_reclaim.protection import (
    EDGE_FROZEN_INHERIT,
    EDGE_LIVE_REF,
    ROOT_FREEZE,
    ROOT_RETENTION,
)
from evidence_reclaim.scenario import FREEZE_ID, NEW_REF_SPEC, NOW, build_sample_engine


class ClosureTests(unittest.TestCase):
    def test_minimal_nonreclaimable_closure(self):
        engine = build_sample_engine()
        plan = engine.plan(NOW)
        candidates = {tuple(k) for k in plan.candidates}
        protected = {tuple(k) for k in plan.protected}
        self.assertEqual(candidates, {("ev5", 1), ("ev6", 1)})
        self.assertIn(("ev2", 1), protected)   # 过期但被活结论引用
        self.assertIn(("ev3", 1), protected)   # 显式冻结
        self.assertIn(("ev4", 1), protected)   # 冻结继承
        self.assertIn(("ev1", 1), protected)
        self.assertIn(("ev7", 1), protected)

    def test_expired_but_live_referenced_chain(self):
        engine = build_sample_engine()
        chain = engine.blocking_chain(("ev2", 1), NOW)
        self.assertIsNotNone(chain)
        self.assertEqual([s.node for s in chain], [("ev1", 1), ("ev2", 1)])
        self.assertEqual(chain[0].reason, ROOT_RETENTION)
        self.assertEqual(chain[1].reason, EDGE_LIVE_REF)
        self.assertEqual(chain[1].via_ref, "r1")
        for step in chain:
            self.assertTrue(step.step_id.startswith("step:"))
            self.assertEqual(step.schema_version, 1)
            self.assertGreaterEqual(step.rule_version, 1)

    def test_freeze_inheritance_chain(self):
        engine = build_sample_engine()
        chain = engine.blocking_chain(("ev4", 1), NOW)
        self.assertEqual([s.node for s in chain], [("ev3", 1), ("ev4", 1)])
        self.assertEqual(chain[0].reason, ROOT_FREEZE)
        self.assertEqual(chain[1].reason, EDGE_FROZEN_INHERIT)
        self.assertEqual(chain[1].via_ref, "r2")

    def test_permanent_retention_never_expires(self):
        engine = build_sample_engine()
        plan = engine.plan(10**9)
        self.assertIn(("ev7", 1), plan.protected)
        self.assertNotIn(("ev7", 1), plan.candidates)

    def test_dry_run_does_not_mutate(self):
        engine = build_sample_engine()
        version_before = engine.log.version
        node_count = len(engine.log.store.nodes)
        engine.plan(NOW)
        engine.plan(NOW + 1)
        self.assertEqual(engine.log.version, version_before)
        self.assertEqual(len(engine.log.store.nodes), node_count)


class CommitTests(unittest.TestCase):
    def test_commit_reclaims_and_proves_liveness(self):
        engine = build_sample_engine()
        plan = engine.plan(NOW)
        cert, rejection = engine.commit(plan)
        self.assertIsNone(rejection)
        self.assertIsNotNone(cert)
        self.assertTrue(cert.liveness.ok)
        self.assertEqual(cert.liveness.dangling_refs, [])
        remaining = set(engine.log.store.nodes)
        self.assertNotIn(("ev5", 1), remaining)
        self.assertNotIn(("ev6", 1), remaining)
        self.assertIn(("ev2", 1), remaining)
        self.assertNotIn("r3", engine.log.store.refs)  # 随候选删除
        self.assertIn("r1", engine.log.store.refs)

    def test_new_reference_blocks_planned_deletion(self):
        engine = build_sample_engine()
        plan = engine.plan(NOW)
        engine.add_reference(
            NEW_REF_SPEC["ref_id"], NEW_REF_SPEC["source"],
            NEW_REF_SPEC["target"], NEW_REF_SPEC["created_at"],
        )
        cert, rejection = engine.commit(plan)
        self.assertIsNone(cert)
        self.assertTrue(any("ev5" in r for r in rejection.reasons))
        chains = rejection.blocked_chains["ev5#v1"]
        self.assertEqual(chains[-1]["node"], ["ev5", 1])
        self.assertEqual(chains[0]["node"], ["ev1", 1])

    def test_stale_plan_after_unrelated_event_requires_new_generation(self):
        engine = build_sample_engine()
        plan = engine.plan(NOW)
        engine.add_reference("r-extra", ("ev7", 1), ("ev1", 1), created_at=400)
        cert, rejection = engine.commit(plan)
        self.assertIsNone(cert)
        self.assertTrue(any("存储版本已变化" in r for r in rejection.reasons))
        self.assertEqual(rejection.new_generation_hint, 2)
        fresh = engine.plan(NOW)
        cert, rejection = engine.commit(fresh)
        self.assertIsNone(rejection)
        self.assertIsNotNone(cert)

    def test_unfreeze_recomputes_closure(self):
        engine = build_sample_engine()
        self.assertTrue(engine.protection(NOW).is_protected(("ev3", 1)))
        engine.revoke_freeze(FREEZE_ID)
        plan = engine.plan(NOW)
        candidates = {tuple(k) for k in plan.candidates}
        self.assertIn(("ev3", 1), candidates)
        self.assertIn(("ev4", 1), candidates)  # 继承链消失

    def test_dangling_reference_rejected(self):
        engine = build_sample_engine()
        plan = engine.plan(NOW)
        cert, _ = engine.commit(plan)
        self.assertIsNotNone(cert)
        with self.assertRaises(EventError):
            engine.add_reference("r-bad", ("ev1", 1), ("ev5", 1), created_at=700)

    def test_double_commit_rejected(self):
        engine = build_sample_engine()
        plan = engine.plan(NOW)
        cert, _ = engine.commit(plan)
        self.assertIsNotNone(cert)
        cert2, rejection = engine.commit(plan)
        self.assertIsNone(cert2)
        self.assertTrue(rejection.reasons)

    def test_tampered_plan_rejected(self):
        engine = build_sample_engine()
        plan = engine.plan(NOW)
        plan.candidates = [("ev1", 1)]  # 篡改删除集
        cert, rejection = engine.commit(plan)
        self.assertIsNone(cert)
        self.assertTrue(any("篡改" in r for r in rejection.reasons))


class ReplayTests(unittest.TestCase):
    def test_log_replay_state_root(self):
        engine = build_sample_engine()
        engine.plan(NOW)  # 不写日志
        plan = engine.plan(NOW)
        cert, _ = engine.commit(plan)
        rebuilt = EventLog.from_records(engine.log.event_records())
        self.assertEqual(rebuilt.store.state_root(), engine.log.store.state_root())
        self.assertEqual(rebuilt.version, engine.log.version)

    def test_certificate_replay_verification(self):
        engine = build_sample_engine()
        plan = engine.plan(NOW)
        cert, _ = engine.commit(plan)
        problems = replay_certificate(engine, cert)
        self.assertEqual(problems, [])

    def test_interleaving_permutations(self):
        engine = build_sample_engine()
        plan = engine.plan(NOW)
        scenarios = enumerate_interleavings(engine, plan, NOW, NEW_REF_SPEC, FREEZE_ID)
        self.assertEqual(len(scenarios), 6)
        for scenario in scenarios:
            self.assertTrue(scenario.liveness_ok)
        by_order = {tuple(s.order): s for s in scenarios}
        # 提交最先：删除成功，之后指向被删节点的新引用必被拒绝
        first_commit = by_order[(OP_COMMIT, OP_NEW_REF, OP_UNFREEZE)]
        self.assertEqual(first_commit.commit_status, "committed")
        new_ref_outcome = [o for o in first_commit.outcomes if o.op == OP_NEW_REF][0]
        self.assertFalse(new_ref_outcome.admitted)
        self.assertNotIn("ev5#v1", first_commit.remaining_nodes)
        # 严格版本校验：只有“提交最先”才成功；任何先前事件都会使计划过期
        for order, scenario in by_order.items():
            if order[0] == OP_COMMIT:
                self.assertEqual(scenario.commit_status, "committed", order)
                self.assertNotIn("ev5#v1", scenario.remaining_nodes)
            else:
                self.assertEqual(scenario.commit_status, "rejected", order)
            # 新引用先于提交：拒绝原因必须点名被新引用保护的 ev5
            if order.index(OP_NEW_REF) < order.index(OP_COMMIT):
                self.assertTrue(
                    any("ev5" in r for r in scenario.commit_reasons), order
                )

    def test_replay_is_idempotent_and_stable(self):
        engine = build_sample_engine()
        plan_a = engine.plan(NOW)
        plan_b = engine.plan(NOW)
        self.assertNotEqual(plan_a.plan_id, plan_b.plan_id)  # 代际不同
        # 同内容同代际重算应得到相同阻塞链步骤 ID
        from evidence_reclaim.protection import compute_closure

        c1 = compute_closure(engine.log.store, NOW, 1)
        c2 = compute_closure(engine.log.store, NOW, 1)
        self.assertEqual(
            [s.step_id for s in c1.blocking_chain(("ev4", 1))],
            [s.step_id for s in c2.blocking_chain(("ev4", 1))],
        )


if __name__ == "__main__":
    unittest.main(verbosity=2)
