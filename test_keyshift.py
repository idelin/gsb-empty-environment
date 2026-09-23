"""keyshift 测试套件（Python 3.11，标准库 unittest）。"""
import os
import tempfile
import unittest

from keyshift import (ConflictError, Database, Event, apply_stream,
                      apply_transaction, load_fixture, verify_model)


def ev(tx, table, old, new, seq):
    return Event(tx_id=tx, table=table, old_key=old, new_key=new,
                 commit_seq=seq)


def state_of(db):
    """提取可比较的全量状态，用于断言回滚后完全复原。"""
    tables = {}
    for name, table in db.tables.items():
        tables[name] = {
            "index": dict(table.index),
            "entities": {eid: (e.pk, e.version, dict(e.data))
                         for eid, e in table.entities.items()},
            "history": {k: list(v) for k, v in table.key_history.items()},
        }
    refs = {k: set(v) for k, v in db.reverse_refs.items()}
    return tables, refs, db.commit_seq


class Base(unittest.TestCase):
    def setUp(self):
        self.db = Database()
        self.db.create_table("users", fks={"primary_order": "orders"})
        self.db.create_table("orders", fks={"owner_key": "users"})
        self.db.insert("users", "u1", {"name": "alice"})
        self.db.insert("users", "u2", {"name": "bob"})
        self.db.insert("users", "u3", {"name": "carol"})
        self.db.insert("orders", "o1", {"owner_key": "u1"})
        self.db.insert("orders", "o2", {"owner_key": "u2"})
        self.db.insert("orders", "o3", {"owner_key": "u3"})

    def assertClean(self, db=None):
        violations = verify_model(db or self.db)
        self.assertEqual(violations, [])


class TestPrimaryKeyMove(Base):
    def test_basic_move_updates_index(self):
        result = apply_transaction(self.db, "t1", [ev("t1", "users", "u1", "u10", 1)])
        self.assertEqual(result.status, "committed")
        self.assertIsNone(self.db.get("users", "u1"))       # 旧键不再命中
        self.assertEqual(self.db.get("users", "u10").data["name"], "alice")
        self.assertClean()

    def test_entity_id_and_version(self):
        entity_id = self.db.get("users", "u1").entity_id
        version = self.db.get("users", "u1").version
        apply_transaction(self.db, "t1", [ev("t1", "users", "u1", "u10", 1)])
        after = self.db.get("users", "u10")
        self.assertEqual(after.entity_id, entity_id)         # 稳定身份保留
        self.assertEqual(after.version, version + 1)

    def test_reverse_references_follow_move(self):
        apply_transaction(self.db, "t1", [ev("t1", "users", "u1", "u10", 1)])
        self.assertEqual(self.db.get("orders", "o1").data["owner_key"], "u10")
        self.assertClean()


class TestAtomicityAndConflicts(Base):
    def test_occupied_new_key_fails_whole_transaction(self):
        before = state_of(self.db)
        result = apply_transaction(self.db, "t1", [
            ev("t1", "users", "u3", "u30", 1),   # 本可成功
            ev("t1", "users", "u1", "u2", 2),    # 但 u2 被占用
        ])
        self.assertEqual(result.status, "failed")
        self.assertEqual(state_of(self.db), before)          # 无部分落库
        self.assertEqual(self.db.get("users", "u3").data["name"], "carol")
        self.assertClean()

    def test_missing_old_key_fails(self):
        result = apply_transaction(self.db, "t1",
                                   [ev("t1", "users", "nope", "x", 1)])
        self.assertEqual(result.status, "failed")
        self.assertClean()

    def test_swap_two_entities_in_one_transaction(self):
        alice = self.db.get("users", "u1").entity_id
        bob = self.db.get("users", "u2").entity_id
        result = apply_transaction(self.db, "t1", [
            ev("t1", "users", "u1", "u2", 1),
            ev("t1", "users", "u2", "u1", 2),
        ])
        self.assertEqual(result.status, "committed")
        self.assertEqual(self.db.get("users", "u1").entity_id, bob)
        self.assertEqual(self.db.get("users", "u2").entity_id, alice)
        self.assertEqual(self.db.get("orders", "o1").data["owner_key"], "u2")
        self.assertEqual(self.db.get("orders", "o2").data["owner_key"], "u1")
        self.assertClean()

    def test_three_way_rotation(self):
        ids = {k: self.db.get("users", k).entity_id for k in ("u1", "u2", "u3")}
        result = apply_transaction(self.db, "t1", [
            ev("t1", "users", "u1", "u2", 1),
            ev("t1", "users", "u2", "u3", 2),
            ev("t1", "users", "u3", "u1", 3),
        ])
        self.assertEqual(result.status, "committed")
        self.assertEqual(self.db.get("users", "u1").entity_id, ids["u3"])
        self.assertEqual(self.db.get("users", "u2").entity_id, ids["u1"])
        self.assertEqual(self.db.get("users", "u3").entity_id, ids["u2"])
        self.assertClean()

    def test_rollback_restores_state_on_mid_apply_failure(self):
        # 注入一条指向不存在实体的反向引用，强制级联阶段中途爆炸。
        self.db.reverse_refs.setdefault(("users", 0), set()).add(
            ("orders", 99999, "owner_key"))
        before = state_of(self.db)
        result = apply_transaction(self.db, "t1",
                                   [ev("t1", "users", "u1", "u10", 1)])
        self.assertEqual(result.status, "failed")
        self.assertIn("rolled back", result.reason)
        self.assertEqual(state_of(self.db), before)          # 撤销日志复原
        self.db.reverse_refs[("users", 0)].discard(("orders", 99999, "owner_key"))
        self.assertClean()


class TestIdempotencyAndVersionIsolation(Base):
    def test_duplicate_event_does_not_move_again(self):
        apply_transaction(self.db, "t1", [ev("t1", "users", "u1", "u10", 1)])
        version = self.db.get("users", "u10").version
        result = apply_transaction(self.db, "t1", [ev("t1", "users", "u1", "u10", 1)])
        self.assertEqual(result.status, "skipped")
        self.assertEqual(self.db.get("users", "u10").version, version)
        self.assertEqual(self.db.isolated_events[-1][1], "duplicate")
        self.assertClean()

    def test_late_event_on_old_key_isolated_by_version(self):
        apply_transaction(self.db, "t1", [ev("t1", "users", "u1", "u10", 5)])
        result = apply_transaction(self.db, "t0", [ev("t0", "users", "u1", "u99", 2)])
        self.assertEqual(result.status, "skipped")
        self.assertEqual(self.db.isolated_events[-1][1], "stale-version")
        self.assertIsNone(self.db.get("users", "u99"))
        self.assertEqual(self.db.get("users", "u10").data["name"], "alice")
        history = self.db.tables["users"].key_history["u1"]
        self.assertEqual(history[-1][1], self.db.get("users", "u10").entity_id)
        self.assertClean()

    def test_stream_groups_and_orders_transactions(self):
        results = apply_stream(self.db, [
            ev("t1", "users", "u1", "u2", 1),
            ev("t1", "users", "u2", "u1", 2),
            ev("t2", "users", "u3", "u30", 3),
        ])
        self.assertEqual([r.status for r in results], ["committed", "committed"])
        self.assertEqual(self.db.commit_seq, 3)
        self.assertClean()


class TestStableIdentity(Base):
    def test_ids_never_reused_after_delete(self):
        doomed = self.db.get("users", "u3").entity_id
        self.db.delete("orders", "o3")          # 先解除引用
        self.db.delete("users", "u3")
        self.assertIn(doomed, self.db.retired_ids)
        new_id = self.db.insert("users", "u3", {"name": "carol-2"})
        self.assertNotEqual(new_id, doomed)
        self.assertGreater(new_id, doomed)
        self.assertClean()

    def test_delete_referenced_entity_rejected(self):
        with self.assertRaises(ConflictError):
            self.db.delete("users", "u1")
        self.assertClean()


class TestCyclicReferencesAndSnapshots(Base):
    def test_cyclic_references_stay_consistent(self):
        self.db.update_fk("users", "u1", "primary_order", "o1")  # u1 <-> o1
        self.assertClean()
        apply_transaction(self.db, "t1", [
            ev("t1", "users", "u1", "u2", 1),
            ev("t1", "users", "u2", "u1", 2),
        ])
        # 环上的前向外键与反向引用在交换后仍互为镜像
        self.assertEqual(self.db.get("orders", "o1").data["owner_key"], "u2")
        self.assertEqual(self.db.get("users", "u2").data["primary_order"], "o1")
        self.assertClean()

    def test_every_snapshot_is_consistent(self):
        self.db.update_fk("users", "u1", "primary_order", "o1")
        events = [
            ev("t1", "users", "u1", "u2", 1),
            ev("t1", "users", "u2", "u1", 2),
            ev("t2", "users", "u3", "u1", 3),   # 冲突，整笔失败
            ev("t3", "users", "u3", "u30", 4),
        ]
        snapshots = [self.db.snapshot()]
        results = apply_stream(self.db, events)
        self.assertEqual([r.status for r in results],
                         ["committed", "failed", "committed"])
        snapshots += [self.db.snapshot() for _ in results]
        for snap in snapshots:
            self.assertEqual(verify_model(snap), [])
        # 快照是冻结的：后续变更不回渗
        self.assertEqual(snapshots[0].get("users", "u1").data["name"], "alice")


class TestFixtures(Base):
    def test_load_fixture_and_apply_end_to_end(self):
        path = os.path.join(os.path.dirname(__file__),
                            "fixtures", "sample_events.json")
        events = load_fixture(path)
        self.assertEqual(len(events), 6)
        results = apply_stream(self.db, events)
        self.assertEqual([r.status for r in results],
                         ["committed", "failed", "committed", "skipped"])
        self.assertIsNone(self.db.get("users", "u3"))
        self.assertEqual(self.db.get("orders", "o3").data["owner_key"], "u30")
        self.assertClean()

    def test_fixture_validation_rejects_bad_records(self):
        with tempfile.NamedTemporaryFile("w", suffix=".json",
                                         delete=False) as fh:
            fh.write('{"events": [{"tx_id": "t1", "table": "users"}]}')
            path = fh.name
        try:
            with self.assertRaises(ValueError):
                load_fixture(path)
        finally:
            os.unlink(path)


if __name__ == "__main__":
    unittest.main()
