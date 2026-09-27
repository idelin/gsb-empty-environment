"""Runnable demos: python3 examples.py"""
import random

from sprt import (BinomialSPRT, Decision, EvidenceProcess, SharedBudget,
                  exact_operating_characteristics, simulate)

P0, P1, ALPHA, BETA, N_MAX = 0.4, 0.6, 0.05, 0.05, 300

print("=" * 72)
print("1) Single stream, sequential decisions (log-accumulated SPRT)")
print("=" * 72)
rng = random.Random(42)                      # caller-injected random source
s = BinomialSPRT(P0, P1, ALPHA, BETA, N_MAX, label="demo")
ev = EvidenceProcess(s)                      # re-queryable evidence process
while True:
    x = 1 if rng.random() < 0.62 else 0      # true p = 0.62
    d = s.step(x)
    if s.n % 10 == 0 or d is not Decision.CONTINUE:
        q = ev.query()
        print(f"  n={q['n']:>3} k={q['successes']:>3} "
              f"log_lr={q['log_lr']:+.3f} e e_value={q['e_value']:.3f} "
              f"-> {q['decision']}")
    if d in (Decision.ACCEPT_H0, Decision.ACCEPT_H1, Decision.UNDECIDED):
        break
print(f"  final: {s.status.name} after n={s.n} samples "
      f"(bounds log_b={s.log_b:.3f}, log_a={s.log_a:.3f})")

print()
print("=" * 72)
print("2) Exact recursion vs Monte Carlo (labelled separately)")
print("=" * 72)
for p, name in [(P0, "p0=0.4 (H0 true)"), (P1, "p1=0.6 (H1 true)")]:
    ex = exact_operating_characteristics(P0, P1, ALPHA, BETA, N_MAX, p)
    emp = simulate(P0, P1, ALPHA, BETA, N_MAX, p, n_rep=4000,
                   rng=random.Random(2026), seed=2026)
    print(f"  sampling at {name}")
    print(f"    [{ex.label}]  P(acc H0)={ex.p_accept_h0:.4f} "
          f"P(acc H1)={ex.p_accept_h1:.4f} "
          f"P(undec)={ex.p_undecided:.2e} E[n]={ex.expected_n:.2f}")
    print(f"    [{emp.label}] P(acc H0)={emp.p_accept_h0:.4f} "
          f"P(acc H1)={emp.p_accept_h1:.4f} "
          f"P(undec)={emp.p_undecided:.4f} E[n]={emp.expected_n:.2f} "
          f"(reps={emp.n_rep}, seed={emp.seed}, "
          f"total_samples={emp.total_samples})")

print()
print("=" * 72)
print("3) Multi-stream shared stopping budget (no double counting)")
print("=" * 72)
budget = SharedBudget(60)
streams = [BinomialSPRT(P0, P1, 1e-4, 1e-4, 1000, budget=budget,
                        label=f"stream-{i}") for i in range(3)]
rng = random.Random(7)
i = 0
while budget.remaining > 0:
    st = streams[i % 3]
    st.step(1 if rng.random() < 0.55 else 0)
    i += 1
for st in streams:
    print(f"  {st.label}: n={st.n} status={st.status.name}")
print(f"  budget: consumed={budget.consumed} remaining={budget.remaining} "
      f"sum(n)={sum(st.n for st in streams)}")
st = streams[0]
before = st.n
print(f"  step with empty budget -> {st.step(1).name} "
      f"(n unchanged: {st.n == before})")
budget.refill(5)
print(f"  after refill(5): step -> {st.step(0).name}, n={st.n} "
      f"(resumed without recounting)")

print()
print("=" * 72)
print("4) Checkpoint / restore consistency")
print("=" * 72)
rng = random.Random(20260928)
data = [1 if rng.random() < 0.58 else 0 for _ in range(200)]
a = BinomialSPRT(P0, P1, ALPHA, BETA, 200)
for x in data[:80]:
    a.step(x)
snap = a.snapshot()
print(f"  checkpoint at n={snap['n']} log_lr={snap['log_lr']:+.6f}")
b = BinomialSPRT.from_snapshot(snap)
for x in data[80:]:
    da, db = a.step(x), b.step(x)
print(f"  original : {a.status.name} at n={a.n}, log_lr={a.log_lr:+.6f}")
print(f"  restored : {b.status.name} at n={b.n}, log_lr={b.log_lr:+.6f}")
print(f"  identical: {a.status is b.status and a.log_lr == b.log_lr}")

print()
print("=" * 72)
print("5) Zero-likelihood hypotheses (handled exactly, no epsilon)")
print("=" * 72)
z = BinomialSPRT(0.0, 0.6, ALPHA, BETA, 100)
print(f"  H0: p=0.0 vs H1: p=0.6, one success -> {z.step(1).name} at n={z.n}")
z2 = BinomialSPRT(0.4, 1.0, ALPHA, BETA, 100)
print(f"  H0: p=0.4 vs H1: p=1.0, one failure -> {z2.step(0).name} at n={z2.n}")
