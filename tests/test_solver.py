import random
import unittest

from evidence import (
    EvidenceStore,
    FactVersion,
    MutexRuleVersion,
    solve,
    solve_store,
)
from evidence.bruteforce import brute_solve


def fact(fid, src, lo, hi, rec=1, ver=1):
    return FactVersion(fid, ver, src, rec, lo, hi)


class VisibilityTest(unittest.TestCase):
    def test_late_recorded_evidence_excluded(self):
        store = EvidenceStore()
        store.add_fact_version(fact("F1", "A", 0, 10, rec=3))
        store.add_fact_version(fact("F2", "B", 0, 10, rec=10))
        result = solve_store(store, observation=5)
        self.assertEqual(result.evidence.fact_refs, ("F1@v1",))

    def test_version_selected_by_observation_time(self):
        store = EvidenceStore()
        store.add_fact_version(fact("F1", "A", 0, 5, rec=1, ver=1))
        store.add_fact_version(fact("F1", "A", 2, 8, rec=4, ver=2))
        self.assertEqual(solve_store(store, 2).evidence.fact_refs, ("F1@v1",))
        self.assertEqual(solve_store(store, 6).evidence.fact_refs, ("F1@v2",))

    def test_no_visible_facts(self):
        store = EvidenceStore()
        store.add_fact_version(fact("F1", "A", 0, 5, rec=10))
        result = solve_store(store, observation=5)
        self.assertFalse(result.segments)
        self.assertEqual([r.kind for r in result.reasons], ["NO_VISIBLE_FACTS"])


class SegmentTest(unittest.TestCase):
    def test_half_open_no_common_interval(self):
        # [0,5) 与 [5,9) 无共同区间
        result = solve([fact("G1", "A", 0, 5), fact("G2", "A", 5, 9)], [], 1)
        self.assertFalse(result.feasible_segments())
        kinds = [r.kind for r in result.reasons]
        self.assertIn("EMPTY_COMMON_INTERVAL", kinds)
        # 覆盖区域 [0,9) 全部不可行
        infeasible = [(s.start, s.end) for s in result.segments if s.status == "infeasible"]
        self.assertEqual(infeasible, [(0, 9)])

    def test_single_fact(self):
        result = solve([fact("F1", "A", 2, 7)], [], 1)
        feasible = result.feasible_segments()
        self.assertEqual(len(feasible), 1)
        self.assertEqual((feasible[0].start, feasible[0].end), (2, 7))
        self.assertEqual(feasible[0].cover[0].selection.fact_refs, ("F1@v1",))

    def test_mutex_sources_split_segments(self):
        facts = [
            fact("F1", "sensorA", 0, 6),
            fact("F2", "sensorB", 2, 9),
            fact("F3", "manual", 1, 7),
        ]
        rules = [MutexRuleVersion("R1", 1, frozenset({"sensorB", "manual"}), 0)]
        result = solve(facts, rules, 6)
        feasible = result.feasible_segments()
        self.assertEqual([(s.start, s.end) for s in feasible], [(1, 6)])
        # 分段覆盖：每片至少一个一致选择
        cover = feasible[0].cover
        self.assertEqual((cover[0].start, cover[0].end), (1, 2))
        self.assertEqual(cover[0].selection.fact_refs, ("F1@v1", "F3@v1"))
        self.assertEqual((cover[-1].start, cover[-1].end), (2, 6))
        # 被事实覆盖但不可行的区域
        infeasible = [(s.start, s.end) for s in result.segments if s.status == "infeasible"]
        self.assertEqual(infeasible, [(0, 1), (6, 9)])

    def test_not_global_intersection(self):
        # 全部候选区间交集为空，但存在可行段：不能只取全局交集
        facts = [
            fact("F1", "A", 0, 4),
            fact("F2", "B", 2, 8),
            fact("F3", "C", 5, 10),
        ]
        rules = [MutexRuleVersion("R1", 1, frozenset({"A", "C"}), 0)]
        global_lo = max(f.valid_from for f in facts)
        global_hi = min(f.valid_to for f in facts)
        self.assertGreaterEqual(global_lo, global_hi)  # 全局交集为空
        result = solve(facts, rules, 1)
        feasible = [(s.start, s.end) for s in result.feasible_segments()]
        self.assertEqual(feasible, [(2, 4), (5, 8)])

    def test_unknown_segments_when_truncated(self):
        facts = [
            fact("F1", "sensorA", 0, 6),
            fact("F2", "sensorB", 2, 9),
            fact("F3", "manual", 1, 7),
        ]
        rules = [MutexRuleVersion("R1", 1, frozenset({"sensorB", "manual"}), 0)]
        result = solve(facts, rules, 6, budget=1)
        self.assertTrue(result.truncated)
        unknown = [s for s in result.segments if s.status == "unknown"]
        self.assertTrue(unknown)
        self.assertIn("SEARCH_TRUNCATED", [r.kind for r in result.reasons])
        # 完整搜索时同一区域应为 feasible/infeasible，截断时未知段与可行段不重叠
        full = solve(facts, rules, 6)
        self.assertFalse(full.truncated)
        for u in unknown:
            for f in full.feasible_segments():
                self.assertFalse(u.start < f.end and f.start < u.end
                                 and (u.start, u.end) == (f.start, f.end))


class BruteForceCrossCheckTest(unittest.TestCase):
    def test_random_small_instances_match_bruteforce(self):
        rng = random.Random(20260925)
        sources = ["A", "B", "C"]
        for trial in range(60):
            n = rng.randint(1, 6)
            facts = []
            for i in range(n):
                lo = rng.randint(0, 8)
                hi = lo + rng.randint(1, 6)
                facts.append(fact(f"F{i}", rng.choice(sources), lo, hi,
                                  rec=rng.randint(0, 3)))
            rules = []
            pairs = [(x, y) for x in sources for y in sources if x < y]
            for j, (a, b) in enumerate(random.Random(trial).sample(pairs, k=2)):
                rules.append(MutexRuleVersion(f"R{j}", 1, frozenset({a, b}), 0))
            t = rng.choice([1, 2, 3])
            visible = [f for f in facts if f.recorded_at <= t]
            fast = solve(visible, rules, t)
            slow = brute_solve(visible, rules, t)
            self.assertEqual(fast.digest(), slow.digest(),
                             f"trial={trial} facts={visible} rules={rules}")


if __name__ == "__main__":
    unittest.main()
