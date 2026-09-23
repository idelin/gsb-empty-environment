import unittest

from cdc import (
    OP_DELETE,
    OP_INSERT,
    OP_UPDATE,
    FixtureSource,
    Replicator,
    TableStore,
)


def seed_source():
    """源库初始三行，随后一笔水位前更新。"""
    src = FixtureSource()
    src.apply_change("users", 1, OP_INSERT, after={"name": "alice", "balance": 100})
    src.apply_change("users", 2, OP_INSERT, after={"name": "bob", "balance": 50})
    src.apply_change("users", 3, OP_INSERT, after={"name": "carol", "balance": 30})
    src.apply_change(
        "users", 1, OP_UPDATE,
        before={"name": "alice", "balance": 100},
        after={"name": "alice", "balance": 120},
    )
    return src


def scan_time_changes(src):
    """快照扫描期间注入：更新已扫描行、删除一行、插入新行。"""
    def hook(scanned):
        if scanned == 1:
            src.apply_change(
                "users", 1, OP_UPDATE,
                before={"name": "alice", "balance": 120},
                after={"name": "alice", "balance": 200},
            )
        elif scanned == 2:
            src.apply_change(
                "users", 2, OP_DELETE,
                before={"name": "bob", "balance": 50},
            )
        elif scanned == 3:
            src.apply_change(
                "users", 4, OP_INSERT,
                after={"name": "dave", "balance": 10},
            )
    return hook


class ReplicatorTest(unittest.TestCase):
    def test_snapshot_plus_replay_matches_source(self):
        """快照 + 水位后回放 = 源库最新状态，无漏变更。"""
        src = seed_source()
        target = TableStore()
        rep = Replicator(src, target)
        watermark = rep.run(during_scan=scan_time_changes(src))

        self.assertEqual(watermark, 4)  # 水位 = 快照开始时的提交序号
        self.assertEqual(rep.stats.applied, 3)  # 仅回放水位后 3 笔
        self.assertEqual(target.state(), src.live_state())

    def test_snapshot_updates_not_reapplied(self):
        """快照已包含的更新（commit_seq <= 水位）不得再次应用。"""
        src = seed_source()
        target = TableStore()
        rep = Replicator(src, target, overlap=10)  # 重叠拉取全部历史事件
        rep.run(during_scan=scan_time_changes(src))

        self.assertEqual(rep.stats.cached, 7)  # 4 笔历史 + 3 笔扫描期间
        self.assertEqual(rep.stats.skipped_before_watermark, 4)
        self.assertEqual(rep.stats.applied, 3)
        self.assertEqual(
            target.state()["users"][1], {"name": "alice", "balance": 200}
        )

    def test_delete_during_scan_is_eventually_deleted(self):
        """扫描期间删除的行，最终必须从目标库消失。"""
        src = seed_source()
        target = TableStore()
        Replicator(src, target).run(during_scan=scan_time_changes(src))

        users = target.state()["users"]
        self.assertNotIn(2, users)  # bob 在扫描期间被删除
        self.assertIn(4, users)     # dave 在扫描期间被插入

    def test_duplicate_delivery_not_double_applied(self):
        """同一事务重复投递，仅应用一次。"""
        src = seed_source()
        target = TableStore()
        rep = Replicator(src, target)
        duplicates = []

        def hook(scanned):
            if scanned == 1:
                ev = src.apply_change(
                    "users", 1, OP_UPDATE,
                    before={"name": "alice", "balance": 120},
                    after={"name": "alice", "balance": 200},
                )
                duplicates.append(ev)  # 同一事务将被重复投递

        rep.run(during_scan=hook, extra_events=duplicates)

        self.assertEqual(rep.stats.applied, 1)
        self.assertEqual(rep.stats.skipped_duplicate_txn, 1)
        self.assertEqual(target.state()["users"][1]["balance"], 200)

    def test_out_of_order_cache_replayed_by_commit_seq(self):
        """缓存中乱序的事件按提交序号回放，最终状态正确。"""
        from cdc import ChangeEvent

        src = seed_source()
        target = TableStore()
        rep = Replicator(src, target)
        rep.run()

        # 水位后的两笔事件乱序到达缓存
        e1 = ChangeEvent("txn-a", "users", 4, OP_INSERT,
                         None, {"name": "dave", "balance": 11}, 100)
        e2 = ChangeEvent("txn-b", "users", 4, OP_UPDATE,
                         {"name": "dave", "balance": 11},
                         {"name": "dave", "balance": 99}, 101)
        rep.run(extra_events=[e2, e1])
        self.assertEqual(target.state()["users"][4]["balance"], 99)

    def test_snapshot_failure_publishes_no_mixed_state(self):
        """快照失败：目标库保持旧状态，不出现半新半旧。"""
        src = seed_source()
        target = TableStore()
        # 先成功发布一版旧状态
        Replicator(src, target).run()
        old_state = target.state()

        # 源库继续变更，然后快照失败
        src.apply_change(
            "users", 5, OP_INSERT, after={"name": "erin", "balance": 5}
        )
        src.fail_snapshot = True
        rep = Replicator(src, target)
        result = rep.run(during_scan=scan_time_changes(src))

        self.assertIsNone(result)
        self.assertEqual(target.state(), old_state)  # 原状保持
        self.assertNotIn(5, target.state().get("users", {}))

    def test_second_run_incremental_only(self):
        """首轮发布后源库继续变更，第二轮仅回放增量。"""
        src = seed_source()
        target = TableStore()
        rep = Replicator(src, target)
        rep.run(during_scan=scan_time_changes(src))

        src.apply_change(
            "users", 3, OP_UPDATE,
            before={"name": "carol", "balance": 30},
            after={"name": "carol", "balance": 31},
        )
        rep.run()
        self.assertEqual(target.state(), src.live_state())


if __name__ == "__main__":
    unittest.main()
