# -*- coding: utf-8 -*-
import random
import unittest

from trend_df import TrendEngine, tokenize


class TestTokenize(unittest.TestCase):
    def test_rules(self):
        # 小写化；[0-9a-z]+ 成词；每个 CJK 字单独成词；其余为分隔符
        self.assertEqual(tokenize("Hello, 世界! ABC123 中文abc"),
                         ["hello", "世", "界", "abc123", "中", "文", "abc"])
        self.assertEqual(tokenize("全角，标点。分隔"), ["全", "角", "标", "点", "分", "隔"])
        self.assertEqual(tokenize(""), [])


def make_engine(alpha=1.0, min_support=2, window=2):
    return TrendEngine(bucket_span=1, window_buckets=window,
                       alpha=alpha, min_support=min_support)


class TestDocumentFrequency(unittest.TestCase):
    def test_duplicate_term_counts_once_per_doc(self):
        eng = make_engine(min_support=1)
        eng.add_document("a", 0, "手机 手机 手机 电池")
        stats = eng.window_stats(0)
        self.assertEqual(stats.df["手"], 1)   # CJK 单字成词，文档内去重
        self.assertEqual(stats.df["电"], 1)
        self.assertEqual(stats.n, 1)

    def test_denominator_by_visible_identity(self):
        eng = make_engine(min_support=1)
        eng.add_document("a", 0, "x")
        eng.add_document("b", 0, "y")
        self.assertEqual(eng.window_stats(0).n, 2)
        eng.retract_document("a")
        stats = eng.window_stats(0)
        self.assertEqual(stats.n, 1)          # 分母随可见身份减少
        self.assertNotIn("x", stats.df)
        eng.add_document("a", 0, "x")         # 撤回后重新登记
        self.assertEqual(eng.window_stats(0).n, 2)


class TestSmoothingAndSupport(unittest.TestCase):
    def test_zero_to_one_is_smoothed_not_infinite(self):
        eng = make_engine(alpha=1.0, min_support=1)
        eng.add_document("p", 0, "alpha beta")
        eng.add_document("c", 2, "alpha gamma")
        r = eng.compare(prev_end=1, cur_end=3, k=10)
        entry = {e.term: e for e in r.entries}["gamma"]
        self.assertEqual(entry.df_prev, 0)
        self.assertEqual(entry.df_cur, 1)
        # (1+1)/(1+1) / ((0+1)/(1+1)) = 2.0，有限而非无穷
        self.assertAlmostEqual(entry.score, 2.0)

    def test_min_support_filters(self):
        eng = make_engine(min_support=2)
        eng.add_document("p", 0, " filler")
        eng.add_document("c1", 2, "rare")
        eng.add_document("c2", 3, "common")
        eng.add_document("c3", 3, "common")
        r = eng.compare(prev_end=1, cur_end=3, k=10)
        terms = {e.term for e in r.entries}
        self.assertIn("common", terms)        # df_cur=2 达标
        self.assertNotIn("rare", terms)       # df_cur=1 低于最低支持数

    def test_empty_window_never_reports_growth(self):
        eng = make_engine(min_support=1)
        eng.add_document("c", 2, "boom")
        r = eng.compare(prev_end=1, cur_end=3, k=10)   # 上一窗口为空
        self.assertIsNotNone(r.degenerate)
        self.assertEqual(r.entries, [])
        eng2 = make_engine(min_support=1)
        eng2.add_document("p", 0, "old")
        r2 = eng2.compare(prev_end=1, cur_end=3, k=10)  # 当前窗口为空
        self.assertIsNotNone(r2.degenerate)
        self.assertEqual(r2.entries, [])


class TestLateRetractSlide(unittest.TestCase):
    def test_late_document_revises_its_bucket(self):
        eng = make_engine(min_support=1)
        eng.add_document("a", 0, "x")
        before = eng.window_stats(0)
        self.assertNotIn("迟", before.df)
        eng.add_document("late", 0, "迟 到")   # 迟到文档落入历史桶 0
        after = eng.window_stats(0)
        self.assertEqual(after.df["迟"], 1)
        self.assertEqual(after.n, 2)

    def test_retraction_updates_counts_and_denominator(self):
        eng = make_engine(min_support=1)
        eng.add_document("a", 1, "shared uniqa")
        eng.add_document("b", 1, "shared uniqb")
        eng.retract_document("a")
        stats = eng.window_stats(1)
        self.assertEqual(stats.df["shared"], 1)
        self.assertNotIn("uniqa", stats.df)
        self.assertEqual(stats.n, 1)

    def test_retract_missing_is_noop(self):
        eng = make_engine(min_support=1)
        self.assertFalse(eng.retract_document("ghost"))
        self.assertEqual(eng.window_stats(0).n, 0)

    def test_window_advance_drops_old_bucket(self):
        eng = make_engine(min_support=1, window=2)
        eng.add_document("old", 0, "ancient")
        eng.add_document("new", 3, "modern")
        stats = eng.window_stats(3)            # 窗口覆盖桶 [2,3]
        self.assertNotIn("ancient", stats.df)
        self.assertEqual(stats.n, 1)

    def test_duplicate_event_idempotent(self):
        eng = make_engine(min_support=1)
        self.assertTrue(eng.add_document("a", 0, "x"))
        self.assertFalse(eng.add_document("a", 0, "x"))  # 重复事件
        stats = eng.window_stats(0)
        self.assertEqual(stats.n, 1)
        self.assertEqual(stats.df["x"], 1)
        self.assertEqual(eng.duplicate_events, 1)


class TestTopKPruning(unittest.TestCase):
    def test_topk_matches_bruteforce_and_prunes(self):
        eng = make_engine(alpha=1.0, min_support=2, window=2)
        eng.add_document("p0", 0, "filler0")
        eng.add_document("p1", 1, "filler1")
        # 5 个强趋势词：当前窗口 df=3，上一窗口 df=0
        for i in range(5):
            for j in range(3):
                eng.add_document(f"s{i}_{j}", 2 + (j % 2), f"strong{i}")
        # 30 个弱词：当前窗口 df=2，上界低于强词精确分
        for i in range(30):
            for j in range(2):
                eng.add_document(f"w{i}_{j}", 2 + (j % 2), f"weak{i}")
        r = eng.compare(prev_end=1, cur_end=3, k=5)
        # 暴力全量求值对照
        prev, cur = eng.window_stats(1), eng.window_stats(3)
        brute = sorted(
            ((eng.smoothed_ratio(c, cur.n, prev.df.get(t, 0), prev.n), t)
             for t, c in cur.df.items() if c >= eng.min_support),
            key=lambda st: (-st[0], st[1]))[:5]
        self.assertEqual([e.term for e in r.entries], [t for _, t in brute])
        self.assertEqual([e.term for e in r.entries],
                         [f"strong{i}" for i in range(5)])
        self.assertEqual(r.evaluated, 5)       # 只精确求值 5 个
        self.assertEqual(r.pruned, 30)         # 其余被上界剪枝
        self.assertEqual(r.candidates, 35)


class TestChecker(unittest.TestCase):
    def test_randomized_events_agree_with_full_recompute(self):
        rng = random.Random(20260926)
        eng = TrendEngine(bucket_span=2, window_buckets=3,
                          alpha=0.5, min_support=2)
        vocab = ["apple", "banana", "手", "机", "电", "池", "x1", "y2"]
        live = []
        for step in range(300):
            roll = rng.random()
            if roll < 0.55 or not live:
                doc_id = f"d{step}"
                # 10% 概率迟到：时间戳落在过去
                ts = rng.randint(0, 20) if rng.random() < 0.1 else 10 + step % 15
                text = " ".join(rng.sample(vocab, rng.randint(1, 4)))
                if eng.add_document(doc_id, ts, text):
                    live.append(doc_id)
            elif roll < 0.8:
                victim = rng.choice(live)
                if eng.retract_document(victim):
                    live.remove(victim)
            else:
                # 重复事件
                eng.add_document(rng.choice(live), 10, "dup")
            if step % 25 == 0:
                report = eng.verify(prev_end=8, cur_end=11)
                self.assertTrue(report.ok,
                                f"step {step}: {report.corrections}")
        report = eng.verify(prev_end=8, cur_end=11)
        self.assertTrue(report.ok)
        self.assertGreater(report.duplicates_ignored, 0)   # 确实覆盖了重复事件
        self.assertEqual(report.duplicates_ignored, eng.duplicate_events)
        # 分母与可见身份一致
        stats = eng.window_stats(11)
        self.assertEqual(stats.n, report.recomputed_n_cur)

    def test_checker_reports_contribution_evidence_on_corruption(self):
        eng = make_engine(min_support=1)
        eng.add_document("a", 0, "x shared")
        eng.add_document("b", 1, "y shared")
        eng.buckets[1].df["shared"] = 5        # 人为注入状态损坏
        report = eng.verify(prev_end=0, cur_end=1)
        self.assertFalse(report.ok)
        df_corr = [c for c in report.corrections
                   if c.kind == "df" and c.term == "shared"]
        self.assertEqual(len(df_corr), 1)
        evidence = df_corr[0]
        self.assertEqual(evidence.incremental, 6)  # 桶0(1)+桶1(被篡改为5)
        self.assertEqual(evidence.recomputed, 2)
        self.assertEqual(evidence.contributing_docs, ("a", "b"))  # 贡献证据

    def test_checker_flags_zero_windows(self):
        eng = make_engine(min_support=1)
        eng.add_document("a", 5, "x")
        report = eng.verify(prev_end=1, cur_end=6)   # prev 窗 [ -1..1 ] 为空
        self.assertTrue(report.prev_empty)
        self.assertFalse(report.cur_empty)
        r = eng.compare(prev_end=1, cur_end=6, k=5)
        self.assertIsNotNone(r.degenerate)           # 零窗不伪报增长


if __name__ == "__main__":
    unittest.main()
