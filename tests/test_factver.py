"""factver 需求测试（Python 3.11，仅标准库 + unittest）。"""
import unittest

from factver import (
    BatchCorrectionError,
    Correction,
    Engine,
    FactStore,
    Rule,
    UnknownFactError,
    VersionConflictError,
    replay_against_store,
    replay_from_evidence,
)


def make_engine() -> Engine:
    """合成数据：订单事实 + 三条规则（数值/存在性/通配）。"""
    store = FactStore()
    store.put("order-1", {
        "amount":   (100.0, "erp"),
        "quantity": (2, "erp"),
        "remark":   ("常规", "manual"),
        "discount": (0.9, "crm"),
    })
    engine = Engine(store)
    engine.register(Rule(
        "rule-total", 1,
        lambda r: r.value("order-1", "amount") * r.value("order-1", "quantity"),
    ))
    engine.register(Rule(
        "rule-has-discount", 1,
        lambda r: r.exists("order-1", "discount"),
    ))
    engine.register(Rule(
        "rule-digest", 1,
        lambda r: tuple(sorted(r.snapshot("order-1").items())),
    ))
    return engine


class TestVersioningAndProvenance(unittest.TestCase):
    def test_correction_keeps_old_versions_and_provenance(self):
        store = FactStore()
        store.put("f1", {"a": (1, "src-a"), "b": (2, "src-b")})
        store.correct(Correction("f1", changes={"a": (10, "src-fix")}))

        self.assertEqual(store.current_version("f1"), 2)
        v1, v2 = store.history("f1")
        # 旧版本完整保留
        self.assertEqual(v1.value("a"), 1)
        self.assertEqual(v1.source_of("a"), "src-a")
        # 更正字段：新值新来源；未触及字段：原值原来源
        self.assertEqual(v2.value("a"), 10)
        self.assertEqual(v2.source_of("a"), "src-fix")
        self.assertEqual(v2.value("b"), 2)
        self.assertEqual(v2.source_of("b"), "src-b")

    def test_stable_ids_and_versions(self):
        engine = make_engine()
        s1 = engine.run("rule-total")
        engine.apply_corrections([Correction("order-1", changes={"amount": (5.0, "fix")})])
        s2 = engine.run("rule-total", step_id=s1.step_id)
        self.assertEqual(s1.step_id, s2.step_id)      # 步骤 ID 稳定
        self.assertEqual((s1.version, s2.version), (1, 2))
        self.assertEqual(s2.rule_id, "rule-total")
        self.assertEqual(s2.rule_version, 1)
        self.assertEqual(engine.store.current_version("order-1"), 2)

    def test_expected_version_conflict(self):
        store = FactStore()
        store.put("f1", {"a": (1, "s")})
        with self.assertRaises(VersionConflictError):
            store.correct(Correction("f1", changes={"a": (2, "s")}, expected_version=9))
        self.assertEqual(store.current_version("f1"), 1)


class TestFieldLevelInvalidation(unittest.TestCase):
    def test_remark_change_does_not_invalidate_numeric_rule(self):
        engine = make_engine()
        s_total = engine.run("rule-total")
        s_digest = engine.run("rule-digest")
        invalidated = engine.apply_corrections([
            Correction("order-1", changes={"remark": ("加急", "manual-edit")}),
        ])
        # 仅读数值的规则保持有效；通配读取视为依赖全部字段，失效
        self.assertEqual(engine.steps[s_total.step_id][-1].status, "valid")
        self.assertEqual(engine.steps[s_digest.step_id][-1].status, "invalid")
        self.assertEqual([s.step_id for s in invalidated], [s_digest.step_id])

    def test_only_changed_fields_invalidate(self):
        engine = make_engine()
        s_total = engine.run("rule-total")
        # 更正 quantity：total 读取了 quantity，失效
        engine.apply_corrections([Correction("order-1", changes={"quantity": (9, "fix")})])
        self.assertEqual(engine.steps[s_total.step_id][-1].status, "invalid")

    def test_same_value_correction_invalidates_nothing(self):
        engine = make_engine()
        s_total = engine.run("rule-total")
        s_digest = engine.run("rule-digest")
        invalidated = engine.apply_corrections([
            Correction("order-1", changes={"amount": (100.0, "erp-recheck")}),  # 值未变
        ])
        self.assertEqual(invalidated, [])
        self.assertEqual(engine.steps[s_total.step_id][-1].status, "valid")
        self.assertEqual(engine.steps[s_digest.step_id][-1].status, "valid")

    def test_field_removal_affects_existence_condition(self):
        engine = make_engine()
        s_flag = engine.run("rule-has-discount")
        s_total = engine.run("rule-total")
        engine.apply_corrections([Correction("order-1", removals=frozenset({"discount"}))])
        # 存在性条件失效；不读 discount 的数值规则不受影响
        self.assertEqual(engine.steps[s_flag.step_id][-1].status, "invalid")
        self.assertEqual(engine.steps[s_total.step_id][-1].status, "valid")
        self.assertFalse(engine.store.get("order-1").has("discount"))

    def test_field_addition_affects_existence_condition(self):
        store = FactStore()
        store.put("f1", {"a": (1, "s")})
        engine = Engine(store)
        engine.register(Rule("rule-check", 1, lambda r: r.exists("f1", "b")))
        s = engine.run("rule-check")
        self.assertFalse(s.output)
        engine.apply_corrections([Correction("f1", changes={"b": (7, "s2")})])
        self.assertEqual(engine.steps[s.step_id][-1].status, "invalid")
        s2 = engine.run("rule-check", step_id=s.step_id)
        self.assertTrue(s2.output)

    def test_value_rule_invalidated_when_read_field_removed(self):
        store = FactStore()
        store.put("f1", {"a": (3, "s")})
        engine = Engine(store)
        engine.register(Rule("rule-read", 1, lambda r: r.value("f1", "a") * 2))
        s = engine.run("rule-read")
        engine.apply_corrections([Correction("f1", removals=frozenset({"a"}))])
        self.assertEqual(engine.steps[s.step_id][-1].status, "invalid")


class TestBatchAtomicity(unittest.TestCase):
    def test_failed_batch_keeps_original_versions(self):
        engine = make_engine()
        s_total = engine.run("rule-total")
        before = engine.store.get("order-1")
        with self.assertRaises(UnknownFactError):
            engine.apply_corrections([
                Correction("order-1", changes={"amount": (120.0, "fix")}),
                Correction("ghost", changes={"x": (1, "s")}),  # 不存在 -> 整批失败
            ])
        after = engine.store.get("order-1")
        self.assertEqual(after.version, before.version)       # 保持原版本
        self.assertEqual(after.value("amount"), 100.0)
        self.assertEqual(engine.steps[s_total.step_id][-1].status, "valid")  # 无误伤

    def test_invalid_correction_rejected_atomically(self):
        store = FactStore()
        store.put("f1", {"a": (1, "s")})
        with self.assertRaises(BatchCorrectionError):
            store.batch_correct([
                Correction("f1", changes={"a": (2, "s")}, removals=frozenset({"a"})),
            ])
        self.assertEqual(store.current_version("f1"), 1)

    def test_successful_batch_applies_all(self):
        engine = make_engine()
        s_total = engine.run("rule-total")
        engine.apply_corrections([Correction(
            "order-1",
            changes={"amount": (120.0, "fix"), "quantity": (3, "fix")},
        )])
        fv = engine.store.get("order-1")
        self.assertEqual((fv.value("amount"), fv.value("quantity")), (120.0, 3))
        self.assertEqual(engine.steps[s_total.step_id][-1].status, "invalid")
        s2 = engine.run("rule-total", step_id=s_total.step_id)
        self.assertEqual(s2.output, 360.0)


class TestReplayableEvidence(unittest.TestCase):
    def test_all_step_versions_replay_against_store(self):
        engine = make_engine()
        steps = [engine.run(r) for r in ("rule-total", "rule-has-discount", "rule-digest")]
        engine.apply_corrections([
            Correction("order-1", changes={"remark": ("加急", "m"), "amount": (50.0, "fix")}),
        ])
        engine.apply_corrections([Correction("order-1", removals=frozenset({"discount"}))])
        for s in steps:  # 失效后重跑，产生多版本历史
            engine.run(engine.steps[s.step_id][-1].rule_id, step_id=s.step_id)
        all_recs = engine.all_steps()
        self.assertGreater(len(all_recs), len(steps))  # 确有历史版本
        for rec in all_recs:
            rule = engine.rules[rec.rule_id]
            report = replay_against_store(engine.store, rule, rec)
            self.assertTrue(report.ok, f"{rec.step_id} v{rec.version}: {report.detail}")

    def test_evidence_is_self_contained(self):
        engine = make_engine()
        for r in ("rule-total", "rule-has-discount", "rule-digest"):
            engine.run(r)
        engine.apply_corrections([Correction("order-1", removals=frozenset({"discount"}))])
        for rec in engine.all_steps():
            rule = engine.rules[rec.rule_id]
            report = replay_from_evidence(rule, rec)  # 不访问 store
            self.assertTrue(report.ok, f"{rec.step_id} v{rec.version}: {report.detail}")

    def test_tampered_evidence_detected(self):
        engine = make_engine()
        s = engine.run("rule-total")
        s.output = 999999  # 篡改证据
        report = replay_against_store(engine.store, engine.rules["rule-total"], s)
        self.assertFalse(report.ok)
        self.assertFalse(report.checks["output_matches"])


if __name__ == "__main__":
    unittest.main()
