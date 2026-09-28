import json
import math
import unittest

from seqtest import (
    Decision,
    Evidence,
    ScriptedSampler,
    SeededBernoulli,
    SprtConfig,
    exact_performance,
    run_once,
    simulate,
)
from seqtest.multistream import MultiStreamStudy


def aligned_config(p0=0.4, p1=0.6, n_max=2):
    """边界取 2 个成功 / 2 个失败权重，构造可手算且精确命中边界的用例。"""
    c = SprtConfig(p0, p1, bounds=(2.0, 0.5), n_max=100)
    return SprtConfig(p0, p1, log_bounds=(2 * c.weight(1), 2 * c.weight(0)), n_max=n_max)


class TestExactRecursion(unittest.TestCase):
    def test_hand_calculated_nmax2(self):
        cfg = aligned_config(0.4, 0.6, n_max=2)
        r0 = exact_performance(cfg, 0.4)
        r1 = exact_performance(cfg, 0.6)
        # n=1 不触界；n=2: k=2 接受 H1, k=0 接受 H0, k=1 未决
        self.assertAlmostEqual(r0.accept_h1, 0.16)
        self.assertAlmostEqual(r0.accept_h0, 0.36)
        self.assertAlmostEqual(r0.inconclusive, 0.48)
        self.assertAlmostEqual(r0.expected_n, 2.0)
        self.assertAlmostEqual(r1.accept_h1, 0.36)
        self.assertAlmostEqual(r1.accept_h0, 0.16)
        self.assertAlmostEqual(r1.inconclusive, 0.48)
        self.assertEqual(r0.kind, "exact")

    def test_probability_mass_normalizes_to_one(self):
        cfg = SprtConfig(0.35, 0.65, bounds=(9.0, 1.0 / 9.0), n_max=40)
        for p in (0.35, 0.5, 0.65):
            r = exact_performance(cfg, p)
            self.assertAlmostEqual(r.accept_h1 + r.accept_h0 + r.inconclusive, 1.0, places=12)

    def test_boundary_hit_at_nmax_is_decisive(self):
        cfg = aligned_config(0.4, 0.6, n_max=2)
        ev = Evidence.replay(cfg, [1, 1])
        self.assertEqual(ev.decision(), Decision.ACCEPT_H1)
        self.assertEqual(ev.n, 2)  # 不是 INCONCLUSIVE：闭区间命中优先

    def test_nmax_reached_undecided_is_inconclusive(self):
        cfg = aligned_config(0.4, 0.6, n_max=2)
        ev = Evidence.replay(cfg, [1, 0])
        self.assertEqual(ev.decision(), Decision.INCONCLUSIVE)


class TestOnlineConsistency(unittest.TestCase):
    def test_online_matches_canonical_and_exact(self):
        cfg = SprtConfig(0.4, 0.6, bounds=(20.0, 0.05), n_max=60)
        sampler = ScriptedSampler([1, 0, 1, 1, 0, 0, 1], cyclic=True)
        ev = Evidence.fresh(cfg)
        while not Decision.is_terminal(ev.decision()):
            ev.add_observation(sampler.sample())
        self.assertEqual(ev.log_lr, ev.canonical_log_lr())

    def test_repeatable_queries_do_not_mutate(self):
        cfg = SprtConfig(0.4, 0.6, bounds=(20.0, 0.05), n_max=60)
        ev = Evidence.replay(cfg, [1, 1, 0, 1, 0])
        d = ev.decision()
        n = ev.n
        for _ in range(5):
            self.assertEqual(ev.decision(), d)
        self.assertEqual(ev.n, n)

    def test_terminal_rejects_more_observations(self):
        cfg = aligned_config(0.4, 0.6, n_max=5)
        ev = Evidence.replay(cfg, [1, 1])
        self.assertEqual(ev.decision(), Decision.ACCEPT_H1)
        with self.assertRaises(RuntimeError):
            ev.add_observation(0)


class TestDegenerateLikelihood(unittest.TestCase):
    def test_p0_zero_observation_one_is_infinite_evidence(self):
        cfg = SprtConfig(0.0, 0.5, bounds=(4.0, 0.25), n_max=10)
        ev = Evidence.fresh(cfg)
        ev.add_observation(1)
        self.assertEqual(ev.log_lr, math.inf)
        self.assertEqual(ev.decision(), Decision.ACCEPT_H1)

    def test_p1_one_observation_one_is_negative_infinite(self):
        cfg = SprtConfig(0.5, 1.0, bounds=(4.0, 0.25), n_max=10)
        ev = Evidence.fresh(cfg)
        ev.add_observation(0)
        self.assertEqual(ev.log_lr, -math.inf)
        self.assertEqual(ev.decision(), Decision.ACCEPT_H0)

    def test_zero_probability_paths_excluded_from_exact_recursion(self):
        cfg = SprtConfig(0.0, 0.5, bounds=(4.0, 0.25), n_max=10)
        r0 = exact_performance(cfg, 0.0)
        r1 = exact_performance(cfg, 0.5)
        self.assertEqual(r0.accept_h1, 0.0)
        self.assertEqual(r0.accept_h0, 1.0)
        self.assertEqual(r0.expected_n, 2.0)
        self.assertAlmostEqual(r1.accept_h1, 0.75)
        self.assertAlmostEqual(r1.expected_n, 1.5)


class TestCheckpoint(unittest.TestCase):
    def test_snapshot_restore_matches_replay(self):
        cfg = SprtConfig(0.4, 0.6, bounds=(20.0, 0.05), n_max=80)
        obs = [1, 0, 0, 1, 1, 0, 1, 0, 0, 1, 1, 1, 0, 0, 1, 0, 1, 0]
        partial = Evidence.replay(cfg, obs[:8])
        restored = Evidence.from_snapshot(json.loads(json.dumps(partial.snapshot())))
        for x in obs[8:]:
            if Decision.is_terminal(restored.decision()):
                break
            restored.add_observation(x)
        full = Evidence.replay(cfg, obs)
        self.assertEqual(restored.decision(), full.decision())
        self.assertEqual(restored.n, full.n)

    def test_corrupt_checkpoint_rejected(self):
        cfg = SprtConfig(0.4, 0.6, bounds=(20.0, 0.05), n_max=20)
        ev = Evidence.replay(cfg, [1, 1, 0])
        snap = ev.snapshot()
        snap["k"] = ev.k + 1
        with self.assertRaises(ValueError):
            Evidence.from_snapshot(snap)


class TestBudgetInterruption(unittest.TestCase):
    def test_interruption_does_not_draw_and_resume_continues_once(self):
        cfg = SprtConfig(0.4, 0.6, bounds=(20.0, 0.05), n_max=100)
        sampler = ScriptedSampler([1, 1, 0, 1, 0, 1, 0, 0, 0], cyclic=True)
        ev = Evidence.fresh(cfg)
        rec = run_once(cfg, sampler, ev, sample_budget=3)
        self.assertTrue(rec.interrupted)
        self.assertEqual(rec.n, 3)
        self.assertEqual(sampler.drawn, 3)  # 中断时没有多抽
        self.assertEqual(ev.decision(), Decision.CONTINUE)
        # 恢复后沿用同一证据，不重复计入前 3 个样本
        rec2 = run_once(cfg, sampler, ev, sample_budget=100)
        self.assertFalse(rec2.interrupted)
        self.assertGreaterEqual(rec2.n, 3)
        self.assertEqual(rec2.n, ev.n)


class TestEmpiricalSimulation(unittest.TestCase):
    def test_empirical_close_to_exact(self):
        cfg = SprtConfig(0.4, 0.6, bounds=(20.0, 0.05), n_max=100)
        exact0 = exact_performance(cfg, 0.4)
        emp = simulate(cfg, 0.4, replications=20000, base_seed=12345)
        self.assertEqual(emp.kind, "empirical")
        self.assertEqual(len(emp.seeds), 20000)
        self.assertLess(abs(emp.accept_h1 - exact0.accept_h1), 5 * emp.se_accept_h1)
        self.assertLess(abs(emp.inconclusive - exact0.inconclusive), 5 * emp.se_inconclusive)
        self.assertLess(abs(emp.mean_n - exact0.expected_n), 5 * emp.se_mean_n)

    def test_seed_reproducibility(self):
        cfg = SprtConfig(0.4, 0.6, bounds=(20.0, 0.05), n_max=100)
        a = simulate(cfg, 0.4, replications=2000, base_seed=999)
        b = simulate(cfg, 0.4, replications=2000, base_seed=999)
        self.assertEqual((a.accept_h1, a.mean_n), (b.accept_h1, b.mean_n))
        self.assertEqual(a.seeds, b.seeds)


class TestMultiStream(unittest.TestCase):
    def test_shared_sample_budget_interrupts_without_double_count(self):
        cfg = SprtConfig(0.4, 0.6, bounds=(20.0, 0.05), n_max=100)
        study = MultiStreamStudy(sample_budget=5)
        study.declare("a", cfg, alpha_allocation=1.0)
        study.declare("b", cfg, alpha_allocation=1.0)
        study.attach_sampler("a", ScriptedSampler([0], cyclic=True))
        study.attach_sampler("b", ScriptedSampler([1], cyclic=True))
        st_a = study.run("a")
        self.assertTrue(st_a.interrupted)
        self.assertEqual(study.total_sampled, 5)
        st_b = study.run("b")
        self.assertTrue(st_b.interrupted)
        self.assertEqual(st_b.n, 0)  # 预算已耗尽，一个样本都没抽
        self.assertEqual(study.total_sampled, 5)
        n_a = st_a.n
        study.refill_budget(200)
        st_a2 = study.run("a")
        self.assertFalse(st_a2.interrupted)
        self.assertGreater(st_a2.n, n_a)

    def test_family_alpha_cap_enforced_only_for_declared_rules(self):
        cfg = SprtConfig(0.4, 0.6, bounds=(19.0, 1.0 / 19.0), n_max=100)
        study = MultiStreamStudy(family_alpha_cap=0.05)
        study.declare("a", cfg, alpha_allocation=0.05)
        with self.assertRaises(ValueError):
            study.declare("b", cfg, alpha_allocation=0.05)  # 超出族上界
        # 未声明的流无法运行（不在覆盖保证内）
        with self.assertRaises(KeyError):
            study.step("undeclared")
        cov = study.coverage_statement()
        self.assertLessEqual(cov["sum_exact_alpha"], cov["sum_allocated"])

    def test_declaration_rejects_rule_exceeding_allocation(self):
        cfg = SprtConfig(0.4, 0.6, bounds=(4.0, 0.25), n_max=100)
        study = MultiStreamStudy(family_alpha_cap=0.5)
        with self.assertRaises(ValueError):
            study.declare("loose", cfg, alpha_allocation=0.001)


class TestConfigValidation(unittest.TestCase):
    def test_invalid_bounds_rejected(self):
        with self.assertRaises(ValueError):
            SprtConfig(0.4, 0.6, bounds=(1.0, 0.5), n_max=10)
        with self.assertRaises(ValueError):
            SprtConfig(0.4, 0.6, bounds=(2.0, 1.0), n_max=10)
        with self.assertRaises(ValueError):
            SprtConfig(0.4, 0.4, bounds=(2.0, 0.5), n_max=10)
        with self.assertRaises(ValueError):
            SprtConfig(0.4, 0.6, log_bounds=(-1.0, -2.0), n_max=10)


if __name__ == "__main__":
    unittest.main(verbosity=2)
