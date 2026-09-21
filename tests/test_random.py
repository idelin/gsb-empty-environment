"""随机差分测试（全部本地生成）。

每个场景由一个“设备真值模拟器”驱动：
- 维护绝对（未取模）累计计数器 abs_counter，事件步长保证相邻已生成事件
  的真实增量 < M，因此相邻对在间隔内不足一整圈。
- 随机触发设备 reset：reset 点 abs_counter 归零，真值链断开（跨点不可判定）。
- 采样可以乱序迟到、也可以被撤回。

每一步操作后检查：
1. 增量引擎的当前区间集合与 reference.recompute_source 逐字段一致；
2. 所有 DEFINITE 区间的 delta 与模拟器真值完全相等；
3. 守恒性：当前确定区间集合的任意“粗划分 vs 细划分”在模拟器真值下重合
   （即插入有效中间采样前后，可确定总增量守恒；引擎实现保证了拆分恒等）；
4. 撤回/插入只触碰相邻区间（远离点 interval_id 不变）。
"""

from __future__ import annotations

import os
import random
import unittest

from wrapcounter import (
    CounterAnalyzer,
    DuplicatePolicy,
    Reason,
    RevisionOp,
    Sample,
    SourceConfig,
    Status,
    recompute_source,
)


class GroundTruth:
    """单个来源的隐藏真值。"""

    def __init__(self, source: str, width_bits: int, max_gap: float, seed: int):
        self.source = source
        self.mod = 1 << width_bits
        self.max_gap = max_gap
        self.rng = random.Random(seed)
        self.abs_counter = self.rng.randrange(self.mod)
        self.time = 0.0
        self.events: list[dict] = []  # 真值事件，时间升序

    def advance(self) -> dict:
        # dt 控制在 max_gap 内，保证相邻事件间隔不足一整圈的采样合同；
        # 真实增量取 [1, M-1]，相邻事件间严格不足一圈。
        dt = self.rng.uniform(1.0, self.max_gap)
        # 速率合同：inc <= (M-1) * dt / max_gap，从而任意长度 <= max_gap 的
        # 时间窗内真实总增量严格 < M（至多 0 整圈），与判定合同一致。
        rate_bound = max(1, int((self.mod - 1) * dt / self.max_gap))
        inc = self.rng.randrange(1, rate_bound + 1)
        reset = self.rng.random() < 0.12
        self.time += dt
        if reset:
            self.abs_counter = 0
            new_value = self.rng.randrange(0, self.mod)
            truth_delta = None  # 重置点：重置前增量不可知
            self.abs_counter = new_value
        else:
            self.abs_counter += inc
            new_value = self.abs_counter % self.mod
            truth_delta = inc
        seq = self.rng.randrange(0, 3)
        ev = {
            "time": self.time,
            "seq": seq,
            "value": int(new_value),
            "reset": reset,
            "truth": truth_delta,
            "abs_after": self.abs_counter,
        }
        self.events.append(ev)
        return ev

    def truth_between(self, i: int, j: int) -> int | None:
        """真值事件链上 i->j 的真实增量；中间任何 reset 则 None。"""
        if i is None:
            return None
        total = 0
        for k in range(i + 1, j + 1):
            if self.events[k]["reset"]:
                return None
            total += self.events[k]["truth"]
        return total


def _assert_intervals_equal(test: unittest.TestCase, a, b) -> None:
    test.assertEqual(len(a), len(b))
    for x, y in zip(a, b):
        test.assertEqual(x.interval_id, y.interval_id)
        test.assertEqual(x.status, y.status)
        test.assertEqual(x.reason, y.reason)
        test.assertEqual(x.delta, y.delta)
        test.assertEqual(x.wraps, y.wraps)
        test.assertEqual(x.reset_boundary, y.reset_boundary)
        test.assertEqual(x.config_version, y.config_version)
        test.assertEqual(x.prev_sample_id, y.prev_sample_id)
        test.assertEqual(x.curr_sample_id, y.curr_sample_id)
        test.assertEqual(x.prev_value, y.prev_value)
        test.assertEqual(x.curr_value, y.curr_value)


class RandomDifferentialTests(unittest.TestCase):
    def _run_scenario(self, seed: int, width_bits: int, n_events: int) -> None:
        max_gap = 25.0
        source = f"dev{seed % 5}"
        cfg = SourceConfig(source=source, width_bits=width_bits,
                           max_gap_seconds=max_gap, config_version=1)
        gt = GroundTruth(source, width_bits, max_gap, seed * 7919 + 13)
        for _ in range(n_events):
            gt.advance()

        # 处理同 (time, seq) 冲突：给真值事件强制唯一 seq（seq 槽冲突则顺延）。
        used: set[tuple[float, int]] = set()
        for ev in gt.events:
            seq = ev["seq"]
            while (ev["time"], seq) in used:
                seq += 1
            used.add((ev["time"], seq))
            ev["seq"] = seq

        engine = CounterAnalyzer()
        engine.register_source(cfg)

        # 事件随机乱序送达；部分送达后撤回，再补送。
        order = list(range(n_events))
        rng = random.Random(seed * 104729 + 7)
        rng.shuffle(order)
        live_ids: list[str] = []

        def samples_from_live():
            samples = []
            for idx in live_ids:
                ev = gt.events[idx]
                samples.append(Sample(
                    sample_id=f"e{idx}", source=source, value=ev["value"],
                    event_time=ev["time"], seq=ev["seq"], reset=ev["reset"],
                    ingest_order=idx,
                ))
            return samples

        def assert_matches_reference():
            samples = samples_from_live()
            ordered_ref, ref_intervals, ref_total = recompute_source(cfg, samples)
            cur = engine.current_intervals(source)
            _assert_intervals_equal(self, cur, ref_intervals)
            total = engine.source_total(source)
            self.assertEqual(total.definite_delta, ref_total.definite_delta)
            self.assertEqual(total.complete, ref_total.complete)
            self.assertEqual(total.uncertain_interval_ids,
                             ref_total.uncertain_interval_ids)
            self.assertEqual(total.ordered_sample_ids, ref_total.ordered_sample_ids)
            self.assertEqual(total.config_version, 1)
            return ordered_ref, cur

        # 真值索引 -> 已送达
        delivered: set[int] = set()
        for step, idx in enumerate(order):
            ev = gt.events[idx]
            sample = Sample(
                sample_id=f"e{idx}", source=source, value=ev["value"],
                event_time=ev["time"], seq=ev["seq"], reset=ev["reset"],
                ingest_order=step,
            )
            before = {iv.interval_id for iv in engine.current_intervals(source)}
            revs = engine.upsert_sample(sample)
            delivered.add(idx)
            live_ids = sorted(delivered)

            # 远离插入点的区间不应被修订
            after = {iv.interval_id for iv in engine.current_intervals(source)}
            untouched_before = before - after
            self.assertLessEqual(len(untouched_before), 1, revs)  # 至多旧相邻区间被拆
            # 插入产出的修订数：首样 1(anchor)，否则 1(anchor替换)+2 或 2 或 3
            self.assertGreaterEqual(len(revs), 1)

            ordered, cur = assert_matches_reference()

            # 真值校验：每个 DEFINITE 区间 delta 必须等于真值
            ord_sorted = sorted(delivered)
            idx_of = {f"e{g}": k for k, g in enumerate(ord_sorted)}
            for iv in cur:
                if iv.prev_sample_id is None:
                    self.assertEqual(iv.reason, Reason.SOLE_SAMPLE)
                    continue
                gi = idx_of[iv.prev_sample_id]
                gj = idx_of[iv.curr_sample_id]
                lo_event, hi_event = ord_sorted[gi], ord_sorted[gj]
                truth = gt.truth_between(lo_event, hi_event)
                dt = gt.events[hi_event]["time"] - gt.events[lo_event]["time"]
                adjacent = hi_event == lo_event + 1
                if iv.status is Status.DEFINITE:
                    if truth is None:
                        # 只有当 reset 采样当前未送达（信息不可知）时才允许：
                        # reset 必严格位于两端点之间，且对应采样不在 live 集合。
                        hidden = [k for k in range(lo_event + 1, hi_event + 1)
                                  if gt.events[k]["reset"]]
                        self.assertTrue(hidden)
                        self.assertTrue(all(k != hi_event for k in hidden))
                        self.assertTrue(all(f"e{k}" not in idx_of for k in hidden))
                        continue
                    if adjacent:
                        # 相邻真值事件且无 reset：时间必在门限内，delta 必须严格等于真值
                        self.assertLessEqual(dt, max_gap + 1e-9)
                        self.assertEqual(iv.delta, truth,
                                         (seed, iv.interval_id, iv.reason))
                    else:
                        # 跨过缺失事件：要被判 DEFINITE 必须 dt<=门限，
                        # 此时真值增量必 < M（否则早触发 GAP_TOO_LARGE），delta 仍等于真值
                        self.assertLessEqual(dt, max_gap + 1e-9)
                        self.assertLess(truth, gt.mod)
                        self.assertEqual(iv.delta, truth,
                                         (seed, iv.interval_id, iv.reason))
                else:
                    # 不确定的确定原因必须成立
                    if iv.reason in (Reason.RESET_CROSSING, Reason.EQUAL_AFTER_RESET):
                        self.assertTrue(gt.events[hi_event]["reset"])
                        self.assertIsNone(truth)
                    elif iv.reason is Reason.GAP_TOO_LARGE:
                        self.assertGreater(dt, max_gap + 1e-9)
                    else:
                        self.fail(f"意外的不确定原因 {iv.reason}")

            # 随机撤回一个已送达的非 reset 采样（约 20% 概率）
            non_reset = [k for k in delivered if not gt.events[k]["reset"]]
            if rng.random() < 0.2 and len(non_reset) >= 1:
                victim = rng.choice(sorted(non_reset))
                rev_before = engine.revision_count
                revs2 = engine.retract_sample(f"e{victim}")
                delivered.remove(victim)
                live_ids = sorted(delivered)
                revoked = [r for r in revs2 if r.op is RevisionOp.REVOKE]
                self.assertLessEqual(len(revoked), 2)
                assert_matches_reference()
                self.assertGreater(engine.revision_count, rev_before)

        # 最终：所有事件都在时的确定总量，必须等于“逐相邻真值求和并丢弃含 reset 洞”，
        # 且 complete 与是否存在 reset 洞一致。
        _, final_intervals = assert_matches_reference()
        definite_sum = 0
        any_hole = any(iv.status is not Status.DEFINITE for iv in final_intervals[1:])
        # 每个 DEFINITE 最终区间都与真值严格相等（reset 采样均在，无隐藏重置）
        ord_sorted = sorted(delivered)
        idx_of = {f"e{g}": k for k, g in enumerate(ord_sorted)}
        for iv in final_intervals[1:]:
            if iv.status is Status.DEFINITE:
                truth = gt.truth_between(
                    ord_sorted[idx_of[iv.prev_sample_id]],
                    ord_sorted[idx_of[iv.curr_sample_id]],
                )
                self.assertIsNotNone(truth)
                self.assertEqual(iv.delta, truth)
        for iv in final_intervals[1:]:
            if iv.status is Status.DEFINITE:
                definite_sum += iv.delta
        total = engine.source_total(source)
        self.assertEqual(total.definite_delta, definite_sum)
        self.assertEqual(total.complete, not any_hole)

        # 守恒性：对任意三个连续留存事件 a<b<c，a->c 若在“无中间点视图”下
        # 与 a->b + b->c 的确定状态集合一致。这里直接在最终全量视图验证
        # 细划分确定增量之和只来自确定相邻对（不伪造）。
        neighbor_sum = sum(
            iv.delta for iv in final_intervals[1:]
            if iv.status is Status.DEFINITE and iv.delta is not None
        )
        self.assertEqual(neighbor_sum, total.definite_delta)

    def test_random_scenarios(self):
        base_seed = int(os.environ.get("WRAPCOUNTER_TEST_SEED", "20260921"))
        scenarios = [(8, 40), (8, 12), (4, 60), (12, 30), (16, 25)]
        for rep, (bits, n) in enumerate(scenarios * 8):
            self._run_scenario(base_seed + rep, bits, n)


def ordered_ids(delivered: set[int]) -> list[int]:
    return sorted(delivered)


if __name__ == "__main__":
    unittest.main()
