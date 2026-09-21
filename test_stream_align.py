import random
import unittest

from stream_align import (
    AlignmentEngine, AlignmentConsumer, align_batch, reference_align,
)

SRC = ["aux"]


def feed(engine, *ops):
    """ops: ('anchor', id, t, seq) / ('sample', src, id, t, seq)
            / ('retract_anchor', id) / ('retract_sample', src, id) / ('wm', w)"""
    out = []
    for op in ops:
        if op[0] == "anchor":
            out += engine.add_anchor(op[1], op[2], op[3])
        elif op[0] == "sample":
            out += engine.add_sample(op[1], op[2], op[3], op[4])
        elif op[0] == "retract_anchor":
            out += engine.retract_anchor(op[1])
        elif op[0] == "retract_sample":
            out += engine.retract_sample(op[1], op[2])
        elif op[0] == "wm":
            out += engine.advance_watermark(op[1])
    return out


class TestBasicAlignment(unittest.TestCase):
    def test_prompt_example_tie_then_late_better(self):
        """主10，辅8和12，容差2 → 同距选较早的8；迟到9更优 → 替换为9。"""
        eng = AlignmentEngine(tolerance=2, sources=SRC)
        ev = feed(eng,
                  ("anchor", "m1", 10, 1),
                  ("sample", "aux", "s8", 8, 1),
                  ("sample", "aux", "s12", 12, 2))
        self.assertEqual(ev[-1].matches["aux"], "s8")   # 8 与 12 同距，取较早
        ev = feed(eng, ("sample", "aux", "s9", 9, 3))
        self.assertEqual(len(ev), 1)
        self.assertEqual(ev[0].kind, "upsert")
        self.assertEqual(ev[0].matches["aux"], "s9")    # 更优样本触发替换
        self.assertEqual(ev[0].version, 3)              # v1:空 v2:s8 v3:s9
        self.assertEqual(ev[0].missing, ())

    def test_tie_break_by_seq(self):
        """同距且同时间 → 较小 seq 胜。"""
        eng = AlignmentEngine(tolerance=2, sources=SRC)
        feed(eng, ("anchor", "m1", 10, 1))
        ev = feed(eng, ("sample", "aux", "sB", 9, 7))
        self.assertEqual(ev[0].matches["aux"], "sB")
        ev = feed(eng, ("sample", "aux", "sA", 9, 3))   # 同时间更小 seq
        self.assertEqual(ev[0].matches["aux"], "sA")

    def test_only_out_of_tolerance_sample_is_missing(self):
        """容差外唯一样本 → 缺失标记，不产生匹配。"""
        eng = AlignmentEngine(tolerance=2, sources=SRC)
        ev = feed(eng, ("anchor", "m1", 10, 1),
                  ("sample", "aux", "far", 20, 1))
        self.assertEqual(len(ev), 1)                    # 只有锚点首事件
        self.assertIsNone(ev[0].matches["aux"])
        self.assertEqual(ev[0].missing, ("aux",))

    def test_one_sample_serves_multiple_anchors(self):
        """同一辅助样本可同时是多个锚点的最优。"""
        eng = AlignmentEngine(tolerance=2, sources=SRC)
        ev = feed(eng,
                  ("anchor", "m1", 10, 1),
                  ("anchor", "m2", 11, 2),
                  ("sample", "aux", "s10", 10, 1))
        hits = [e for e in ev if e.matches["aux"] == "s10"]
        self.assertEqual({e.anchor_id for e in hits}, {"m1", "m2"})

    def test_sample_retract_falls_back_to_next_best(self):
        """撤回当前最优 → 回退到次优候选；再次撤回 → 缺失。"""
        eng = AlignmentEngine(tolerance=3, sources=SRC)
        feed(eng,
             ("anchor", "m1", 10, 1),
             ("sample", "aux", "s9", 9, 1),
             ("sample", "aux", "s12", 12, 2),
             ("sample", "aux", "s20", 20, 3))
        ev = feed(eng, ("retract_sample", "aux", "s9"))
        self.assertEqual(ev[0].matches["aux"], "s12")   # 回退次优
        self.assertEqual(ev[0].kind, "upsert")
        ev = feed(eng, ("retract_sample", "aux", "s12"))
        self.assertIsNone(ev[0].matches["aux"])         # 候选耗尽 → 缺失
        self.assertEqual(ev[0].missing, ("aux",))

    def test_anchor_retract_emits_retract_and_consumer_drops_row(self):
        eng = AlignmentEngine(tolerance=2, sources=SRC)
        consumer = AlignmentConsumer()
        consumer.apply_all(feed(eng, ("anchor", "m1", 10, 1),
                                ("sample", "aux", "s9", 9, 1)))
        self.assertIn("m1", consumer.table)
        ev = feed(eng, ("retract_anchor", "m1"))
        self.assertEqual(ev[0].kind, "retract")
        consumer.apply_all(ev)
        self.assertNotIn("m1", consumer.table)


class TestWatermarkAndGC(unittest.TestCase):
    def test_finalized_result_cannot_be_rewritten(self):
        """水位线最终化后，更优迟到样本不得改写结果。"""
        eng = AlignmentEngine(tolerance=2, sources=SRC)
        feed(eng, ("anchor", "m1", 10, 1), ("sample", "aux", "s8", 8, 1))
        ev = feed(eng, ("wm", 12))                      # 10+2 <= 12 → 最终化
        self.assertEqual(ev[0].kind, "finalize")
        self.assertEqual(ev[0].matches["aux"], "s8")
        self.assertTrue(ev[0].finalized)
        ev = feed(eng, ("sample", "aux", "s10", 10, 2))  # 更优但太晚
        self.assertEqual(ev, [])
        self.assertEqual(eng.late_samples, 0)   # t=10 仍可能服务未来锚点，只是不得改写 m1
        ev = feed(eng, ("sample", "aux", "s7", 7, 3))   # 真正迟到(<= w-2*tol=8)
        self.assertEqual(ev, [])
        self.assertEqual(eng.late_samples, 1)
        # 撤锚也无效
        self.assertEqual(feed(eng, ("retract_anchor", "m1")), [])

    def test_late_anchor_rejected(self):
        eng = AlignmentEngine(tolerance=2, sources=SRC)
        feed(eng, ("wm", 20))
        self.assertEqual(feed(eng, ("anchor", "m1", 10, 1)), [])
        self.assertEqual(eng.late_anchors, 1)

    def test_gc_keeps_candidates_for_open_anchors(self):
        """回收不得删掉未闭合锚点仍可能用到的候选。"""
        eng = AlignmentEngine(tolerance=2, sources=SRC)
        feed(eng,
             ("anchor", "m1", 10, 1),                   # 未闭合锚点
             ("sample", "aux", "s9", 9, 1),             # m1 的候选，必须保留
             ("sample", "aux", "s5", 5, 2))             # 对任何锚点都无用
        feed(eng, ("wm", 10.5))                         # 阈值 = 10.5-4 = 6.5
        self.assertEqual(eng.live_sample_count(), 1)    # s5 被回收，s9 保留
        # 水位线后新锚点(>8.5)仍能用到存活的 s9
        ev = feed(eng, ("anchor", "m2", 10.6, 2))
        self.assertEqual(ev[0].matches["aux"], "s9")

    def test_watermark_must_be_monotonic(self):
        eng = AlignmentEngine(tolerance=2, sources=SRC)
        feed(eng, ("wm", 10))
        with self.assertRaises(ValueError):
            eng.advance_watermark(5)


class TestMultiSource(unittest.TestCase):
    def test_sources_aligned_independently(self):
        eng = AlignmentEngine(tolerance=2, sources=["a", "b"])
        ev = feed(eng,
                  ("anchor", "m1", 10, 1),
                  ("sample", "a", "sa", 9, 1))
        self.assertEqual(ev[-1].matches, {"a": "sa", "b": None})
        self.assertEqual(ev[-1].missing, ("b",))
        ev = feed(eng, ("sample", "b", "sb", 11, 1))
        self.assertEqual(ev[0].matches, {"a": "sa", "b": "sb"})
        self.assertEqual(ev[0].missing, ())


class TestRandomizedAgainstReference(unittest.TestCase):
    """随机到达顺序 + 随机撤回，增量引擎(经消费方重放)必须等于独立扫描参考。"""

    def run_trial(self, seed):
        rng = random.Random(seed)
        sources = ["a", "b"]
        tol = 3
        anchors = [(f"m{i}", rng.randint(0, 60), i) for i in range(12)]
        samples = {s: [(f"{s}{j}", rng.randint(0, 60), j)
                       for j in range(rng.randint(5, 20))] for s in sources}
        # 构造事件流：全部 add 乱序，部分实体随后撤回，最后大水位线收尾
        ops = []
        for aid, t, sq in anchors:
            ops.append(("anchor", aid, t, sq))
        for s in sources:
            for sid, t, sq in samples[s]:
                ops.append(("sample", s, sid, t, sq))
        rng.shuffle(ops)
        retracted_anchors, retracted_samples = set(), set()
        final_ops = []
        for op in ops:
            final_ops.append(op)
            if op[0] == "anchor" and rng.random() < 0.2:
                final_ops.append(("retract_anchor", op[1]))
                retracted_anchors.add(op[1])
            elif op[0] == "sample" and rng.random() < 0.2:
                final_ops.append(("retract_sample", op[1], op[2]))
                retracted_samples.add((op[1], op[2]))
        final_ops.append(("wm", 10_000))                # 全部最终化

        eng = AlignmentEngine(tolerance=tol, sources=sources)
        consumer = AlignmentConsumer()
        consumer.apply_all(feed(eng, *final_ops))

        live_anchors = [a for a in anchors if a[0] not in retracted_anchors]
        live_samples = {s: [x for x in samples[s] if (s, x[0]) not in retracted_samples]
                        for s in sources}
        ref = reference_align(live_anchors, live_samples, tol, sources)

        self.assertEqual(set(consumer.table), {a[0] for a in live_anchors})
        for aid, row in consumer.table.items():
            self.assertTrue(row["finalized"])
            self.assertEqual(row["matches"], ref[aid]["matches"], f"seed={seed} {aid}")
            self.assertEqual(row["missing"], ref[aid]["missing"])
        # 批量接口与参考一致
        batch = {r.anchor_id: r for r in align_batch(live_anchors, live_samples, tol, sources)}
        for aid, row in consumer.table.items():
            self.assertEqual(row["matches"], batch[aid].matches)

    def test_many_random_trials(self):
        for seed in range(60):
            with self.subTest(seed=seed):
                self.run_trial(seed)


if __name__ == "__main__":
    unittest.main(verbosity=2)
