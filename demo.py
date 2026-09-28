"""运行样例：二项序贯 SPRT 的精确结论与经验估计对照。

运行：python3.11 demo.py
"""
from __future__ import annotations

import json

from seqtest import (
    Decision,
    Evidence,
    ScriptedSampler,
    SeededBernoulli,
    SprtConfig,
    exact_operating_points,
    exact_performance,
    simulate,
)
from seqtest.multistream import MultiStreamStudy


def line(t: str) -> None:
    print(t)


def main() -> None:
    line("=" * 68)
    line("二项序贯检验 SPRT 运行样例")
    line("H0: Bernoulli(0.4) vs H1: Bernoulli(0.6)，A=20, B=0.05，n_max=100")
    cfg = SprtConfig(0.4, 0.6, bounds=(20.0, 0.05), n_max=100)

    line("-" * 68)
    line("[精确结论] 离散小样本格点递推（kind=exact，非模拟、非固定样本 p 值）")
    r0, r1 = exact_operating_points(cfg)
    line(f"  H0 为真: alpha(错误接受H1)={r0.accept_h1:.6f}  "
         f"未决概率={r0.inconclusive:.6f}  期望停止样本数 E[N]={r0.expected_n:.3f}")
    line(f"  H1 为真: power(接受H1)={r1.accept_h1:.6f}  beta={r1.accept_h0:.6f}  "
         f"未决={r1.inconclusive:.6f}  E[N]={r1.expected_n:.3f}")

    line("-" * 68)
    line("[经验估计] 蒙特卡洛 20000 次（kind=empirical，随机源外部注入，带 seed）")
    for true_p, tag in ((0.4, "H0"), (0.6, "H1")):
        emp = simulate(cfg, true_p, replications=20000, base_seed=20260929)
        line(f"  {tag} 为真: 接受H1={emp.accept_h1:.4f}±{emp.se_accept_h1:.4f}  "
             f"未决={emp.inconclusive:.4f}  平均N={emp.mean_n:.2f}±{emp.se_mean_n:.3f}  "
             f"(base_seed={emp.base_seed})")

    line("-" * 68)
    line("[可重复查询的证据过程] 同一状态多次查询结果不变")
    sampler = SeededBernoulli(0.6, seed=42)
    ev = Evidence.fresh(cfg)
    decisions_seen = []
    while not Decision.is_terminal(ev.decision()):
        d = ev.add_observation(sampler.sample())
        decisions_seen.append(d)
    d1, d2 = ev.decision(), ev.decision()
    line(f"  本次: n={ev.n}, k={ev.k}, 判定={d1}; 重复查询一致={d1 == d2}; seed={sampler.seed}")

    line("-" * 68)
    line("[checkpoint 恢复一致] 在未决处快照，恢复后续跑与全程重放结论一致")
    obs = [1, 0, 1, 0, 0, 1, 1, 0, 0, 1, 0, 0, 1, 1, 0, 0, 1, 1, 0, 0,
           0, 0, 0, 1, 0, 0, 1, 1, 1, 0]
    half = Evidence.replay(cfg, obs[:10])
    assert half.decision() == Decision.CONTINUE
    snap = half.snapshot()
    restored = Evidence.from_snapshot(json.loads(json.dumps(snap)))
    tail = obs[10:]
    tail_sampler = ScriptedSampler(tail, cyclic=True)
    consumed_tail = []
    while not Decision.is_terminal(restored.decision()):
        x = tail_sampler.sample()
        consumed_tail.append(x)
        restored.add_observation(x)
    replayed = Evidence.replay(cfg, obs[:10] + consumed_tail)
    line(f"  快照@n=10（{Decision.CONTINUE}）-> 恢复续跑 -> n={restored.n}, "
         f"判定={restored.decision()}；全程重放判定={replayed.decision()}，"
         f"n={replayed.n}，一致={restored.decision()==replayed.decision() and restored.n==replayed.n}")

    line("-" * 68)
    line("[预算中断与恢复不重复计样本] 样本预算耗尽时不产生该次抽样")
    budget_sampler = ScriptedSampler([1, 1, 0, 1, 0], cyclic=True)
    rec1 = run_with_budget(cfg, budget_sampler, Evidence.fresh(cfg), budget=2)
    n_at_stop = rec1.n
    line(f"  预算=2: interrupted={rec1.interrupted}, n={n_at_stop}, 已抽样={budget_sampler.drawn}")
    evb = Evidence.replay(cfg, [1, 1])
    cont_sampler = ScriptedSampler([0, 1, 0, 1, 1, 0, 0, 0], cyclic=True)
    rec_resume = run_with_budget(cfg, cont_sampler, evb, budget=100)
    line(f"  从 n={n_at_stop} 恢复后续跑: n={rec_resume.n}, 判定={rec_resume.decision}，恢复前样本未重复计入")

    line("-" * 68)
    line("[概率 0/1 的似然单独处理] H0: p=0，观测到 1 即似然比 +inf，立即接受 H1")
    deg = SprtConfig(0.0, 0.5, bounds=(4.0, 0.25), n_max=10)
    ev_zero = Evidence.fresh(deg)
    ev_zero.add_observation(0)
    line(f"  观测 0: log_lr={ev_zero.log_lr:g}, 判定={ev_zero.decision()}")
    ev_one = Evidence.fresh(deg)
    ev_one.add_observation(1)
    line(f"  观测 1: log_lr={ev_one.log_lr:g}, 判定={ev_one.decision()}")
    ed0 = exact_performance(deg, 0.0)
    ed1 = exact_performance(deg, 0.5)
    line(f"  [精确] p=0: alpha={ed0.accept_h1:g}, E[N]={ed0.expected_n:g}; "
         f"p=0.5: power={ed1.accept_h1:g}, E[N]={ed1.expected_n:g}")

    line("-" * 68)
    line("[多流共享停止预算 + 声明规则覆盖] 覆盖保证仅限声明的规则")
    study = MultiStreamStudy(sample_budget=50, family_alpha_cap=0.10)
    for name, p0, p1 in (("stream_A", 0.4, 0.6), ("stream_B", 0.3, 0.7)):
        c_i = SprtConfig(p0, p1, bounds=(19.0, 1.0 / 19.0), n_max=100)
        study.declare(name, c_i, alpha_allocation=0.05)
        study.attach_sampler(name, SeededBernoulli((p0 + p1) / 2, seed=name))
    results = study.run_all()
    for name, st in results.items():
        tag = "预算中断" if st.interrupted else st.decision
        line(f"  {name}: n={st.n}, 状态={tag}, 精确alpha={st.exact_alpha:.5f}")
    line(f"  总抽样数={study.total_sampled}（共享预算上限 50）")
    study.refill_budget(100)
    for name in list(results):
        if results[name].interrupted:
            st2 = study.run(name)
            line(f"  追加预算后恢复 {name}: n={st2.n}, 判定={st2.decision}（中断期间未抽样、不重复计入）")
    cov = study.coverage_statement()
    line(f"  覆盖范围: {cov['scope']}")
    line(f"  声明规则精确alpha之和={cov['sum_exact_alpha']:.5f} <= 分配之和={cov['sum_allocated']:g} <= 族上界={cov['family_alpha_cap']:g}")
    line(f"  并集界族级保证成立={cov['family_wise_guarantee_holds']}（仅限声明规则）")
    line("=" * 68)


def run_with_budget(cfg, sampler, ev, *, budget):
    from seqtest.runner import run_once
    return run_once(cfg, sampler, ev, sample_budget=budget)


if __name__ == "__main__":
    main()
