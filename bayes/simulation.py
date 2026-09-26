"""Monte Carlo vs exact enumeration, with caller-injected randomness.

The caller supplies the random source (e.g. random.Random(seed)); every
empirical result records the seed and the number of samples drawn. Exact
results are labelled separately and carry no seed/samples.
"""

from __future__ import annotations

from dataclasses import dataclass
from fractions import Fraction
from itertools import product
from typing import Mapping

from .exact import exact_posterior
from .updater import BayesianUpdater


@dataclass(frozen=True)
class Estimate:
    label: str  # "exact" or "empirical"
    value: object
    seed: object = None
    samples: object = None


def _obs_names(channels: Mapping) -> tuple:
    return tuple(next(iter(channels.values())))


def empirical_identification_rate(
    rng,
    *,
    priors: Mapping,
    channels: Mapping,
    true_hyp: str,
    n_obs: int,
    n_trials: int,
    seed=None,
) -> Estimate:
    """Fraction of trials where the MAP hypothesis equals the true one.

    channels[hyp][obs] is P(obs | hyp). rng is injected by the caller.
    """
    obs_names = _obs_names(channels)
    draw_weights = [channels[true_hyp][o] for o in obs_names]
    hits = 0
    for _ in range(n_trials):
        updater = BayesianUpdater(priors)
        for k in range(n_obs):
            (draw,) = rng.choices(obs_names, weights=draw_weights)
            updater.apply(k, {h: channels[h][draw] for h in priors})
        post = updater.posterior()
        if max(post, key=post.get) == true_hyp:
            hits += 1
    return Estimate("empirical", hits / n_trials, seed=seed, samples=n_trials)


def exact_identification_rate(
    *, priors: Mapping, channels: Mapping, true_hyp: str, n_obs: int
) -> Estimate:
    """Same quantity by exhaustive enumeration, as an exact rational."""
    obs_names = _obs_names(channels)
    total = Fraction(0)
    for seq in product(obs_names, repeat=n_obs):
        obs_list = [{h: channels[h][o] for h in priors} for o in seq]
        post = exact_posterior(priors, obs_list)
        if max(post, key=post.get) == true_hyp:
            prob = Fraction(1)
            for o in seq:
                prob *= Fraction(str(channels[true_hyp][o]))
            total += prob
    return Estimate("exact", total, seed=None, samples=None)
