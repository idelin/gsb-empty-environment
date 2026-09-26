"""leaderboard 的 unittest 测试。所有数据均为合成数据。"""
import json
import subprocess
import sys
import threading
import unittest
from pathlib import Path

from leaderboard import CursorExpiredError, Leaderboard

HERE = Path(__file__).resolve().parent


class FakeClock:
    def __init__(self):
        self.now = 0.0

    def __call__(self):
        return self.now

    def advance(self, seconds):
        self.now += seconds


def drain(board, cursor_id, size=3):
    """翻完一个游标，返回全部条目。"""
    out = []
    while True:
        entries, done = board.page(cursor_id, size)
        out.extend(entries)
        if done:
            return out


class AcceptanceBoundaryTest(unittest.TestCase):
    """验收边界：版本1为 a10 b9 c8，第一页 a；删除 b 后旧游标仍返回 b c，新游标返回 a c。"""

    def setUp(self):
        self.board = Leaderboard(lease_ttl=60)
        self.board.set("a", 10)
        self.board.set("b", 9)
        self.board.set("c", 8)
        self.v1 = self.board.current_version

    def test_first_page_is_a(self):
        cursor = self.board.open_cursor()
        entries, done = self.board.page(cursor, 1)
        self.assertEqual(entries, [("a", 10.0)])
        self.assertFalse(done)

    def test_old_cursor_still_sees_deleted_b_new_cursor_does_not(self):
        old = self.board.open_cursor()
        first, _ = self.board.page(old, 1)
        self.assertEqual(first, [("a", 10.0)])

        self.board.delete("b")  # 产生新版本

        rest = drain(self.board, old, size=10)
        self.assertEqual(rest, [("b", 9.0), ("c", 8.0)])  # 旧游标仍返回 b c

        new = self.board.open_cursor()
        self.assertEqual(drain(self.board, new, size=10),
                         [("a", 10.0), ("c", 8.0)])  # 新游标返回 a c

    def test_score_revision_does_not_disturb_open_cursor(self):
        old = self.board.open_cursor()
        self.board.set("c", 100)  # c 反超到第一
        self.assertEqual(drain(self.board, old),
                         [("a", 10.0), ("b", 9.0), ("c", 8.0)])
        new = self.board.open_cursor()
        self.assertEqual(drain(self.board, new),
                         [("c", 100.0), ("a", 10.0), ("b", 9.0)])

    def test_tie_breaks_by_id_ascending(self):
        board = Leaderboard()
        board.set("x", 5)
        board.set("y", 5)
        board.set("z", 4)
        cursor = board.open_cursor()
        self.assertEqual(drain(board, cursor, size=1),
                         [("x", 5.0), ("y", 5.0), ("z", 4.0)])

    def test_no_duplicates_or_misses_paging_one_by_one(self):
        board = Leaderboard()
        expected = []
        for i in range(50):
            board.set(f"u{i:03d}", 1000 - i)
            expected.append((f"u{i:03d}", float(1000 - i)))
        cursor = board.open_cursor()
        self.assertEqual(drain(board, cursor, size=1), expected)


class LeaseAndReclaimTest(unittest.TestCase):
    def setUp(self):
        self.clock = FakeClock()
        self.board = Leaderboard(lease_ttl=10, clock=self.clock)
        self.board.set("a", 10)
        self.board.set("b", 9)

    def test_expired_cursor_raises_and_never_switches_version(self):
        cursor = self.board.open_cursor()
        entries, _ = self.board.page(cursor, 1)
        self.assertEqual(entries, [("a", 10.0)])
        self.clock.advance(11)  # 租约过期
        with self.assertRaises(CursorExpiredError):
            self.board.page(cursor, 1)
        # 回收后再次访问：仍然明确报错，绝不悄悄切到新版本
        self.board.collect()
        with self.assertRaises(CursorExpiredError):
            self.board.page(cursor, 1)

    def test_sliding_lease_renews_on_page(self):
        cursor = self.board.open_cursor()
        for _ in range(3):
            self.clock.advance(9)  # 每次都未超过续租后的期限
            self.board.page(cursor, 1)
        self.clock.advance(9)
        entries, done = self.board.page(cursor, 1)
        self.assertTrue(done)

    def test_collect_keeps_pinned_version_and_reclaims_unpinned(self):
        v1 = self.board.current_version
        cursor = self.board.open_cursor()  # pin v1
        self.board.set("c", 8)             # v1+1
        self.board.delete("a")             # v1+2
        latest = self.board.current_version
        collected = self.board.collect()
        self.assertNotIn(v1, collected)        # v1 被租约钉住，不得回收
        self.assertNotIn(latest, collected)    # 最新版本始终保留
        self.assertTrue(self.board.snapshot(v1) is not None)  # 钉住的快照仍可读
        self.assertTrue(self.board.close(cursor))
        collected = self.board.collect()
        self.assertIn(v1, collected)
        self.assertNotIn(latest, collected)
        with self.assertRaises(KeyError):
            self.board.snapshot(v1)

    def test_delete_missing_entry_does_not_bump_version(self):
        version = self.board.current_version
        self.assertEqual(self.board.delete("nobody"), version)


class ConcurrencyTest(unittest.TestCase):
    def test_concurrent_updates_do_not_corrupt_pinned_pagination(self):
        board = Leaderboard(lease_ttl=300)
        base = [(f"u{i:03d}", float(i)) for i in range(200)]
        for entry_id, score in base:
            board.set(entry_id, score)
        cursor = board.open_cursor()
        pinned_version = board.cursor_version(cursor)
        expected = list(board.snapshot(pinned_version))

        errors = []

        def writer(n):
            try:
                for i in range(300):
                    if (i + n) % 3 == 0:
                        board.delete(f"u{(i * 7 + n) % 200:03d}")
                    else:
                        board.set(f"u{(i * 13 + n) % 200:03d}", 1000 + i + n)
            except Exception as exc:  # pragma: no cover
                errors.append(exc)

        threads = [threading.Thread(target=writer, args=(n,)) for n in range(6)]
        for t in threads:
            t.start()

        got = []
        while True:
            entries, done = board.page(cursor, 7)
            got.extend(entries)
            if done:
                break
        for t in threads:
            t.join()

        self.assertEqual(errors, [])
        # 并发修订/删除期间，已打开游标不重不漏，精确等于钉住版本的快照
        self.assertEqual(got, expected)
        self.assertEqual(len({entry_id for entry_id, _ in got}), len(expected))

    def test_concurrent_cursors_each_keep_their_own_version(self):
        clock = FakeClock()
        board = Leaderboard(lease_ttl=1000, clock=clock)
        board.set("a", 1)
        cursors = []
        expected = []
        for round_no in range(8):
            cursor = board.open_cursor()
            cursors.append(cursor)
            expected.append(list(board.snapshot(board.cursor_version(cursor))))
            board.set(f"r{round_no}", 100 + round_no)
            board.delete("a") if round_no == 3 else None

        results = [None] * len(cursors)
        barrier = threading.Barrier(len(cursors))

        def reader(i):
            barrier.wait()
            results[i] = drain(board, cursors[i], size=2)

        threads = [threading.Thread(target=reader, args=(i,)) for i in range(len(cursors))]
        for t in threads:
            t.start()
        for t in threads:
            t.join()
        self.assertEqual(results, expected)


class CliTest(unittest.TestCase):
    def test_acceptance_scenario_via_cli(self):
        script = "\n".join([
            "set a 10", "set b 9", "set c 8",
            "open", "page c1 1",
            "del b",
            "page c1 10",
            "open", "page c2 10",
            "gc", "quit",
        ])
        proc = subprocess.run(
            [sys.executable, str(HERE / "leaderboard.py")],
            input=script, capture_output=True, text=True, timeout=30,
        )
        self.assertEqual(proc.returncode, 0, proc.stderr)
        lines = [json.loads(l) for l in proc.stdout.splitlines()]
        self.assertTrue(all(l["ok"] for l in lines), lines)
        first_page = lines[4]["entries"]
        self.assertEqual(first_page, [{"id": "a", "score": 10.0}])
        old_cursor_rest = [e["id"] for e in lines[6]["entries"]]
        self.assertEqual(old_cursor_rest, ["b", "c"])
        new_cursor_all = [e["id"] for e in lines[8]["entries"]]
        self.assertEqual(new_cursor_all, ["a", "c"])

    def test_cli_reports_expired_cursor(self):
        script = "set a 1\nopen\npage c1 1\n"
        proc = subprocess.run(
            [sys.executable, str(HERE / "leaderboard.py"), "--ttl", "0"],
            input=script, capture_output=True, text=True, timeout=30,
        )
        lines = [json.loads(l) for l in proc.stdout.splitlines()]
        self.assertFalse(lines[-1]["ok"])
        self.assertIn("cursor_expired", lines[-1]["error"])


if __name__ == "__main__":
    unittest.main()
