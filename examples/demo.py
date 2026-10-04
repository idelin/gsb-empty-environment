"""End-to-end demo of the hmm package (Python 3.11, standard library only).

Run:  python3.11 examples/demo.py
"""
import math
import random
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent.parent))

from hmm import (
    HMM,
    MISSING,
    NEG_INF,
    VersionedInference,
    accumulate_path_probabilities,
    estimate_state_posterior,
    forward_backward,
    path_log_joint,
    rank_topk_paths,
    sample_sequences,
)


def fmt_row(row):
    return "(" + ", ".join(f"{p:.4f}" for p in row) + ")"


def section(title):
    print()
    print("=" * 68)
    print(title)
    print("=" * 68)


# A weather-like model: states 0=sunny, 1=rainy; observations walk/shop/clean.
weather = HMM(
    n_states=2,
    initial=(0.6, 0.4),
    transition=((0.7, 0.3), (0.4, 0.6)),
    emission=(
        {"walk": 0.6, "shop": 0.3, "clean": 0.1},
        {"walk": 0.1, "shop": 0.4, "clean": 0.5},
    ),
    name="weather",
)

section("1. Forward-backward + Viterbi (exact, log-space)")
obs = ("walk", "shop", "clean", "clean", "walk")
res = forward_backward(weather, obs)
print(f"observations      : {obs}")
print(f"kind              : {res.kind}")
print(f"log-evidence      : {res.log_evidence:.6f}  (evidence = {math.exp(res.log_evidence):.3e})")
for t, row in enumerate(res.posteriors):
    print(f"  P(S_{t} | obs)    : {fmt_row(row)}")
print(f"viterbi path      : {res.viterbi_path}  log-joint = {res.viterbi_logprob:.6f}")

section("2. Pointwise MAP != Viterbi path")
m2 = HMM(
    n_states=2,
    initial=(0.75, 0.25),
    transition=((0.9, 0.1), (0.1, 0.9)),
    emission=({"x": 0.5, "y": 0.5}, {"x": 0.9, "y": 0.1}),
)
r2 = forward_backward(m2, ("x", "x"))
pointwise_map = tuple(max(range(2), key=lambda j: row[j]) for row in r2.posteriors)
for t, row in enumerate(r2.posteriors):
    print(f"  P(S_{t} | obs)    : {fmt_row(row)}")
print(f"pointwise MAP     : {pointwise_map}  log-joint = {path_log_joint(m2, ('x', 'x'), pointwise_map):.6f}")
print(f"viterbi path      : {r2.viterbi_path}  log-joint = {r2.viterbi_logprob:.6f}")
print("-> the per-time most likely states are NOT the best joint path")

section("3. Missing observations (MISSING carries no information)")
obs_missing = ("walk", MISSING, "clean")
r3 = forward_backward(weather, obs_missing)
print(f"observations      : {obs_missing}")
print(f"log-evidence      : {r3.log_evidence:.6f}")
for t, row in enumerate(r3.posteriors):
    print(f"  P(S_{t} | obs)    : {fmt_row(row)}")
r3b = forward_backward(weather, (MISSING, MISSING, MISSING))
print(f"all-missing evidence = {math.exp(r3b.log_evidence):.1f} (no information)")

section("4. Constrained state path (state at t=2 forced to 1)")
r4 = forward_backward(weather, obs, constraints=(None, None, {1}, None, None))
print(f"log-evidence      : {r4.log_evidence:.6f}")
for t, row in enumerate(r4.posteriors):
    print(f"  P(S_{t} | obs)    : {fmt_row(row)}")

section("5. Piecewise (segmented) transition matrices")
calm = ((0.9, 0.1), (0.8, 0.2))
stormy = ((0.4, 0.6), (0.2, 0.8))
r5 = forward_backward(weather, obs, transitions=[calm, calm, stormy, stormy])
print("segments          : calm, calm, stormy, stormy")
print(f"log-evidence      : {r5.log_evidence:.6f}")
print(f"viterbi path      : {r5.viterbi_path}")

section("6. Top-3 distinct paths: ranking, then probability accumulation")
ranked = rank_topk_paths(weather, obs, 3)
acc = accumulate_path_probabilities(weather, obs, ranked)
for e in acc.entries:
    print(f"  #{e.rank} path={e.path}  P(path,obs)={e.joint_prob:.3e}"
          f"  P(path|obs)={e.conditional_prob:.4f}  cumulative={e.cumulative_conditional:.4f}")
print(f"covered mass      : {acc.covered_conditional_mass:.4f}"
      f"  residual (all other paths) <= {acc.residual_conditional_mass:.4f}")

section("7. Incremental revision with immutable versions")
vi = VersionedInference(weather, obs)
print(f"version {vi.version}: log-evidence = {vi.log_evidence():.6f}")
v1 = vi.revise(2, "walk")  # local observation revision at t=2
print(f"version {v1}: revised obs[2] -> 'walk', log-evidence = {vi.log_evidence():.6f}")
print(f"  P(S_2|obs) v0   : {fmt_row(vi.posterior(2, version=0))}  (old query preserved)")
print(f"  P(S_2|obs) v{v1}   : {fmt_row(vi.posterior(2))}")

section("8. Zero-probability sequence: explicitly infeasible")
bad = forward_backward(weather, ("walk", "cook", "shop"))  # 'cook' never emitted
print(f"feasible          : {bad.feasible}")
print(f"log-evidence      : {bad.log_evidence}")
print(f"posteriors        : {bad.posteriors}   (None, never a renormalized fake)")
print(f"viterbi path      : {bad.viterbi_path}   (no path exists)")

section("9. Empty sequence (model convention)")
empty = forward_backward(weather, ())
print(f"feasible={empty.feasible}  log-evidence={empty.log_evidence}"
      f"  posteriors={empty.posteriors}  viterbi={empty.viterbi_path}")

section("10. Sampling: caller-injected RNG, seed + sample size recorded")
seed = 20261004
rng = random.Random(seed)
batch = sample_sequences(weather, length=4, n=5, rng=rng, seed=seed)
print(f"kind={batch.kind}  seed={batch.seed}  n_samples={batch.n_samples}")
for st, ob in zip(batch.states, batch.observations):
    print(f"  states={st}  obs={ob}")
obs_q = ("walk", "shop")
exact = forward_backward(weather, obs_q)
est = estimate_state_posterior(weather, obs_q, time=1, n=20000,
                               rng=random.Random(seed), seed=seed)
print(f"[exact]     P(S_1 | {obs_q}) = {fmt_row(exact.posteriors[1])}")
print(f"[empirical] same, seed={est.seed}, n={est.n_samples}, "
      f"accepted={est.n_accepted}: {fmt_row(est.estimates)}")
