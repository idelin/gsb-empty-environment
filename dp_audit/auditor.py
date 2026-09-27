"""Monte-Carlo audit of event-probability ratios on synthetic adjacent data.

Definitions
-----------
- Dataset: a tuple of synthetic 0/1 records (no real data is ever used).
- Adjacency: add-one (injection). D' = D + (injected_record,).
- Event E: a predicate on the mechanism output.
- A mechanism M satisfies epsilon-DP only if for EVERY adjacent pair
  (D, D') and EVERY event E:
      P(M(D) in E) <= exp(epsilon) * P(M(D') in E)   (and vice versa).

Method
------
Estimate p = P(M(D) in E) and p' = P(M(D') in E) from n_trials independent
runs each (seed recorded for reproducibility), build Clopper-Pearson
confidence intervals, and derive a conservative confidence interval for the
ratio max(p, p') / min(p, p'). If the lower confidence bound of the ratio
exceeds exp(claimed epsilon), the privacy claim is falsified for this event.

IMPORTANT: this is a statistical falsification tool. It can detect
violations; it can never prove formal differential privacy.
"""

from __future__ import annotations

import math
import random
from dataclasses import dataclass
from typing import Callable, Sequence

from .mechanisms import Mechanism
from .stats import clopper_pearson, one_sided_zero_upper

DISCLAIMER = (
    "Empirical audit on synthetic data only. Statistical experiments can "
    "falsify a differential-privacy claim (detect violations) but cannot "
    "prove that a mechanism satisfies formal DP: any finite experiment "
    "covers only one adjacent pair, one output event, and finitely many "
    "random draws, and every conclusion carries a sampling error rate."
)

ZERO_OBSERVATION_NOTE = (
    "0 events observed in {n} trials; the true probability is NOT 0. "
    "One-sided Clopper-Pearson upper bound at confidence {conf:.0%}: "
    "{upper:.6g} (rule-of-three approximation 3/n = {approx:.6g})."
)

RATIO_CI_NOTE = (
    "Conservative interval: intersection of two Clopper-Pearson intervals "
    "at confidence 1-alpha each, so joint coverage is at least 1-2*alpha."
)


@dataclass
class AuditConfig:
    """All parameters of one audit. Everything needed to reproduce the
    experiment is recorded here and echoed into the report."""

    mechanism: Mechanism
    base_dataset: Sequence[int]
    injected_record: int
    event: Callable[[float], bool]
    event_description: str
    n_trials: int
    alpha: float
    claimed_epsilon: float
    seed: int


def _count_hits(
    mechanism: Mechanism,
    dataset: tuple,
    event: Callable[[float], bool],
    n_trials: int,
    rng: random.Random,
) -> int:
    return sum(1 for _ in range(n_trials) if event(mechanism(dataset, rng)))


def _group_stats(k: int, n: int, alpha: float) -> dict:
    lower, upper = clopper_pearson(k, n, alpha)
    stats = {
        "trials": n,
        "event_count": k,
        "p_hat": k / n,
        "ci_lower": lower,
        "ci_upper": upper,
        "ci_confidence": 1.0 - alpha,
    }
    if k == 0:
        stats["zero_observation_note"] = ZERO_OBSERVATION_NOTE.format(
            n=n,
            conf=1.0 - alpha,
            upper=one_sided_zero_upper(n, alpha),
            approx=3.0 / n,
        )
    return stats


def _sanitize(obj):
    """Make the report strictly JSON-safe (no bare Infinity/NaN)."""
    if isinstance(obj, float):
        if math.isinf(obj):
            return "inf" if obj > 0 else "-inf"
        if math.isnan(obj):
            return "nan"
        return obj
    if isinstance(obj, dict):
        return {key: _sanitize(value) for key, value in obj.items()}
    if isinstance(obj, (list, tuple)):
        return [_sanitize(value) for value in obj]
    return obj


def run_audit(config: AuditConfig) -> dict:
    """Run the audit and return a JSON-safe report dict."""
    if config.n_trials <= 0:
        raise ValueError("n_trials must be positive")
    if not 0.0 < config.alpha < 1.0:
        raise ValueError("alpha must be in (0, 1)")
    if config.claimed_epsilon < 0:
        raise ValueError("claimed_epsilon must be non-negative")

    d_base = tuple(config.base_dataset)
    d_inj = d_base + (config.injected_record,)

    # Independent, seed-derived streams so the whole experiment is reproducible.
    rng_base = random.Random(f"{config.seed}:base")
    rng_inj = random.Random(f"{config.seed}:injected")
    k_base = _count_hits(
        config.mechanism, d_base, config.event, config.n_trials, rng_base
    )
    k_inj = _count_hits(
        config.mechanism, d_inj, config.event, config.n_trials, rng_inj
    )

    base_stats = _group_stats(k_base, config.n_trials, config.alpha)
    inj_stats = _group_stats(k_inj, config.n_trials, config.alpha)

    # DP constrains the ratio in both directions; the binding quantity is
    # max(p, p') / min(p, p').
    hi, lo = (inj_stats, base_stats) if k_inj >= k_base else (base_stats, inj_stats)

    if hi["p_hat"] == 0.0:
        ratio_estimate = None  # event never observed for either dataset
    elif lo["p_hat"] == 0.0:
        ratio_estimate = math.inf
    else:
        ratio_estimate = hi["p_hat"] / lo["p_hat"]

    ratio_lower = hi["ci_lower"] / lo["ci_upper"] if lo["ci_upper"] > 0 else math.inf
    ratio_upper = hi["ci_upper"] / lo["ci_lower"] if lo["ci_lower"] > 0 else math.inf

    epsilon_hat = (
        math.log(ratio_estimate)
        if ratio_estimate is not None and ratio_estimate > 0
        else None
    )
    epsilon_lower = math.log(ratio_lower) if ratio_lower > 0 else 0.0

    bound = math.exp(config.claimed_epsilon)
    violation = ratio_lower > bound

    if ratio_estimate is None:
        explanation = (
            f"Inconclusive: event never observed for either dataset in "
            f"{config.n_trials} trials each. This says nothing about privacy; "
            "choose a more probable event or increase n_trials."
        )
    elif violation:
        explanation = (
            f"VIOLATION: lower confidence bound on the event-probability ratio "
            f"({ratio_lower:.6g}) exceeds exp(claimed epsilon) = {bound:.6g}. "
            f"Reproducible event: '{config.event_description}' with "
            f"seed={config.seed}, n_trials={config.n_trials} per dataset, "
            f"alpha={config.alpha}."
        )
    else:
        explanation = (
            "No violation detected at this confidence level. This is NOT a "
            "proof of differential privacy; see disclaimer."
        )

    report = {
        "report_type": "empirical_dp_audit",
        "disclaimer": DISCLAIMER,
        "synthetic_data_only": True,
        "mechanism": {
            "name": config.mechanism.name,
            "params": dict(config.mechanism.params),
        },
        "adjacency": "add-one (injection)",
        "datasets": {
            "base": list(d_base),
            "injected_record": config.injected_record,
            "description": (
                f"base dataset of {len(d_base)} synthetic records; "
                f"D' = D + ({config.injected_record},)"
            ),
        },
        "event": {"description": config.event_description},
        "parameters": {
            "n_trials_per_dataset": config.n_trials,
            "alpha": config.alpha,
            "ci_confidence": 1.0 - config.alpha,
            "claimed_epsilon": config.claimed_epsilon,
            "seed": config.seed,
        },
        "results": {"base": base_stats, "injected": inj_stats},
        "ratio": {
            "estimate": ratio_estimate,
            "ci_lower": ratio_lower,
            "ci_upper": ratio_upper,
            "ci_note": RATIO_CI_NOTE,
        },
        "epsilon": {
            "claimed": config.claimed_epsilon,
            "empirical_estimate": epsilon_hat,
            "lower_confidence_bound": epsilon_lower,
        },
        "verdict": {
            "violation_detected": violation,
            "explanation": explanation,
        },
    }
    return _sanitize(report)


def format_summary(report: dict) -> str:
    """Human-readable one-screen summary of an audit report."""
    params = report["parameters"]
    base = report["results"]["base"]
    inj = report["results"]["injected"]
    ratio = report["ratio"]
    eps = report["epsilon"]

    def fmt(x):
        return f"{x:.6g}" if isinstance(x, float) else str(x)

    lines = [
        "=== Empirical DP audit (synthetic data only; NOT a formal privacy proof) ===",
        f"Mechanism          : {report['mechanism']['name']} {report['mechanism']['params']}",
        f"Adjacency          : {report['adjacency']}; {report['datasets']['description']}",
        f"Event E            : {report['event']['description']}",
        f"Trials per dataset : {params['n_trials_per_dataset']} "
        f"(seed={params['seed']}, alpha={params['alpha']}, "
        f"confidence={params['ci_confidence']:.0%})",
        f"P(M(D)  in E)      : {fmt(base['p_hat'])}  CI [{fmt(base['ci_lower'])}, {fmt(base['ci_upper'])}]  (k={base['event_count']})",
        f"P(M(D') in E)      : {fmt(inj['p_hat'])}  CI [{fmt(inj['ci_lower'])}, {fmt(inj['ci_upper'])}]  (k={inj['event_count']})",
        f"Ratio max/min      : {fmt(ratio['estimate'])}  CI [{fmt(ratio['ci_lower'])}, {fmt(ratio['ci_upper'])}] (conservative)",
        f"Empirical epsilon  : {fmt(eps['empirical_estimate'])} "
        f"(lower bound {fmt(eps['lower_confidence_bound'])}) vs claimed {fmt(eps['claimed'])}",
        f"Verdict            : {'VIOLATION DETECTED' if report['verdict']['violation_detected'] else 'no violation detected (not a proof of privacy)'}",
    ]
    for group in (base, inj):
        note = group.get("zero_observation_note")
        if note:
            lines.append(f"Zero-observation   : {note}")
    lines.append(f"Disclaimer         : {report['disclaimer']}")
    return "\n".join(lines)
