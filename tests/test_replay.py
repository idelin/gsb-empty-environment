import os
import unittest
from dataclasses import replace

from cdc_masking import (
    MappingRegistry,
    MaskingEngine,
    PolicyStore,
    VersionedOutput,
    check_invisible_non_interference,
    load_events,
    load_policy,
    load_schema,
    validate_replay,
)

FIXTURES = os.path.join(os.path.dirname(__file__), os.pardir, "fixtures")


def build_cross_version():
    """v1 masks seq<10, hot update to v3 at seq 10, v3 masks the rest."""
    schemas = load_schema(os.path.join(FIXTURES, "schema.json"))
    store = PolicyStore(load_policy(os.path.join(FIXTURES, "policy_v1.json")),
                        schemas, strict=True)
    registry = MappingRegistry()
    engine = MaskingEngine(store, registry)
    events = load_events(os.path.join(FIXTURES, "changes.json"))
    masked_v1 = [engine.process(ev) for ev in events if ev.commit_seq < 10]
    schemas.evolve("users", {"phone": True})
    store.hot_update(load_policy(os.path.join(FIXTURES, "policy_v3.json")),
                     registry, valid_from_seq=10)
    masked_v3 = [engine.process(ev) for ev in events if ev.commit_seq >= 10]
    return store, registry, masked_v1 + masked_v3


class ReplayValidationTest(unittest.TestCase):
    def setUp(self):
        self.store, self.registry, self.masked = build_cross_version()

    def test_cross_version_references_are_consistent(self):
        report = validate_replay(self.masked, self.store, self.registry)
        self.assertTrue(report.ok, report.failures)
        self.assertEqual(report.versions, (1, 3))
        self.assertGreater(report.edges, 0)

    def test_invisible_fields_do_not_interfere(self):
        self.assertTrue(
            check_invisible_non_interference(self.masked, self.store, self.registry)
        )

    def test_tampered_fk_fails_validation_and_keeps_old_output(self):
        output = VersionedOutput()
        good = validate_replay(self.masked, self.store, self.registry)
        self.assertTrue(output.publish(3, self.masked, good))
        tampered = list(self.masked)
        victim = tampered[6]
        bad_after = dict(victim.after)
        bad_after["user_id"] = "v3-forgedtoken00000000"
        tampered[6] = replace(victim, after=bad_after)
        bad = validate_replay(tampered, self.store, self.registry)
        self.assertFalse(bad.ok)
        self.assertTrue(any("unresolvable fk token" in f for f in bad.failures))
        self.assertFalse(output.publish(4, tampered, bad))
        self.assertEqual(output.current_version, 3)
        self.assertEqual(output.output(), self.masked)

    def test_dangling_reference_detected(self):
        only_v3 = [m for m in self.masked if m.policy_version == 3]
        report = validate_replay(only_v3, self.store, self.registry)
        self.assertFalse(report.ok)
        self.assertTrue(any("dangling reference" in f for f in report.failures))

    def test_failure_messages_carry_no_raw_values(self):
        tampered = list(self.masked)
        victim = tampered[6]
        bad_after = dict(victim.after)
        bad_after["user_id"] = "v3-forgedtoken00000000"
        tampered[6] = replace(victim, after=bad_after)
        report = validate_replay(tampered, self.store, self.registry)
        for failure in report.failures:
            for raw in ("u1", "u2", "alice", "bob"):
                self.assertNotIn(raw, failure)


if __name__ == "__main__":
    unittest.main()
