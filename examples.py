"""End-to-end demo of the hmm toolkit (Python 3.11, standard library only).

Run:  python3.11 examples.py
"""
import math
import random

from hmm import (
    HMM,
    PiecewiseHMM,
    VersionedObservations,
    accumulate_log_probability,
    exact_evidence,
    estimate_state_frequencies,
    forward_backward,
    pointwise_map,
    sample_path,
    topk_paths,
    viterbi,
)


def show(title):
    print(f"\n=== {title} ===")


def main():
    model = HMM(
        initial=[0.6, 0.4],
        transition=[[0.7, 0.3], [0.4, 0.6]],
        emission=[
            {"walk": 0.6, "shop": 0.3, "clean": 0.1},
            {"walk": 0.1, "shop": 0.4, "clean": 0.5},
        ],
    )
    obs = ["walk", "shop", "clean", "walk", None, "shop"]  # None = missing

    show("forward-backward (missing observation at t=4)")
    fb = forward_backward(model, obs)
    print(f"feasible={fb.feasible}  log-evidence={fb.log_evidence:.6f} [exact]")
    for t, row in enumerate(fb.posteriors):
        print(f"  t={t} obs={str(obs[t]):6} P(state|obs)={[round(p, 4) for p in row]}")

    show("Viterbi vs pointwise MAP")
    vit = viterbi(model, obs)
    print(f"viterbi path={vit.path}  logP={vit.log_prob:.6f} [exact]")
    print(f"MAP concat ={pointwise_map(fb.posteriors)}  (need not equal viterbi)")

    show("top-3 distinct paths; ranking and mass accumulation are separate")
    top = topk_paths(model, obs, 3)
    for s in top:
        print(f"  path={s.path}  logP={s.log_prob:.6f}  P={s.prob:.6e} [exact]")
    mass = accumulate_log_probability([s.log_prob for s in top])
    print(f"  accumulated mass of top-3: {math.exp(mass):.6f} [exact]")

    show("long sequence scaling (T=20000, no underflow)")
    long_obs = ["walk", "shop", "clean"] * 6666 + ["walk", "shop"]
    fb_long = forward_backward(model, long_obs)
    print(f"log-evidence={fb_long.log_evidence:.3f} (finite, < 0) [exact]")
    print(f"viterbi path length={len(viterbi(model, long_obs).path)}")

    show("constrained state path (state 0 forced at t=0, state 1 at t=2)")
    allowed = [{0}, None, {1}, None, None, None]
    fbc = forward_backward(model, obs, allowed)
    print(f"posterior t=0: {[round(p, 4) for p in fbc.posteriors[0]]} [exact]")
    print(f"posterior t=2: {[round(p, 4) for p in fbc.posteriors[2]]} [exact]")

    show("piecewise transition (regime switch at t=3)")
    pw = PiecewiseHMM(
        initial=[0.5, 0.5],
        transitions=[[[0.9, 0.1], [0.2, 0.8]], [[0.3, 0.7], [0.6, 0.4]]],
        boundaries=[3],
        emission=[{"x": 0.8, "y": 0.2}, {"x": 0.3, "y": 0.7}],
    )
    pobs = ["x", "y", "x", "x", "y"]
    pfb = forward_backward(pw, pobs)
    print(f"log-evidence={pfb.log_evidence:.6f} [exact]")
    print(f"viterbi={viterbi(pw, pobs).path}")

    show("incremental revision with versioned queries")
    store = VersionedObservations(model, obs)
    print(f"v0 log-evidence={store.log_evidence(0):.6f} [exact]")
    v1 = store.revise(2, "walk")
    print(f"v{v1} (obs[2]: clean->walk) log-evidence={store.log_evidence(v1):.6f} [exact]")
    print(f"v0 still queryable: log-evidence={store.log_evidence(0):.6f}")
    v2 = store.revise(1, "teleport")  # impossible observation
    print(f"v{v2} impossible obs -> posteriors={store.posteriors(v2)} "
          f"log-evidence={store.log_evidence(v2)}")

    show("zero-probability sequence: explicit no-solution, no pseudo-posterior")
    hard = HMM([1.0, 0.0], [[0.5, 0.5], [0.5, 0.5]], [{"a": 1.0}, {"b": 1.0}])
    bad = forward_backward(hard, ["b"])
    print(f"feasible={bad.feasible} posteriors={bad.posteriors} "
          f"log-evidence={bad.log_evidence}")
    print(f"viterbi on impossible obs: {viterbi(model, ['teleport'])}")

    show("empty sequence (model convention)")
    empty = forward_backward(model, [])
    print(f"evidence=exp({empty.log_evidence})=1.0 posteriors={empty.posteriors} "
          f"viterbi={viterbi(model, []).path}")

    show("MAP concat != Viterbi (constructed example)")
    tricky = HMM([0.6, 0.4], [[0.0, 1.0], [0.6, 0.4]], [{"a": 1.0}, {"a": 1.0}])
    tobs = ["a", "a", "a"]
    tfb = forward_backward(tricky, tobs)
    print(f"MAP concat={pointwise_map(tfb.posteriors)} "
          f"viterbi={viterbi(tricky, tobs).path} [exact]")

    show("sampling with caller-injected RNG; empirical vs exact")
    seed, n_samples = 20261002, 20000
    rng = random.Random(seed)
    states, sample_obs = sample_path(model, 5, rng)
    print(f"one trajectory: states={states} obs={sample_obs}")
    est = estimate_state_frequencies(model, length=3, n_samples=n_samples, seed=seed)
    print(f"[empirical] kind={est.kind} seed={est.seed} n={est.n_samples}")
    print(f"[empirical] P(s_0)={ [round(p, 4) for p in est.state_frequencies[0]] }")
    exact_marginal = forward_backward(model, [None, None, None]).posteriors[0]
    print(f"[exact]     P(s_0)={[round(p, 4) for p in exact_marginal]}")
    print(f"[exact]     evidence of sample obs: "
          f"{exact_evidence(model, sample_obs):.6e}")


if __name__ == "__main__":
    main()
