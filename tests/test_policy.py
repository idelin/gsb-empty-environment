import os
import unittest

from cdc_masking import (
    MappingRegistry,
    PolicyError,
    PolicyStore,
    load_policy,
    load_schema,
)

FIXTURES = os.path.join(os.path.dirname(__file__), os.pardir, "fixtures")


def make_store(strict=True):
    schemas = load_schema(os.path.join(FIXTURES, "schema.json"))
    store = PolicyStore(load_policy(os.path.join(FIXTURES, "policy_v1.json")),
                        schemas, strict=strict)
    return store, schemas


class PolicyStoreTest(unittest.TestCase):
    def test_resolve_returns_event_time_version(self):
        store, _ = make_store()
        registry = MappingRegistry()
        store.hot_update(load_policy(os.path.join(FIXTURES, "policy_v3.json")),
                         registry, valid_from_seq=10)
        self.assertEqual(store.resolve(1).version, 1)
        self.assertEqual(store.resolve(9).version, 1)
        self.assertEqual(store.resolve(10).version, 3)
        self.assertEqual(store.resolve(99).version, 3)

    def test_strict_hot_update_rejects_missing_sensitive_coverage(self):
        store, schemas = make_store()
        schemas.evolve("users", {"phone": True})
        registry = MappingRegistry()
        registry.get_or_create(store.active, ("users", "id"), "u1")
        with self.assertRaises(PolicyError) as ctx:
            store.hot_update(load_policy(os.path.join(FIXTURES, "policy_v2.json")),
                             registry, valid_from_seq=10)
        self.assertIn("users.phone", str(ctx.exception))
        # old version stays active, no partial migration happened
        self.assertEqual(store.active.version, 1)
        self.assertEqual(registry.versions(), [1])

    def test_non_strict_hot_update_allows_missing_coverage(self):
        store, schemas = make_store(strict=False)
        schemas.evolve("users", {"phone": True})
        registry = MappingRegistry()
        store.hot_update(load_policy(os.path.join(FIXTURES, "policy_v2.json")),
                         registry, valid_from_seq=10)
        self.assertEqual(store.active.version, 2)

    def test_duplicate_version_rejected_without_side_effects(self):
        store, _ = make_store()
        registry = MappingRegistry()
        with self.assertRaises(PolicyError):
            store.hot_update(load_policy(os.path.join(FIXTURES, "policy_v1.json")),
                             registry, valid_from_seq=10)
        self.assertEqual(store.active.version, 1)
        self.assertEqual(registry.versions(), [])

    def test_hot_update_migrates_mappings_atomically(self):
        store, _ = make_store()
        registry = MappingRegistry()
        v1 = store.active
        t1 = registry.get_or_create(v1, ("users", "id"), "u1")
        t2 = registry.get_or_create(v1, ("users", "email"), "a@b.c")
        store.hot_update(load_policy(os.path.join(FIXTURES, "policy_v3.json")),
                         registry, valid_from_seq=10)
        self.assertEqual(sorted(registry.versions()), [1, 3])
        # same raw values re-mapped under the new version's key
        v3 = store.active
        self.assertEqual(registry.snapshot(3)[(("users", "id"), "u1")],
                         v3.token(("users", "id"), "u1"))
        self.assertEqual(registry.snapshot(3)[(("users", "email"), "a@b.c")],
                         v3.token(("users", "email"), "a@b.c"))
        # old version mappings untouched
        self.assertEqual(registry.snapshot(1)[(("users", "id"), "u1")], t1)
        self.assertNotEqual(t1, v3.token(("users", "id"), "u1"))
        self.assertEqual(registry.resolve(3, v3.token(("users", "id"), "u1")), "u1")


if __name__ == "__main__":
    unittest.main()
