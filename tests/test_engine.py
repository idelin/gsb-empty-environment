import json
import os
import unittest

from cdc_masking import (
    ChangeEvent,
    MappingRegistry,
    MaskingEngine,
    MaskingError,
    PolicyStore,
    load_events,
    load_policy,
    load_schema,
)

FIXTURES = os.path.join(os.path.dirname(__file__), os.pardir, "fixtures")
RAW_SSN = "111-22-3333"
RAW_PHONE = "+86-13800000000"
RAW_EMAIL = "alice@example.com"


def make_engine(strict=True):
    schemas = load_schema(os.path.join(FIXTURES, "schema.json"))
    store = PolicyStore(load_policy(os.path.join(FIXTURES, "policy_v1.json")),
                        schemas, strict=strict)
    registry = MappingRegistry()
    engine = MaskingEngine(store, registry)
    events = load_events(os.path.join(FIXTURES, "changes.json"))
    return engine, store, registry, schemas, events


class ConsistentMaskingTest(unittest.TestCase):
    def setUp(self):
        self.engine, self.store, self.registry, self.schemas, self.events = make_engine()

    def test_same_value_before_after_maps_identically(self):
        ev = next(e for e in self.events if e.tx_id == "tx-2")
        masked = self.engine.process(ev)
        self.assertEqual(masked.before["email"], masked.after["email"])

    def test_pk_fk_mapping_is_stable_and_joinable(self):
        user = self.engine.process(self.events[0])
        order = self.engine.process(self.events[1])
        self.assertEqual(user.after["id"], order.after["user_id"])
        again = self.engine.process(self.events[0])
        self.assertEqual(again.after["id"], user.after["id"])

    def test_delete_with_only_old_keys_is_locatable(self):
        insert = self.engine.process(self.events[0])
        delete = next(e for e in self.events if e.op == "delete")
        masked_delete = self.engine.process(delete)
        self.assertIsNone(masked_delete.after)
        self.assertEqual(masked_delete.pk["id"], insert.after["id"])
        self.assertEqual(masked_delete.before["id"], insert.after["id"])

    def test_delete_resolved_with_event_time_policy_not_current_key(self):
        old_delete = next(e for e in self.events if e.op == "delete"
                          and e.commit_seq < 10)
        masked_before = self.engine.process(old_delete)
        self.store.hot_update(load_policy(os.path.join(FIXTURES, "policy_v3.json")),
                              self.registry, valid_from_seq=10)
        self.assertEqual(self.store.active.version, 3)
        masked_after = self.engine.process(old_delete)
        self.assertEqual(masked_after.policy_version, 1)
        self.assertEqual(masked_after.pk["id"], masked_before.pk["id"])
        self.assertTrue(masked_after.pk["id"].startswith("v1-"))

    def test_redacted_field_never_appears_in_output(self):
        masked = self.engine.process(self.events[0])
        self.assertIsNone(masked.after["ssn"])
        self.assertNotIn(RAW_SSN, json.dumps(masked.__dict__))
        self.assertNotIn(RAW_EMAIL, json.dumps(masked.__dict__))


class StrictModeTest(unittest.TestCase):
    def test_new_sensitive_field_without_policy_rejected(self):
        engine, store, registry, schemas, events = make_engine()
        schemas.evolve("users", {"phone": True})
        ev = next(e for e in events if e.commit_seq == 10)
        with self.assertRaises(MaskingError) as ctx:
            engine.process(ev)
        msg = str(ctx.exception)
        self.assertIn("users.phone", msg)
        self.assertNotIn(RAW_PHONE, msg)

    def test_error_log_contains_no_raw_values(self):
        engine, store, registry, schemas, events = make_engine()
        schemas.evolve("users", {"phone": True})
        for ev in events:
            try:
                engine.process(ev)
            except MaskingError:
                pass
        blob = json.dumps(engine.error_log)
        self.assertTrue(engine.error_log)
        for raw in (RAW_SSN, RAW_PHONE, RAW_EMAIL, "999-88-7777", "bob@example.com"):
            self.assertNotIn(raw, blob)

    def test_unexpected_error_is_sanitized(self):
        engine, store, registry, schemas, events = make_engine()
        def boom(pv, domain, raw):
            raise RuntimeError(f"backend failed on {raw!r}")
        registry.get_or_create = boom
        with self.assertRaises(MaskingError) as ctx:
            engine.process(events[0])
        self.assertNotIn(RAW_EMAIL, str(ctx.exception))
        self.assertNotIn(RAW_SSN, str(ctx.exception))
        self.assertNotIn(RAW_EMAIL, json.dumps(engine.error_log))

    def test_non_strict_mode_redacts_unmapped_sensitive_field(self):
        engine, store, registry, schemas, events = make_engine(strict=False)
        schemas.evolve("users", {"phone": True})
        ev = next(e for e in events if e.commit_seq == 10)
        masked = engine.process(ev)
        self.assertIsNone(masked.after["phone"])
        self.assertNotIn(RAW_PHONE, json.dumps(masked.__dict__))


if __name__ == "__main__":
    unittest.main()
