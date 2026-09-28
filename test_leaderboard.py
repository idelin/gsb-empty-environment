"""leaderboard 组件的 unittest 测试。所有数据均为合成数据。"""

import io
import random
import threading
import unittest
from unittest import mock

import leaderboard
from leaderboard import (
    CursorExpiredError,
    EntryNotFoundError,
    Leaderboard,
    UnknownCursorError,
)


class FakeClock:
    def __init__(self):
        self.now = 0.0

    def __call__(self):
        return self.now

    def advance(self, seconds):
        self.now += seconds


def drain(board, token, limit):
    """逐页取完一个游标，返回 (items, 每页列表)。"""
    pages = []
    items = []
    current = token
    while current is not None:
        page, current = board.fetch_page(current, limit)
        pages.append(page)
        items.extend(page)
    return items, pages


class AcceptanceBoundaryTest(unittest.TestCase):
    """验收边界：版本1为 a10 b9 c8，第一页 a；删除 b 后旧游标仍返回 b c，
    新游标返回 a c。"""

    def test_acceptance_boundary(self):
        board = Leaderboard(lease_seconds=60)
        board.set_score("a", 10)
        board.set_score("b", 9)
        board.set_score("c", 8)
        version_1 = board.current_version_id()

        old_token, pinned = board.open_cursor()
        self.assertEqual(pinned, version_1)

        # 第一页（页大小 1）为 a
        page1, next_token = board.fetch_page(old_token, 1)
        self.assertEqual(page1, [("a", 10)])
        self.assertIsNotNone(next_token)

        # 删除 b -> 新版本
        new_version = board.delete("b")
        self.assertGreater(new_version, version_1)

        # 旧游标继续分页：仍返回 b、c（不重复、不漏项）
        page2, next_token = board.fetch_page(next_token, 1)
        self.assertEqual(page2, [("b", 9)])
        page3, next_token = board.fetch_page(next_token, 1)
        self.assertEqual(page3, [("c", 8)])
        self.assertIsNone(next_token)

        # 新游标看到新版本：a c（无 b）
        fresh_token, fresh_version = board.open_cursor()
        self.assertEqual(fresh_version, new_version)
        items, _ = drain(board, fresh_token, 1)
        self.assertEqual(items, [("a", 10), ("c", 8)])


class OrderingTest(unittest.TestCase):
    def test_score_desc_id_asc(self):
        board = Leaderboard()
        board.set_score("b", 5)
        board.set_score("a", 5)   # 同分按 ID 升序：a 在 b 前
        board.set_score("c", 7)   # 分数高者优先
        board.set_score("d", 1)
        token, _ = board.open_cursor()
        items, _ = drain(board, token, 2)
        self.assertEqual([i for i, _ in items], ["c", "a", "b", "d"])

    def test_score_revision_publishes_new_version(self):
        board = Leaderboard()
        board.set_score("a", 1)
        board.set_score("b", 2)
        token, version = board.open_cursor()
        board.set_score("a", 100)  # 修订分数 -> 新版本
        old_items, _ = drain(board, token, 10)
        self.assertEqual(old_items, [("b", 2), ("a", 1)])  # 旧版本不变
        new_items = board.version_items(board.current_version_id())
        self.assertEqual(new_items, [("a", 100), ("b", 2)])
        self.assertNotEqual(version, board.current_version_id())

    def test_delete_missing_entry_raises(self):
        board = Leaderboard()
        with self.assertRaises(EntryNotFoundError):
            board.delete("nope")


class LeaseTest(unittest.TestCase):
    def test_expired_cursor_raises_explicitly(self):
        clock = FakeClock()
        board = Leaderboard(lease_seconds=10, clock=clock)
        board.set_score("a", 1)
        token, _ = board.open_cursor()
        clock.advance(11)
        with self.assertRaises(CursorExpiredError):
            board.fetch_page(token, 1)

    def test_expired_cursor_never_switches_version(self):
        clock = FakeClock()
        board = Leaderboard(lease_seconds=10, clock=clock)
        board.set_score("a", 1)
        token, old_version = board.open_cursor()
        board.set_score("b", 2)  # 产生新版本
        clock.advance(11)
        board.gc()  # 回收旧版本
        with self.assertRaises(CursorExpiredError):
            board.fetch_page(token, 1)
        # 报错而不是悄悄切到新版本
        self.assertNotEqual(old_version, board.current_version_id())

    def test_fetch_renews_lease(self):
        clock = FakeClock()
        board = Leaderboard(lease_seconds=10, clock=clock)
        for i in range(5):
            board.set_score(f"e{i}", i)
        token, _ = board.open_cursor()
        current = token
        seen = []
        for _ in range(5):
            clock.advance(9)  # 每次都在租约内取页并续租
            page, current = board.fetch_page(current, 1)
            seen.extend(page)
        self.assertEqual(len(seen), 5)
        self.assertIsNone(current)

    def test_gc_reclaims_unpinned_old_versions(self):
        clock = FakeClock()
        board = Leaderboard(lease_seconds=10, clock=clock)
        board.set_score("a", 1)   # v2
        board.set_score("b", 2)   # v3
        reclaimed = board.gc()
        self.assertEqual(reclaimed, [1, 2])  # 仅保留当前版本
        self.assertEqual(board.version_items(board.current_version_id()),
                         [("b", 2), ("a", 1)])

    def test_gc_keeps_pinned_version_until_lease_expires(self):
        clock = FakeClock()
        board = Leaderboard(lease_seconds=10, clock=clock)
        board.set_score("a", 1)
        _, pinned = board.open_cursor()
        board.set_score("b", 2)
        self.assertNotIn(pinned, board.gc())  # 活跃游标钉住，不回收
        clock.advance(11)
        self.assertIn(pinned, board.gc())     # 租约过期后回收

    def test_unknown_cursor_raises(self):
        board = Leaderboard()
        with self.assertRaises(UnknownCursorError):
            board.fetch_page("cur-999", 1)


class ConcurrencyTest(unittest.TestCase):
    def test_concurrent_updates_and_paging_no_dup_no_skip(self):
        """多线程并发修订/删除时，已打开游标逐页结果必须等于打开时快照。"""
        board = Leaderboard(lease_seconds=300)
        for i in range(50):
            board.set_score(f"e{i:02d}", i)
        token, version = board.open_cursor()
        expected = board.version_items(version)

        stop = threading.Event()
        errors = []

        def writer(seed):
            rng = random.Random(seed)
            try:
                while not stop.is_set():
                    entry = f"e{rng.randrange(50):02d}"
                    if rng.random() < 0.7:
                        board.set_score(entry, rng.randrange(1000))
                    else:
                        try:
                            board.delete(entry)
                        except EntryNotFoundError:
                            pass
            except Exception as exc:  # noqa: BLE001
                errors.append(exc)

        writers = [threading.Thread(target=writer, args=(s,)) for s in range(4)]
        for t in writers:
            t.start()

        collected = []
        current = token
        while current is not None:
            page, current = board.fetch_page(current, 3)
            collected.extend(page)

        stop.set()
        for t in writers:
            t.join()

        self.assertEqual(errors, [])
        self.assertEqual(collected, expected)  # 无重复、无遗漏、顺序一致

    def test_new_cursor_during_writes_sees_consistent_version(self):
        """并发写入期间打开的每个新游标，其内容都是某个一致快照。"""
        board = Leaderboard(lease_seconds=300)
        board.set_score("x", 1)
        stop = threading.Event()

        def writer():
            i = 0
            while not stop.is_set():
                board.set_score(f"w{i % 20}", i)
                i += 1

        threads = [threading.Thread(target=writer) for _ in range(3)]
        for t in threads:
            t.start()
        try:
            for _ in range(30):
                token, version = board.open_cursor()
                items, _ = drain(board, token, 7)
                self.assertEqual(items, board.version_items(version))
                # 排序约束：(分数降序, ID 升序)
                keys = [(-score, entry) for entry, score in items]
                self.assertEqual(keys, sorted(keys))
        finally:
            stop.set()
            for t in threads:
                t.join()


class CliTest(unittest.TestCase):
    def run_cli(self, script):
        stdin = io.StringIO(script)
        stdout = io.StringIO()
        with mock.patch("sys.stdin", stdin), mock.patch("sys.stdout", stdout):
            rc = leaderboard.main(["--lease-seconds", "60"])
        self.assertEqual(rc, 0)
        return stdout.getvalue().splitlines()

    def test_cli_acceptance_flow(self):
        lines = self.run_cli(
            "set a 10\nset b 9\nset c 8\nopen\npage cur-1 1\n"
            "delete b\npage cur-1 1\npage cur-1 1\n"
            "open\npage cur-2 10\nsnapshot\nquit\n"
        )
        self.assertEqual(lines[0:3], ["OK v2", "OK v3", "OK v4"])
        self.assertEqual(lines[3], "CURSOR cur-1 v4")
        self.assertEqual(lines[4], "PAGE a:10 NEXT cur-1")
        self.assertEqual(lines[5], "OK v5")
        self.assertEqual(lines[6], "PAGE b:9 NEXT cur-1")   # 旧游标仍见 b
        self.assertEqual(lines[7], "PAGE c:8 NEXT END")
        self.assertEqual(lines[8], "CURSOR cur-2 v5")
        self.assertEqual(lines[9], "PAGE a:10 c:8 NEXT END")  # 新游标无 b
        self.assertEqual(lines[10], "SNAP v5 a:10 c:8")

    def test_cli_error_and_gc(self):
        lines = self.run_cli(
            "set a 1\ndelete ghost\npage cur-9 1\nbogus\ngc\nquit\n"
        )
        self.assertTrue(lines[1].startswith("ERROR NOT_FOUND"))
        self.assertTrue(lines[2].startswith("ERROR UNKNOWN_CURSOR"))
        self.assertTrue(lines[3].startswith("ERROR UNKNOWN_COMMAND"))
        self.assertEqual(lines[4], "GC v1")  # 仅保留当前版本 v2


if __name__ == "__main__":
    unittest.main()
