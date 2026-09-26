"""retractable_view 的单元测试。所有数据均为合成数据。"""

import json
import os
import subprocess
import sys
import tempfile
import unittest

from retractable_view import RetractableView


def feed(view, commands):
    return [view.apply_command(c) for c in commands]


class AcceptanceTest(unittest.TestCase):
    """验收边界：2 和 8 中位数 5；撤回后计数 0；旧代次 100 重放仍为 0；
    新代次值 3 只计 3。"""

    def test_acceptance_boundary(self):
        view = RetractableView()
        results = feed(view, [
            {"type": "upsert", "subject": "s1", "event_id": "e1", "group": "g",
             "value": 2, "revision": 1, "generation": 1},
            {"type": "upsert", "subject": "s1", "event_id": "e2", "group": "g",
             "value": 8, "revision": 1, "generation": 1},
        ])
        self.assertTrue(all(r[0] for r in results))
        self.assertEqual(view.query(), {"sum": 10, "count": 2, "median": 5})

        applied, _ = view.apply_command(
            {"type": "retract", "subject": "s1", "generation": 1})
        self.assertTrue(applied)
        self.assertEqual(view.query(), {"sum": 0, "count": 0, "median": None})

        # 旧代次（<= 墓碑代次）重放，值 100 不得复活
        applied, reason = view.apply_command(
            {"type": "upsert", "subject": "s1", "event_id": "e3", "group": "g",
             "value": 100, "revision": 1, "generation": 1})
        self.assertFalse(applied)
        self.assertEqual(reason, "subject_retracted")
        self.assertEqual(view.query(), {"sum": 0, "count": 0, "median": None})

        # 相同代次的 consent 不被接受，必须严格更高
        applied, _ = view.apply_command(
            {"type": "consent", "subject": "s1", "generation": 1})
        self.assertFalse(applied)

        # 更高代次重新同意，新事件值 3 只计 3
        applied, _ = view.apply_command(
            {"type": "consent", "subject": "s1", "generation": 2})
        self.assertTrue(applied)
        applied, _ = view.apply_command(
            {"type": "upsert", "subject": "s1", "event_id": "e4", "group": "g",
             "value": 3, "revision": 1, "generation": 2})
        self.assertTrue(applied)
        self.assertEqual(view.query(), {"sum": 3, "count": 1, "median": 3})


class RevisionTest(unittest.TestCase):
    def test_higher_revision_replaces(self):
        view = RetractableView()
        feed(view, [
            {"type": "upsert", "subject": "s", "event_id": "e", "group": "g",
             "value": 10, "revision": 1, "generation": 1},
            {"type": "upsert", "subject": "s", "event_id": "e", "group": "g",
             "value": 42, "revision": 2, "generation": 1},
        ])
        self.assertEqual(view.query(), {"sum": 42, "count": 1, "median": 42})

    def test_stale_revision_ignored(self):
        view = RetractableView()
        feed(view, [
            {"type": "upsert", "subject": "s", "event_id": "e", "group": "g",
             "value": 10, "revision": 2, "generation": 1},
        ])
        for rev in (1, 2):  # 更低与相同修订号都不得覆盖
            applied, reason = view.apply_command(
                {"type": "upsert", "subject": "s", "event_id": "e", "group": "g",
                 "value": 99, "revision": rev, "generation": 1})
            self.assertFalse(applied)
            self.assertEqual(reason, "stale_revision")
        self.assertEqual(view.query(), {"sum": 10, "count": 1, "median": 10})

    def test_revision_can_change_group(self):
        view = RetractableView()
        feed(view, [
            {"type": "upsert", "subject": "s", "event_id": "e", "group": "a",
             "value": 5, "revision": 1, "generation": 1},
            {"type": "upsert", "subject": "s", "event_id": "e", "group": "b",
             "value": 7, "revision": 2, "generation": 1},
        ])
        self.assertEqual(view.query("a"), {"sum": 0, "count": 0, "median": None})
        self.assertEqual(view.query("b"), {"sum": 7, "count": 1, "median": 7})


class MedianTest(unittest.TestCase):
    def test_odd_and_even_median(self):
        view = RetractableView()
        for i, v in enumerate([9, 1, 5]):
            view.apply_command({"type": "upsert", "subject": "s",
                                "event_id": f"e{i}", "group": "g",
                                "value": v, "revision": 1, "generation": 1})
        self.assertEqual(view.query()["median"], 5)
        view.apply_command({"type": "upsert", "subject": "s", "event_id": "e3",
                            "group": "g", "value": 3, "revision": 1,
                            "generation": 1})
        self.assertEqual(view.query()["median"], 4)  # (3+5)/2

    def test_half_integer_median(self):
        view = RetractableView()
        for i, v in enumerate([1, 2]):
            view.apply_command({"type": "upsert", "subject": "s",
                                "event_id": f"e{i}", "group": "g",
                                "value": v, "revision": 1, "generation": 1})
        self.assertEqual(view.query()["median"], 1.5)


class GroupTest(unittest.TestCase):
    def test_per_group_aggregation(self):
        view = RetractableView()
        feed(view, [
            {"type": "upsert", "subject": "s1", "event_id": "e1", "group": "a",
             "value": 2, "revision": 1, "generation": 1},
            {"type": "upsert", "subject": "s2", "event_id": "e2", "group": "b",
             "value": 8, "revision": 1, "generation": 1},
        ])
        self.assertEqual(view.query("a"), {"sum": 2, "count": 1, "median": 2})
        self.assertEqual(view.query("b"), {"sum": 8, "count": 1, "median": 8})
        self.assertEqual(view.query(), {"sum": 10, "count": 2, "median": 5})


class VerifyTest(unittest.TestCase):
    def test_incremental_matches_full_recompute(self):
        view = RetractableView()
        feed(view, [
            {"type": "upsert", "subject": "s1", "event_id": "e1", "group": "a",
             "value": 2, "revision": 1, "generation": 1},
            {"type": "upsert", "subject": "s1", "event_id": "e2", "group": "b",
             "value": 8, "revision": 1, "generation": 1},
            {"type": "upsert", "subject": "s2", "event_id": "e3", "group": "a",
             "value": 4, "revision": 1, "generation": 1},
            {"type": "upsert", "subject": "s1", "event_id": "e1", "group": "a",
             "value": 6, "revision": 2, "generation": 1},
            {"type": "retract", "subject": "s2", "generation": 2},
            {"type": "consent", "subject": "s2", "generation": 3},
            {"type": "upsert", "subject": "s2", "event_id": "e4", "group": "b",
             "value": 1, "revision": 1, "generation": 3},
        ])
        report = view.verify()
        self.assertTrue(report["ok"], msg=json.dumps(report, ensure_ascii=False))


class PersistenceTest(unittest.TestCase):
    COMMANDS = [
        {"type": "upsert", "subject": "s1", "event_id": "e1", "group": "a",
         "value": 2, "revision": 1, "generation": 1},
        {"type": "upsert", "subject": "s1", "event_id": "e2", "group": "a",
         "value": 8, "revision": 1, "generation": 1},
        {"type": "retract", "subject": "s1", "generation": 1},
        {"type": "upsert", "subject": "s1", "event_id": "e3", "group": "a",
         "value": 100, "revision": 1, "generation": 1},
        {"type": "consent", "subject": "s1", "generation": 2},
        {"type": "upsert", "subject": "s1", "event_id": "e4", "group": "a",
         "value": 3, "revision": 1, "generation": 2},
    ]

    def test_snapshot_roundtrip(self):
        view = RetractableView()
        feed(view, self.COMMANDS)
        restored = RetractableView.load_state(json.loads(json.dumps(view.dump_state())))
        self.assertEqual(restored.query(), {"sum": 3, "count": 1, "median": 3})
        # 重启后墓碑仍然生效：旧代次事件依旧被拒绝
        applied, _ = restored.apply_command(
            {"type": "upsert", "subject": "s1", "event_id": "e5", "group": "a",
             "value": 100, "revision": 1, "generation": 1})
        self.assertFalse(applied)
        self.assertEqual(restored.query()["count"], 1)

    def test_cli_journal_restart(self):
        """通过 CLI 写入日志、退出后重启查询，结果一致。"""
        with tempfile.TemporaryDirectory() as tmp:
            db = os.path.join(tmp, "view.journal")
            cmds = os.path.join(tmp, "cmds.jsonl")
            with open(cmds, "w", encoding="utf-8") as fh:
                for c in self.COMMANDS:
                    fh.write(json.dumps(c) + "\n")
            env = dict(os.environ, PYTHONIOENCODING="utf-8")
            root = os.path.dirname(os.path.abspath(__file__))
            subprocess.run([sys.executable, "cli.py", "apply", "--db", db, cmds],
                           cwd=root, check=True, capture_output=True, env=env)
            out = subprocess.run([sys.executable, "cli.py", "query", "--db", db],
                                 cwd=root, check=True, capture_output=True,
                                 text=True, env=env)
            self.assertEqual(json.loads(out.stdout),
                             {"sum": 3, "count": 1, "median": 3})
            # 快照 -> 重启加载快照
            snap = os.path.join(tmp, "snap.json")
            subprocess.run([sys.executable, "cli.py", "snapshot", "--db", db,
                            "--out", snap], cwd=root, check=True,
                           capture_output=True, env=env)
            out = subprocess.run([sys.executable, "cli.py", "query",
                                  "--snapshot", snap], cwd=root, check=True,
                                 capture_output=True, text=True, env=env)
            self.assertEqual(json.loads(out.stdout),
                             {"sum": 3, "count": 1, "median": 3})
            # verify 子命令退出码为 0
            out = subprocess.run([sys.executable, "cli.py", "verify", "--db", db],
                                 cwd=root, capture_output=True, text=True, env=env)
            self.assertEqual(out.returncode, 0, msg=out.stdout + out.stderr)


if __name__ == "__main__":
    unittest.main()
