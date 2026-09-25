"""unittest suite for the factver package (synthetic data only)."""
import os
import sys
import unittest
from dataclasses import replace

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

from factver import (  # noqa: E402
    CorrectionError,
    DeleteField,
    Engine,
    FactStore,
    RuleRegistry,
    SetField,
    SetRemark,
)

ORDER = "order-1001"
CUSTOMER = "customer-77"


def make_store():
    store = FactStore()
    store.create(
        ORDER,
        fields={
            "qty": (3, "erp:orders"),
            "unit_price": (19.9, "erp:pricing"),
            "gift": ("mug", "promo:2026-q3"),
        },
        remarks={"note": "call before shipping"},
    )
    store.create(CUSTOMER, fields={"level": ("gold", "crm:profile")})
    return store


def make_engine(store):
    rules = RuleRegistry()
    rules.register("line-total", 1, lambda v: round(
        v["order"].require("qty") * v["order"].require("unit_price"), 2))
    rules.register("gift-check", 1, lambda v: {"has_gift": v["order"].has("gift")})
    rules.register("order-digest", 1, lambda v: v["order"].all())
    rules.register("remark-length", 1, lambda v: len(v["order"].remark("note", "")))
    rules.register("shipping", 1, lambda v: (
        "free" if v["customer"].require("level") == "gold" else "standard"))
    engine = Engine(store, rules)
    engine.run("line-total", {"order": ORDER})
    engine.run("gift-check", {"order": ORDER})
    engine.run("order-digest", {"order": ORDER})
    engine.run("remark-length", {"order": ORDER})
    engine.run("shipping", {"customer": CUSTOMER})
    return engine


class TestVersioningAndProvenance(unittest.TestCase):
    def test_versions_and_provenance_kept(self):
        store = make_store()
        store.correct(ORDER, [SetField("qty", 5, "erp:correction#T-1042")])
        self.assertEqual(store.current_version(ORDER), 2)
        v1 = store.get(ORDER, 1)
        v2 = store.get(ORDER, 2)
        self.assertEqual(v1.fields["qty"].value, 3)
        self.assertEqual(v1.fields["qty"].source, "erp:orders")
        self.assertEqual(v2.fields["qty"].value, 5)
        self.assertEqual(v2.fields["qty"].source, "erp:correction#T-1042")
        self.assertEqual(v2.fields["unit_price"].source, "erp:pricing")

    def test_stable_ids_for_fact_rule_step(self):
        store = make_store()
        engine = make_engine(store)
        step = engine.get_step("line-total[order=order-1001]")
        self.assertEqual(step.rule_id, "line-total")
        self.assertEqual(step.rule_version, 1)
        self.assertEqual(step.version, 1)
        store.correct(ORDER, [SetField("qty", 5, "erp:correction#T-1042")])
        step2 = engine.run("line-total", {"order": ORDER})
        self.assertEqual(step2.step_id, step.step_id)
        self.assertEqual(step2.version, 2)

    def test_unknown_fact_and_bad_version_rejected(self):
        store = make_store()
        with self.assertRaises(CorrectionError):
            store.get("nope")
        with self.assertRaises(CorrectionError):
            store.get(ORDER, 99)
        with self.assertRaises(CorrectionError):
            store.correct("nope", [SetField("a", 1, "s")])

    def test_non_serializable_value_rejected(self):
        store = make_store()
        with self.assertRaises(CorrectionError):
            store.correct(ORDER, [SetField("bad", object(), "s")])
        self.assertEqual(store.current_version(ORDER), 1)


class TestPreciseInvalidation(unittest.TestCase):
    def setUp(self):
        self.store = make_store()
        self.engine = make_engine(self.store)

    def stale(self):
        return set(self.engine.stale_steps())

    def test_only_readers_of_changed_field_invalidate(self):
        self.store.correct(ORDER, [SetField("qty", 5, "erp:correction#T-1042")])
        self.assertEqual(self.stale(), {
            "line-total[order=order-1001]",
            "order-digest[order=order-1001]",
        })

    def test_unrelated_fact_change_invalidates_nothing(self):
        self.store.correct(CUSTOMER, [SetField("level", "silver", "crm:update")])
        self.assertEqual(self.stale(), {"shipping[customer=customer-77]"})

    def test_wildcard_read_depends_on_all_fields(self):
        self.store.correct(ORDER, [SetField("gift", "cap", "promo:fix")])
        self.assertIn("order-digest[order=order-1001]", self.stale())
        self.assertNotIn("line-total[order=order-1001]", self.stale())

    def test_remark_change_spares_numeric_rules(self):
        self.store.correct(ORDER, [SetRemark("note", "leave at front desk")])
        self.assertEqual(self.stale(), {"remark-length[order=order-1001]"})

    def test_field_delete_hits_existence_condition(self):
        self.store.correct(ORDER, [DeleteField("gift")])
        stale = self.stale()
        self.assertIn("gift-check[order=order-1001]", stale)
        self.assertIn("order-digest[order=order-1001]", stale)
        self.assertNotIn("line-total[order=order-1001]", stale)

    def test_rederived_step_is_valid_again(self):
        self.store.correct(ORDER, [SetField("qty", 5, "erp:correction#T-1042")])
        step = self.engine.run("line-total", {"order": ORDER})
        self.assertFalse(self.engine.is_stale(step))
        self.assertEqual(step.output, 5 * 19.9)


class TestAtomicBatch(unittest.TestCase):
    def setUp(self):
        self.store = make_store()

    def test_failed_batch_keeps_original_versions(self):
        before = {fid: self.store.get(fid) for fid in (ORDER, CUSTOMER)}
        with self.assertRaises(CorrectionError):
            self.store.correct_batch({
                ORDER: [SetField("qty", 6, "erp:correction#T-1043")],
                CUSTOMER: [DeleteField("no-such-field")],
            })
        self.assertEqual(self.store.current_version(ORDER), 1)
        self.assertEqual(self.store.current_version(CUSTOMER), 1)
        for fid, snap in before.items():
            self.assertEqual(self.store.get(fid), snap)

    def test_failed_batch_unknown_fact(self):
        with self.assertRaises(CorrectionError):
            self.store.correct_batch({
                ORDER: [SetField("qty", 6, "erp:correction#T-1043")],
                "ghost": [SetField("x", 1, "s")],
            })
        self.assertEqual(self.store.current_version(ORDER), 1)

    def test_successful_batch_bumps_all(self):
        self.store.correct_batch({
            ORDER: [SetField("qty", 6, "erp:correction#T-1043")],
            CUSTOMER: [SetField("level", "platinum", "crm:update")],
        })
        self.assertEqual(self.store.current_version(ORDER), 2)
        self.assertEqual(self.store.current_version(CUSTOMER), 2)
        self.assertEqual(self.store.get(ORDER).fields["qty"].value, 6)
        self.assertEqual(self.store.get(CUSTOMER).fields["level"].value, "platinum")

    def test_conflicting_ops_rejected(self):
        with self.assertRaises(CorrectionError):
            self.store.correct(ORDER, [
                SetField("qty", 6, "s1"),
                SetField("qty", 7, "s2"),
            ])
        self.assertEqual(self.store.current_version(ORDER), 1)


class TestReplayableEvidence(unittest.TestCase):
    def setUp(self):
        self.store = make_store()
        self.engine = make_engine(self.store)

    def test_replay_ok_after_corrections(self):
        self.store.correct(ORDER, [SetField("qty", 5, "erp:correction#T-1042")])
        self.store.correct(ORDER, [DeleteField("gift")])
        result = self.engine.replay("line-total[order=order-1001]", version=1)
        self.assertTrue(result.ok, result.detail)
        self.assertEqual(result.expected_hash, result.actual_hash)

    def test_replay_detects_tampered_output(self):
        sid = "line-total[order=order-1001]"
        step = self.engine.get_step(sid)
        self.engine._steps[sid][-1] = replace(step, output=0.0)
        result = self.engine.replay(sid)
        self.assertFalse(result.ok)
        self.assertIn("output differs", result.detail)

    def test_evidence_hash_deterministic(self):
        a = self.engine.get_step("gift-check[order=order-1001]").evidence_hash
        engine2 = make_engine(make_store())
        b = engine2.get_step("gift-check[order=order-1001]").evidence_hash
        self.assertEqual(a, b)

    def test_replay_uses_recorded_rule_version(self):
        self.engine.registry.register("line-total", 2, lambda v: 0.0)
        result = self.engine.replay("line-total[order=order-1001]", version=1)
        self.assertTrue(result.ok, result.detail)


if __name__ == "__main__":
    unittest.main()
