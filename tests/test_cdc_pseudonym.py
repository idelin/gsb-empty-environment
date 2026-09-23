import unittest

from cdc_pseudonym import (
    CdcEvent,
    Engine,
    FieldPolicy,
    PolicySet,
    Reference,
    TableSchema,
)

SECRET = b"test-secret"


def make_policy(version, *, strict=True, extra_fields=None, extra_sensitive=()):
    customer_fields = {
        "id": FieldPolicy("pseudonym", domain="customer_id"),
        "name": FieldPolicy("pseudonym"),
        "ssn": FieldPolicy("invisible"),
        "tier": FieldPolicy("plain"),
    }
    customer_fields.update(extra_fields or {})
    return PolicySet(
        version=version,
        strict=strict,
        fields={
            "customer": customer_fields,
            "orders": {
                "order_id": FieldPolicy("pseudonym"),
                "customer_id": FieldPolicy("pseudonym", domain="customer_id"),
                "note": FieldPolicy("redact"),
            },
        },
        schemas={
            "customer": TableSchema(
                frozenset({"id", "name", "ssn"} | set(extra_sensitive))
            ),
            "orders": TableSchema(frozenset({"order_id", "customer_id"})),
        },
    )


def ev(txid, table, pk, before, after, seq):
    return CdcEvent(txid=txid, table=table, pk=pk, before=before,
                    after=after, commit_seq=seq)


def insert_customer(txid="t1", cid="C-1", name="Alice", seq=1):
    return ev(txid, "customer", {"id": cid}, None,
              {"id": cid, "name": name, "ssn": "111-22-3333", "tier": "gold"},
              seq)


def insert_order(txid="t2", oid="O-1", cid="C-1", seq=2):
    return ev(txid, "orders", {"order_id": oid}, None,
              {"order_id": oid, "customer_id": cid, "note": "gift"}, seq)


class ConsistentMaskingTest(unittest.TestCase):
    def test_same_value_same_pseudonym_in_before_and_after(self):
        engine = Engine(make_policy(1), SECRET)
        out = engine.process(ev(
            "t1", "customer", {"id": "C-1"},
            {"id": "C-1", "name": "Alice", "ssn": "x", "tier": "gold"},
            {"id": "C-1", "name": "Alice", "ssn": "x", "tier": "platinum"}, 1))
        self.assertEqual(out["before"]["name"], out["after"]["name"])
        self.assertEqual(out["before"]["id"], out["after"]["id"])

    def test_pk_fk_share_stable_mapping_via_domain(self):
        engine = Engine(make_policy(1), SECRET)
        cust = engine.process(insert_customer())
        order = engine.process(insert_order())
        self.assertEqual(cust["after"]["id"], order["after"]["customer_id"])
        again = engine.process(insert_customer(txid="t3", seq=3))
        self.assertEqual(cust["after"]["id"], again["after"]["id"])

    def test_redact_and_plain_and_invisible_strategies(self):
        engine = Engine(make_policy(1), SECRET)
        out = engine.process(insert_order())
        self.assertEqual(out["after"]["note"], "REDACTED")
        cust = engine.process(insert_customer(txid="t3", seq=3))
        self.assertEqual(cust["after"]["tier"], "gold")
        self.assertNotIn("ssn", cust["after"])
        self.assertNotIn("ssn", cust["pk"])


class DeleteResolutionTest(unittest.TestCase):
    def test_delete_with_only_old_keys_is_locatable(self):
        engine = Engine(make_policy(1), SECRET)
        insert = engine.process(insert_customer())
        delete = engine.process(
            ev("t2", "customer", {"id": "C-1"}, {"id": "C-1"}, None, 2))
        self.assertEqual(delete["op"], "delete")
        self.assertEqual(delete["before"]["id"], insert["after"]["id"])

    def test_historical_delete_uses_policy_at_event_time_not_current_key(self):
        engine = Engine(make_policy(1), SECRET)
        insert = engine.process(insert_customer(seq=1))
        self.assertTrue(engine.reload_policy(make_policy(2), effective_seq=100))
        # Late-arriving delete from before the reload (commit_seq 2).
        delete = engine.process(
            ev("t2", "customer", {"id": "C-1"}, {"id": "C-1"}, None, 2))
        self.assertEqual(delete["policy_version"], 1)
        self.assertEqual(delete["before"]["id"], insert["after"]["id"])
        # A delete after the reload resolves under v2 and differs.
        delete_v2 = engine.process(
            ev("t3", "customer", {"id": "C-1"}, {"id": "C-1"}, None, 101))
        self.assertEqual(delete_v2["policy_version"], 2)
        self.assertNotEqual(delete_v2["before"]["id"], insert["after"]["id"])


class HotReloadMigrationTest(unittest.TestCase):
    def test_atomic_migration_covers_keys_and_images(self):
        engine = Engine(make_policy(1), SECRET)
        engine.process(insert_customer())
        engine.process(insert_order())
        v1_store = engine._stores[1]
        self.assertTrue(engine.reload_policy(make_policy(2), effective_seq=100))
        v2_store = engine._stores[2]
        self.assertEqual(len(v1_store), len(v2_store))
        self.assertEqual(engine.output_version, 2)
        out = engine.process(insert_order(txid="t3", oid="O-1", cid="C-1", seq=101))
        cust_v2 = engine.process(insert_customer(txid="t4", seq=102))
        self.assertEqual(out["after"]["customer_id"], cust_v2["after"]["id"])

    def test_failed_migration_keeps_old_version_and_mappings(self):
        engine = Engine(make_policy(1), SECRET)
        engine.process(insert_customer())
        before_entries = dict(engine._stores[1]._entries)

        class BadValue:
            pass

        engine._stores[1]._entries[("customer.name", BadValue())] = "ps_x"
        self.assertFalse(engine.reload_policy(make_policy(2), effective_seq=100))
        self.assertEqual(engine.output_version, 1)
        self.assertIn("customer.name",
                      {k[0] for k in engine._stores[1]._entries})
        good = {k: v for k, v in engine._stores[1]._entries.items()
                if isinstance(k[1], str)}
        self.assertEqual(good, before_entries)
        self.assertEqual(engine.logger.records[-1]["code"], "migration_failed")


class StrictModeTest(unittest.TestCase):
    def test_new_sensitive_field_without_policy_rejected(self):
        policy = make_policy(1, extra_sensitive=("loyalty_no",))
        engine = Engine(policy, SECRET)
        out = engine.process(ev(
            "t1", "customer", {"id": "C-1"}, None,
            {"id": "C-1", "name": "Alice", "ssn": "x", "tier": "gold",
             "loyalty_no": "L-777"}, 1))
        self.assertIsNone(out)
        self.assertEqual(engine.output, [])
        record = engine.logger.records[-1]
        self.assertEqual(record["code"], "policy_violation")
        self.assertEqual(record["field"], "loyalty_no")

    def test_schema_evolution_adds_policy_and_accepts(self):
        engine = Engine(make_policy(1), SECRET)
        engine.process(insert_customer())
        policy_v2 = make_policy(
            2,
            extra_fields={"phone": FieldPolicy("pseudonym")},
            extra_sensitive=("phone",),
        )
        self.assertTrue(engine.reload_policy(policy_v2, effective_seq=100))
        out = engine.process(ev(
            "t2", "customer", {"id": "C-1"},
            {"id": "C-1", "name": "Alice", "ssn": "x", "tier": "gold"},
            {"id": "C-1", "name": "Alice", "ssn": "x", "tier": "gold",
             "phone": "+86-138"}, 101))
        self.assertIsNotNone(out)
        self.assertTrue(out["after"]["phone"].startswith("ps_"))


class NoLeakTest(unittest.TestCase):
    RAW = "Alice-SECRET-999"

    def test_error_paths_never_leak_raw_values(self):
        policy = make_policy(1, extra_sensitive=("loyalty_no",))
        engine = Engine(policy, SECRET)
        engine.process(ev(
            "t1", "customer", {"id": "C-1"}, None,
            {"id": "C-1", "name": self.RAW, "ssn": "x", "tier": "gold",
             "loyalty_no": "L-777"}, 1))
        blob = repr(engine.logger.records)
        self.assertNotIn(self.RAW, blob)
        self.assertNotIn("L-777", blob)
        self.assertNotIn("111-22-3333", blob)

    def test_invisible_field_absent_from_all_outputs(self):
        engine = Engine(make_policy(1), SECRET)
        engine.process(insert_customer(name=self.RAW))
        engine.process(ev("t2", "customer", {"id": "C-1"},
                          {"id": "C-1", "name": self.RAW, "ssn": "999",
                           "tier": "gold"},
                          None, 2))
        blob = repr(engine.output)
        self.assertNotIn("999", blob)
        self.assertNotIn("111-22-3333", blob)


class ReplayValidationTest(unittest.TestCase):
    REFERENCES = [Reference("orders", "customer_id", "customer", "id")]

    def build_events(self):
        return [
            insert_customer(seq=1),
            insert_order(seq=2),
            insert_customer(txid="t3", cid="C-2", name="Bob", seq=3),
            insert_order(txid="t4", oid="O-2", cid="C-2", seq=4),
            # crosses the v1 -> v2 reload at seq 100
            insert_customer(txid="t5", cid="C-1", name="Alice", seq=101),
            insert_order(txid="t6", oid="O-3", cid="C-1", seq=102),
        ]

    def make_engine(self):
        engine = Engine(make_policy(1), SECRET)
        engine.reload_policy(make_policy(2), effective_seq=100)
        return engine

    def test_cross_version_reference_consistency(self):
        engine = self.make_engine()
        report = engine.commit_replay(self.build_events(), self.REFERENCES)
        self.assertTrue(report.ok, report.failures)
        self.assertEqual(report.checked_references, 3)
        self.assertEqual(engine.output_version, 2)

    def test_invisible_fields_do_not_interfere(self):
        engine = self.make_engine()
        events = self.build_events()
        report = engine.commit_replay(events, self.REFERENCES)
        self.assertTrue(report.ok, report.failures)
        for out in engine.output:
            self.assertNotIn("ssn", repr(out))

    def test_failed_replay_keeps_old_output_version(self):
        engine = self.make_engine()
        good = self.build_events()
        self.assertTrue(engine.commit_replay(good, self.REFERENCES).ok)
        old_output = list(engine.output)
        dangling = insert_order(txid="t9", oid="O-9", cid="C-404", seq=103)
        report = engine.commit_replay(good + [dangling], self.REFERENCES)
        self.assertFalse(report.ok)
        self.assertEqual(engine.output, old_output)
        self.assertEqual(engine.output_version, 2)
        self.assertEqual(engine.logger.records[-1]["code"],
                         "replay_validation_failed")


if __name__ == "__main__":
    unittest.main()
