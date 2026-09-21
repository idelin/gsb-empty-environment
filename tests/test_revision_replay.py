"""修订流重放测试：把 UPSERT/REVOKE 序列在一张白纸上重放，
最终区间集合必须与引擎当前视图及全量重算参考三者一致。"""

from __future__ import annotations

import random
import unittest

from wrapcounter import (
    CounterAnalyzer,
    DuplicatePolicy,
    Sample,
    SourceConfig,
    Status,
    recompute_source,
)


class RevisionReplayTests(unittest.TestCase):
    def test_replay_revision_stream_matches_reference(self):
        cfg = SourceConfig(source="s", width_bits=8, max_gap_seconds=30)
        engine = CounterAnalyzer()
        engine.register_source(cfg)
        rng = random.Random(99)

        live: dict[str, Sample] = {}
        for i in range(30):
            t = i * 8.0 + rng.uniform(-2, 2)
            sample = Sample(f"s{i}", "s", rng.randrange(256), t,
                            seq=rng.randrange(2), reset=rng.random() < 0.1,
                            ingest_order=i)
            live[sample.sample_id] = sample
            engine.upsert_sample(sample, DuplicatePolicy.REPLACE)
            if i > 5 and rng.random() < 0.25:
                victim = f"s{rng.randrange(0, i)}"
                if victim in live:
                    engine.retract_sample(victim)
                    del live[victim]

        # ---- 白纸重放修订流 ----
        view: dict[str, object] = {}
        for rev in engine.revision_history():
            if rev.op.value == "upsert":
                view[rev.interval_id] = rev.interval
            else:
                view.pop(rev.interval_id, None)

        # ---- 与引擎当前视图一致 ----
        current = {iv.interval_id: iv for iv in engine.current_intervals("s")}
        self.assertEqual(set(view), set(current))
        for iid, iv in view.items():
            self.assertEqual(iv.delta, current[iid].delta)
            self.assertEqual(iv.status, current[iid].status)
            self.assertEqual(iv.config_version, current[iid].config_version)

        # ---- 与全量排序重算参考一致 ----
        _, ref, total_ref = recompute_source(cfg, list(live.values()))
        ref_map = {iv.interval_id: iv for iv in ref}
        self.assertEqual(set(view), set(ref_map))
        total = engine.source_total("s")
        self.assertEqual(total.definite_delta, total_ref.definite_delta)
        self.assertEqual(total.complete, total_ref.complete)

        # revision_id 全局单调
        ids = [r.revision_id for r in engine.revision_history()]
        self.assertEqual(ids, list(range(1, len(ids) + 1)))

        # 每个生效结果都可溯源到采样 ID
        for iv in current.values():
            self.assertTrue(iv.curr_sample_id)
            self.assertGreaterEqual(iv.config_version, 1)


if __name__ == "__main__":
    unittest.main()
