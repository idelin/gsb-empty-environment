"""materialized_view 的 unittest 测试。所有数据均为合成数据。"""
import json
import os
import random
import subprocess
import sys
import tempfile
import unittest
from fractions import Fraction

from materialized_view import Event, MaterializedView, median_to_json

HERE = os.path.dirname(os.path.abspath(__file__))
CLI = os.path.join(HERE, "materialized_view.py")


def ev(subject, event_id, value, group="g1", revision=1, generation=1):
    return Event(subject=subject, event_id=event_id, group=group,
                 value=value, revision=revision, consent_generation=generation)


class AcceptanceBoundaryTest(unittest.TestCase):
    """验收边界:2 和 8 中位数 5;撤回后计数 0;旧代次 100 重放仍为 0;
    新代次 3 只计 3。"""

    def test_acceptance_boundary(self):
        view = MaterializedView()
        self.assertTrue(view.apply_event(ev("s1", "e1", 2)))
        self.assertTrue(view.apply_event(ev("s1", "e2", 8)))
        result = view.query()
        self.assertEqual(result["count"], 2)
        self.assertEqual(result["sum"], 10)
        self.assertEqual(result["median"], Fraction(5))

        view.revoke("s1")
        self.assertEqual(view.query()["count"], 0)
        self.assertEqual(view.query()["sum"], 0)
        self.assertIsNone(view.query()["median"])

        # 旧代次(=1)值 100 重放:被墓碑拒绝,计数仍为 0
        self.assertFalse(view.apply_event(ev("s1", "e3", 100, generation=1)))
        self.assertEqual(view.query()["count"], 0)
        self.assertEqual(view.query()["sum"], 0)

        # 新代次(=2)值 3:只计 3
        self.assertTrue(view.apply_event(ev("s1", "e4", 3, generation=2)))
        result = view.query()
        self.assertEqual(result["count"], 1)
        self.assertEqual(result["sum"], 3)
        self.assertEqual(result["median"], Fraction(3))


class RevisionTest(unittest.TestCase):
    def test_higher_revision_replaces(self):
        view = MaterializedView()
        view.apply_event(ev("s1", "e1", 5, revision=1))
        self.assertTrue(view.apply_event(ev("s1", "e1", 9, revision=2)))
        self.assertEqual(view.query()["sum"], 9)
        self.assertEqual(view.query()["count"], 1)

    def test_stale_revision_rejected(self):
        view = MaterializedView()
        view.apply_event(ev("s1", "e1", 5, revision=2))
        self.assertFalse(view.apply_event(ev("s1", "e1", 99, revision=1)))
        self.assertFalse(view.apply_event(ev("s1", "e1", 99, revision=2)))
        self.assertEqual(view.query()["sum"], 5)

    def test_revision_after_revoke_needs_higher_generation(self):
        view = MaterializedView()
        view.apply_event(ev("s1", "e1", 5, revision=1, generation=1))
        view.revoke("s1")
        # 同代次更高修订仍被墓碑拒绝
        self.assertFalse(view.apply_event(ev("s1", "e1", 7, revision=2, generation=1)))
        # 更高代次可复活该事件
        self.assertTrue(view.apply_event(ev("s1", "e1", 7, revision=2, generation=2)))
        self.assertEqual(view.query()["sum"], 7)


class RevocationTest(unittest.TestCase):
    def test_revoke_removes_all_contributions_across_groups(self):
        view = MaterializedView()
        view.apply_event(ev("s1", "e1", 1, group="a"))
        view.apply_event(ev("s1", "e2", 2, group="b"))
        view.apply_event(ev("s2", "e3", 4, group="a"))
        view.revoke("s1")
        self.assertEqual(view.query("a")["sum"], 4)
        self.assertEqual(view.query("b")["count"], 0)
        self.assertEqual(view.query()["sum"], 4)

    def test_revoke_unknown_subject_is_noop(self):
        view = MaterializedView()
        view.revoke("nobody")
        self.assertEqual(view.query()["count"], 0)

    def test_stale_generation_rejected_without_tombstone(self):
        view = MaterializedView()
        view.apply_event(ev("s1", "e1", 1, generation=3))
        self.assertFalse(view.apply_event(ev("s1", "e2", 2, generation=2)))
        self.assertEqual(view.query()["sum"], 1)

    def test_reconsent_cycle(self):
        view = MaterializedView()
        view.apply_event(ev("s1", "e1", 10, generation=1))
        view.revoke("s1")
        view.apply_event(ev("s1", "e2", 20, generation=2))
        view.revoke("s1")
        # 代次 2 也已被墓碑覆盖
        self.assertFalse(view.apply_event(ev("s1", "e3", 30, generation=2)))
        self.assertTrue(view.apply_event(ev("s1", "e4", 40, generation=3)))
        self.assertEqual(view.query()["sum"], 40)


class MedianTest(unittest.TestCase):
    def test_exact_median_odd_and_even(self):
        view = MaterializedView()
        for index, value in enumerate([7, 1, 4]):
            view.apply_event(ev("s1", f"e{index}", value))
        self.assertEqual(view.query()["median"], Fraction(4))
        view.apply_event(ev("s1", "e3", 2))
        self.assertEqual(view.query()["median"], Fraction(3))  # (2+4)/2

    def test_exact_median_half(self):
        view = MaterializedView()
        view.apply_event(ev("s1", "e1", 1))
        view.apply_event(ev("s1", "e2", 2))
        self.assertEqual(view.query()["median"], Fraction(3, 2))
        self.assertEqual(median_to_json(view.query()["median"]), 1.5)

    def test_group_median_independent(self):
        view = MaterializedView()
        view.apply_event(ev("s1", "e1", 100, group="a"))
        view.apply_event(ev("s2", "e2", 1, group="b"))
        view.apply_event(ev("s3", "e3", 3, group="b"))
        self.assertEqual(view.query("a")["median"], Fraction(100))
        self.assertEqual(view.query("b")["median"], Fraction(2))
        self.assertEqual(view.query()["median"], Fraction(3))


class FullRecomputeTest(unittest.TestCase):
    def test_incremental_matches_full_recompute_random_ops(self):
        rng = random.Random(20260927)
        view = MaterializedView()
        subjects = [f"s{i}" for i in range(5)]
        for step in range(2000):
            if rng.random() < 0.2:
                view.revoke(rng.choice(subjects))
            else:
                view.apply_event(ev(
                    subject=rng.choice(subjects),
                    event_id=f"e{rng.randrange(60)}",
                    value=rng.randrange(-50, 51),
                    group=rng.choice(["a", "b", "c"]),
                    revision=rng.randrange(4),
                    generation=rng.randrange(5),
                ))
            if step % 250 == 0:
                self.assertTrue(view.verify(), f"第 {step} 步增量与全量不一致")
        self.assertTrue(view.verify())

    def test_verify_detects_nothing_on_empty(self):
        self.assertTrue(MaterializedView().verify())


class PersistenceTest(unittest.TestCase):
    def test_save_load_roundtrip(self):
        view = MaterializedView()
        view.apply_event(ev("s1", "e1", 2, generation=1))
        view.apply_event(ev("s1", "e2", 8, generation=1))
        view.revoke("s1")
        view.apply_event(ev("s1", "e3", 3, generation=2))
        view.apply_event(ev("s2", "e4", 5, group="g2"))
        with tempfile.TemporaryDirectory() as tmp:
            path = os.path.join(tmp, "state.json")
            view.save(path)
            loaded = MaterializedView.load(path)
        self.assertEqual(loaded._snapshot(), view._snapshot())
        self.assertEqual(loaded.query()["sum"], 8)
        self.assertEqual(loaded.query("g2")["median"], Fraction(5))
        # 重启后墓碑仍生效:旧代次重放被拒绝
        self.assertFalse(loaded.apply_event(ev("s1", "e5", 100, generation=1)))
        self.assertEqual(loaded.query()["sum"], 8)

    def test_restart_continues_log(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = os.path.join(tmp, "state.json")
            first = MaterializedView()
            first.apply_event(ev("s1", "e1", 1))
            first.save(path)
            second = MaterializedView.load(path)
            second.apply_event(ev("s1", "e2", 2))
            second.save(path)
            third = MaterializedView.load(path)
        self.assertEqual(third.query()["sum"], 3)
        self.assertTrue(third.verify())


class CliTest(unittest.TestCase):
    def run_cli(self, *args, stdin_text=""):
        return subprocess.run(
            [sys.executable, CLI, *args],
            input=stdin_text, capture_output=True, text=True, timeout=30,
        )

    def test_cli_acceptance_flow_with_persistence(self):
        with tempfile.TemporaryDirectory() as tmp:
            db = os.path.join(tmp, "view.json")
            ops = "\n".join([
                json.dumps({"op": "event", "subject": "s1", "event_id": "e1",
                            "group": "g1", "value": 2, "revision": 1,
                            "consent_generation": 1}),
                json.dumps({"op": "event", "subject": "s1", "event_id": "e2",
                            "group": "g1", "value": 8, "revision": 1,
                            "consent_generation": 1}),
            ])
            proc = self.run_cli("--db", db, "process", "-", stdin_text=ops)
            self.assertEqual(proc.returncode, 0, proc.stderr)
            out = self.run_cli("--db", db, "query")
            self.assertEqual(json.loads(out.stdout)["median"], 5)

            self.run_cli("--db", db, "process", "-",
                         stdin_text=json.dumps({"op": "revoke", "subject": "s1"}))
            out = self.run_cli("--db", db, "query")
            self.assertEqual(json.loads(out.stdout)["count"], 0)

            replay = json.dumps({"op": "event", "subject": "s1", "event_id": "e3",
                                 "group": "g1", "value": 100, "revision": 1,
                                 "consent_generation": 1})
            proc = self.run_cli("--db", db, "process", "-", stdin_text=replay)
            self.assertIn('"accepted": false', proc.stdout)
            out = self.run_cli("--db", db, "query")
            self.assertEqual(json.loads(out.stdout)["count"], 0)

            renew = json.dumps({"op": "event", "subject": "s1", "event_id": "e4",
                                "group": "g1", "value": 3, "revision": 1,
                                "consent_generation": 2})
            self.run_cli("--db", db, "process", "-", stdin_text=renew)
            out = self.run_cli("--db", db, "query")
            result = json.loads(out.stdout)
            self.assertEqual((result["count"], result["sum"], result["median"]), (1, 3, 3))

            verify = self.run_cli("--db", db, "verify")
            self.assertEqual(verify.returncode, 0)
            self.assertEqual(json.loads(verify.stdout)["consistent"], True)

    def test_cli_rejects_bad_op(self):
        proc = self.run_cli("process", "-", stdin_text='{"op": "nope"}')
        self.assertEqual(proc.returncode, 2)
        self.assertIn("error", proc.stdout)


if __name__ == "__main__":
    unittest.main()
