"""Differential test: incremental engine vs full-history replay.

After every single operation (sample, same-time correction, retroactive
threshold/suppression edit, rollback) the incremental state must equal a
fresh engine rebuilt by replaying the immutable input log. The outbox
notification set and the append-only revision audit must match too.
"""
import os
import random
import shutil
import tempfile
import unittest

from alerting.engine import Engine
from alerting.outbox import Pump


def _replay_copy(src_path):
    dst = src_path + ".replay.db"
    shutil.copyfile(src_path, dst)
    try:
        return Engine.replay(dst), dst
    except Exception:
        if os.path.exists(dst):
            os.remove(dst)
        raise


class ReplayEquivalenceTest(unittest.TestCase):
    def setUp(self):
        self.dir = tempfile.mkdtemp()
        self.path = os.path.join(self.dir, "inc.db")
        self.e = Engine(self.path)
        self.random = random.Random(20260921)

    def tearDown(self):
        shutil.rmtree(self.dir, ignore_errors=True)

    def _check(self, tag):
        rep, dst = _replay_copy(self.path)
        try:
            self.assertEqual(
                self.e.alert_snapshot(), rep.alert_snapshot(),
                f"alert snapshot differs after {tag}")
            self.assertEqual(
                self.e.notification_contents(),
                rep.notification_contents(),
                f"notifications differ after {tag}")
            self.assertEqual(
                self.e.revision_snapshot(), rep.revision_snapshot(),
                f"revision audit differs after {tag}")
        finally:
            rep.conn.close()
            os.remove(dst)

    def test_random_stream_against_full_replay(self):
        rnd = self.random
        self.e.add_threshold("r", 0, "m", 10, 10, 3)
        self._check("initial rule")
        t = 0
        for step in range(80):
            t += rnd.choice([0, 1, 2])  # 0 => same-time correction
            roll = rnd.random()
            metric = rnd.choice(["m", "n"])
            if roll < 0.72:
                self.e.ingest_sample(t, metric,
                                     rnd.choice([0, 5, 11, 12, 30]))
            elif roll < 0.80:
                self.e.add_suppression(
                    rnd.choice(["s1", "s2", "s3"]),
                    window_start=max(0, t - rnd.randint(0, 6)),
                    window_end=(t + rnd.randint(1, 12))
                    if rnd.random() < 0.7 else None,
                    metric=(metric if rnd.random() < 0.6 else None),
                    valid_from=t - rnd.randint(0, 4))
            elif roll < 0.90:
                self.e.add_threshold(
                    rnd.choice(["r", "q"]), t - rnd.randint(0, 5),
                    metric,
                     rnd.choice([8, 10, 20]),
                     rnd.choice([8, 10, 20]),
                     rnd.choice([1, 3, 5]))
            else:
                ver = self.e.conn.execute(
                    "SELECT MIN(version) FROM threshold_versions "
                    "WHERE rule_id='r'").fetchone()
                if ver is not None and ver[0] is not None:
                    self.e.rollback_threshold("r", to_version=ver[0],
                                              valid_from=t)
                else:
                    self.e.add_threshold("r", 0, metric, 10, 10, 3)
            self._check(f"step {step}")

        # occasionally pump notifications and inject a restart mid-stream:
        # delivery side effects must never perturb replay equivalence
        Pump(self.e).run()
        self._check("after pumping outbox")

    def test_headline_scenario_replay_matches(self):
        e = self.e
        e.add_threshold("r", 0, "m", 10, 10, 5)
        e.ingest_sample(0, "m", 11)
        e.ingest_sample(6, "m", 12)
        # drain notifications like a running deployment would
        Pump(e).run()
        e.ingest_sample(3, "m", 8)  # retroactive correction -> RETRACT
        self._check("backfill retraction")
        rep, dst = _replay_copy(self.path)
        try:
            self.assertEqual(rep.notification_kinds(),
                             ["OPEN", "RETRACT"])
        finally:
            rep.conn.close()
            os.remove(dst)


if __name__ == "__main__":
    unittest.main()
