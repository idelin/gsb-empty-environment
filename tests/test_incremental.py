import random
import unittest

from evidence import EvidenceStore, FactVersion, IncrementalSolver, MutexRuleVersion, solve_store


def fact(fid, src, lo, hi, rec=1, ver=1):
    return FactVersion(fid, ver, src, rec, lo, hi)


def base_store():
    store = EvidenceStore()
    store.add_fact_version(fact("F1", "sensorA", 0, 6, rec=4, ver=2))
    store.add_fact_version(fact("F2", "sensorB", 2, 9, rec=2))
    store.add_fact_version(fact("F3", "manual", 1, 7, rec=3))
    store.add_rule_version(MutexRuleVersion("R1", 1, frozenset({"sensorB", "manual"}), 0))
    return store


class IncrementalTest(unittest.TestCase):
    def test_interval_update_revokes_only_affected(self):
        store = base_store()
        inc = IncrementalSolver(store, observation=6)
        old_ids = {s.segment_id for s in inc.result.segments}
        report = inc.update_fact(fact("F2", "sensorB", 3, 9, rec=5, ver=2))
        self.assertFalse(report.full_refresh)
        self.assertTrue(report.revoked)
        # 撤销与保留的段都来自旧结果；保留段不触及 span
        self.assertTrue(set(report.revoked) <= old_ids)
        self.assertTrue(set(report.kept) <= old_ids)
        self.assertFalse(set(report.revoked) & set(report.kept))
        # 与全量重算一致
        self.assertEqual(inc.result.digest(), solve_store(store, 6).digest())

    def test_disjoint_move_keeps_far_segments(self):
        store = EvidenceStore()
        store.add_fact_version(fact("A", "x", 0, 5, rec=1))
        store.add_fact_version(fact("B", "y", 100, 110, rec=1))
        store.add_rule_version(MutexRuleVersion("RX", 1, frozenset({"x", "y"}), 0))
        inc = IncrementalSolver(store, observation=2)
        far_before = [s.segment_id for s in inc.result.segments if s.start >= 100]
        report = inc.update_fact(fact("A", "x", 0, 4, rec=2, ver=2))
        self.assertFalse(report.full_refresh)
        # 远处 [100,110) 段不触及 span，必须按 ID 保留
        for seg_id in far_before:
            self.assertIn(seg_id, report.kept)
        self.assertEqual(inc.result.digest(), solve_store(store, 2).digest())

    def test_source_change_full_refresh(self):
        store = base_store()
        inc = IncrementalSolver(store, observation=6)
        report = inc.update_fact(fact("F2", "manual", 2, 9, rec=5, ver=2))
        self.assertTrue(report.full_refresh)
        self.assertEqual(inc.result.digest(), solve_store(store, 6).digest())

    def test_empty_intersection_clique_falls_back_to_full_refresh(self):
        # F1[0,7) 与 F2[8,9) 交集为空（空交集团），其影响不能用 F2 区间界定，
        # 必须全量回退；更新后 F2 与 F1 区间重叠，产生新的可行段。
        store = EvidenceStore()
        store.add_fact_version(fact("F1", "A", 0, 7, rec=1))
        store.add_fact_version(fact("F2", "B", 8, 9, rec=1))
        inc = IncrementalSolver(store, observation=2)
        report = inc.update_fact(fact("F2", "B", 5, 9, rec=2, ver=2))
        self.assertTrue(report.full_refresh)
        self.assertEqual(inc.result.digest(), solve_store(store, 2).digest())
        feasible = inc.result.feasible_segments()
        self.assertEqual([(s.start, s.end) for s in feasible], [(5, 7)])

    def test_noop_update(self):
        store = base_store()
        inc = IncrementalSolver(store, observation=6)
        before = inc.result.digest()
        # 晚于观察时刻录入 -> 可见版本不变
        report = inc.update_fact(fact("F2", "sensorB", 0, 100, rec=10, ver=2))
        self.assertEqual(report.revoked, ())
        self.assertEqual(inc.result.digest(), before)

    def test_random_update_sequences_match_full_recompute(self):
        rng = random.Random(7)
        for trial in range(40):
            store = EvidenceStore()
            sources = ["A", "B", "C"]
            n = rng.randint(2, 5)
            for i in range(n):
                lo = rng.randint(0, 8)
                store.add_fact_version(
                    fact(f"F{i}", rng.choice(sources), lo, lo + rng.randint(1, 5), rec=0))
            store.add_rule_version(MutexRuleVersion("R1", 1, frozenset({"A", "B"}), 0))
            t = 2
            inc = IncrementalSolver(store, observation=t)
            next_ver = {f"F{i}": 2 for i in range(n)}
            for step in range(rng.randint(1, 4)):
                fid = f"F{rng.randrange(n)}"
                lo = rng.randint(0, 8)
                src = rng.choice(sources) if rng.random() < 0.3 else None
                current = {f.fact_id: f for f in store.visible_facts(t)}[fid]
                ver = next_ver[fid]
                next_ver[fid] += 1
                fv = FactVersion(fid, ver,
                                 src or current.source,
                                 recorded_at=rng.randint(1, 3),
                                 valid_from=lo, valid_to=lo + rng.randint(1, 5))
                inc.update_fact(fv)
                self.assertEqual(inc.result.digest(), solve_store(store, t).digest(),
                                 f"trial={trial} step={step} fv={fv}")


if __name__ == "__main__":
    unittest.main()
