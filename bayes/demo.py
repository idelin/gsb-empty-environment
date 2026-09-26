"""Runnable sample: python3.11 -m bayes.demo"""

import random

from .exact import exact_posterior
from .simulation import empirical_identification_rate, exact_identification_rate
from .updater import BayesianUpdater, ConflictError


def show(title, updater):
    try:
        post = updater.posterior()
        body = ", ".join(f"{h}={p:.4f}" for h, p in post.items())
    except ConflictError:
        body = "CONFLICT (zero evidence, no uniform reset)"
    print(f"{title:<44} {body}")


def main():
    priors = {"h1": 0.5, "h2": 0.3, "h3": 0.2}
    u = BayesianUpdater(priors, version=1)
    print("== sequential updates (log-domain normalization) ==")
    show("prior", u)
    u.apply("obs-1", {"h1": 0.9, "h2": 0.4, "h3": 0.1})
    show("apply obs-1", u)
    u.apply("obs-2", {"h1": 0.2, "h2": 0.8, "h3": 0.5})
    show("apply obs-2", u)
    u.correct("obs-1", {"h1": 0.6, "h2": 0.5, "h3": 0.2})
    show("correct obs-1 (late fix)", u)
    u.retract("obs-2")
    show("retract obs-2", u)

    print("\n== contradictory evidence -> conflict -> recovery ==")
    c = BayesianUpdater({"h1": 0.5, "h2": 0.5})
    c.apply("e1", {"h1": 1.0, "h2": 0.0})
    show("apply e1 (kills h2)", c)
    c.apply("e2", {"h1": 0.0, "h2": 1.0})
    show("apply e2 (kills h1)", c)
    c.retract("e2")
    show("retract e2 (recovered via tree)", c)

    print("\n== atomic model/prior version update ==")
    log_z = u.update_model({"h1": 0.2, "h2": 0.5, "h3": 0.3}, version=2)
    show(f"update_model v2 (log evidence={log_z:.4f})", u)

    print("\n== shard merge (no double counting) ==")
    left = BayesianUpdater(priors)
    left.apply_batch([
        ("a", {"h1": 0.9, "h2": 0.2, "h3": 0.1}),
        ("b", {"h1": 0.4, "h2": 0.4, "h3": 0.9}),
        ("s", {"h1": 0.5, "h2": 0.5, "h3": 0.5}),
    ])
    right = BayesianUpdater(priors)
    right.apply_batch([
        ("s", {"h1": 0.5, "h2": 0.5, "h3": 0.5}),  # shared observation
        ("c", {"h1": 0.2, "h2": 0.7, "h3": 0.7}),
    ])
    merged = left.merge(right)
    show(f"merged ({len(merged.observation_ids)} unique obs)", merged)
    exact = exact_posterior(priors, [
        {"h1": 0.9, "h2": 0.2, "h3": 0.1},
        {"h1": 0.4, "h2": 0.4, "h3": 0.9},
        {"h1": 0.5, "h2": 0.5, "h3": 0.5},
        {"h1": 0.2, "h2": 0.7, "h3": 0.7},
    ])
    print("exact rational check:",
          {h: str(p) for h, p in exact.items()})

    print("\n== evidence history (one record per step) ==")
    for rec in u.history:
        print(f"  seq={rec.seq} {rec.event:<8} {rec.detail:<14} "
              f"logZ={rec.log_evidence:+.6f} conflict={rec.conflict}")

    print("\n== exact vs empirical (caller-injected RNG) ==")
    channels = {
        "fair": {"H": 0.5, "T": 0.5},
        "loaded": {"H": 0.7, "T": 0.3},
    }
    coin_priors = {"fair": 0.5, "loaded": 0.5}
    exact_est = exact_identification_rate(
        priors=coin_priors, channels=channels, true_hyp="loaded", n_obs=8
    )
    rng = random.Random(20260927)
    emp_est = empirical_identification_rate(
        rng, priors=coin_priors, channels=channels, true_hyp="loaded",
        n_obs=8, n_trials=20000, seed=20260927,
    )
    print(f"[exact]     P(MAP=loaded | loaded) = {float(exact_est.value):.4f} "
          f"({exact_est.value})")
    print(f"[empirical] P(MAP=loaded | loaded) = {emp_est.value:.4f} "
          f"(seed={emp_est.seed}, samples={emp_est.samples})")


if __name__ == "__main__":
    main()
