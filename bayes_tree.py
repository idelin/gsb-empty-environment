"""Log-domain Bayesian updating over a finite hypothesis set.

Features
--------
- Sequential observation updates with normalization carried out entirely in
  the log domain (logsumexp).
- A likelihood *contribution tree* (segment tree) stores one log-likelihood
  vector per observation. Late arrivals, corrections and retractions are
  O(log n) and never divide a likelihood out of a running product, so an
  observation with zero likelihoods can be retracted exactly.
- Zero evidence probability is reported as a conflict; it is never silently
  reset to a uniform posterior.
- Evidence (log P(observations)) is recorded after every step.
- Versioned prior updates are validated first, so they apply atomically and
  always leave normalized evidence available.
- Observation shards can be merged without double-counting observation ids.
- ExactRationalModel gives ground truth with fractions.Fraction on small
  models.
- Randomness for empirical estimates is injected by the caller; the seed and
  sample size are recorded, and exact/empirical results are labelled.
"""
from __future__ import annotations

import math
from bisect import bisect_left
from dataclasses import dataclass
from fractions import Fraction
from itertools import accumulate

NEG_INF = float("-inf")

__all__ = [
    "ConflictError",
    "DuplicateObservationError",
    "UnknownObservationError",
    "PriorVersionError",
    "EvidenceRecord",
    "ExactResult",
    "EmpiricalResult",
    "BayesianUpdater",
    "ObservationShard",
    "merge_shards",
    "ExactRationalModel",
    "empirical_posterior",
]


class ConflictError(Exception):
    """Raised when the evidence probability is zero.

    This happens when the observations (and/or prior) rule out every
    hypothesis simultaneously. The contradictory state is retained so the
    offending observation can be retracted, after which the contribution
    tree reconstructs the exact non-conflicting likelihood product.
    """


class DuplicateObservationError(KeyError):
    """Raised when an observation id is added twice; state is unchanged."""


class UnknownObservationError(KeyError):
    """Raised when correcting/retracting an unknown observation id."""


class PriorVersionError(ValueError):
    """Raised when a prior update does not use a strictly newer version."""


def _logsumexp(values):
    """Numerically stable log(sum(exp(values))); -inf only if all are -inf."""
    peak = max(values)
    if peak == NEG_INF:
        return NEG_INF
    return peak + math.log(sum(math.exp(value - peak) for value in values))


def _to_log_vector(hypotheses, likelihoods):
    """Validate a likelihood mapping/sequence and return log-likelihoods."""
    if isinstance(likelihoods, dict):
        missing = [h for h in hypotheses if h not in likelihoods]
        extra = [h for h in likelihoods if h not in hypotheses]
        if missing or extra:
            raise ValueError(
                f"likelihood keys mismatch (missing={missing}, extra={extra})"
            )
        values = [likelihoods[h] for h in hypotheses]
    else:
        values = list(likelihoods)
        if len(values) != len(hypotheses):
            raise ValueError(
                f"expected {len(hypotheses)} likelihoods, got {len(values)}"
            )
    log_values = []
    for value in values:
        if value < 0:
            raise ValueError("likelihoods must be non-negative")
        log_values.append(NEG_INF if value == 0 else math.log(value))
    return tuple(log_values)


class _ContributionTree:
    """Segment tree of per-observation log-likelihood vectors.

    Each leaf holds one observation vector (ordered by arrival time); an
    internal node holds the element-wise sum of its children, i.e. the log
    domain product of the observations beneath it. Leaves are reset to the
    identity vector of zeros on retraction, so no division by a likelihood
    is ever needed -- zero likelihoods retract exactly.
    """

    def __init__(self, n_hypotheses):
        self._k = n_hypotheses
        self._capacity = 1
        self._tree = [[0.0] * n_hypotheses for _ in range(2)]
        self._slots = {}        # observation id -> leaf slot
        self._free_slots = []   # slots freed by retraction
        self._next_slot = 0

    def __len__(self):
        return len(self._slots)

    def slot_of(self, obs_id):
        return self._slots[obs_id]

    def _identity(self):
        return [0.0] * self._k

    def _pull(self, index):
        left = self._tree[2 * index]
        right = self._tree[2 * index + 1]
        self._tree[index] = [a + b for a, b in zip(left, right)]

    def _grow(self):
        old_capacity = self._capacity
        self._capacity *= 2
        new_tree = [self._identity() for _ in range(2 * self._capacity)]
        for slot in range(old_capacity):
            new_tree[self._capacity + slot] = self._tree[old_capacity + slot]
        self._tree = new_tree
        for index in range(self._capacity - 1, 0, -1):
            self._pull(index)

    def _set_leaf(self, slot, vector):
        index = self._capacity + slot
        self._tree[index] = list(vector)
        index //= 2
        while index:
            self._pull(index)
            index //= 2

    def add(self, obs_id, vector):
        if self._free_slots:
            slot = self._free_slots.pop()
        else:
            slot = self._next_slot
            self._next_slot += 1
            while slot >= self._capacity:
                self._grow()
        self._slots[obs_id] = slot
        self._set_leaf(slot, vector)

    def update(self, obs_id, vector):
        self._set_leaf(self._slots[obs_id], vector)

    def remove(self, obs_id):
        slot = self._slots.pop(obs_id)
        self._set_leaf(slot, self._identity())
        self._free_slots.append(slot)

    def total(self):
        return tuple(self._tree[1])


@dataclass(frozen=True)
class EvidenceRecord:
    """Evidence value saved after a single update step."""
    step: int
    operation: str
    log_evidence: float
    prior_version: int

    @property
    def conflict(self):
        return self.log_evidence == NEG_INF


@dataclass(frozen=True)
class ExactResult:
    """A conclusion computed exactly (up to floating-point evaluation)."""
    label: str
    posterior: dict
    log_evidence: float
    step: int
    prior_version: int


@dataclass(frozen=True)
class EmpiricalResult:
    """A Monte-Carlo estimate, with provenance of its randomness."""
    label: str
    seed: int
    samples: int
    estimate: dict


class BayesianUpdater:
    """Sequential log-domain Bayesian updates over a fixed hypothesis set.

    Parameters
    ----------
    hypotheses: ordered collection of distinct hypothesis labels.
    prior: mapping hypothesis -> non-negative weight; it is normalized in
        the log domain, so it need not sum to one.
    prior_version: integer version of the hypothesis-set/prior configuration.
        The hypothesis set is fixed for the lifetime of the updater; a
        different hypothesis set requires a new updater (likelihood vectors
        are positional to the hypothesis order).
    """

    def __init__(self, hypotheses, prior, *, prior_version=1,
                 _record_init=True):
        hypotheses = tuple(hypotheses)
        if not hypotheses:
            raise ValueError("hypothesis set must be non-empty")
        if len(set(hypotheses)) != len(hypotheses):
            raise ValueError("hypotheses must be distinct")
        self._hypotheses = hypotheses
        self._log_prior = self._normalize_prior(prior)
        self._prior_version = prior_version
        self._tree = _ContributionTree(len(hypotheses))
        self._observations = {}  # obs id -> log-likelihood vector
        self._history = []
        self._step = 0
        if _record_init:
            self._record("init")

    @property
    def hypotheses(self):
        return self._hypotheses

    @property
    def prior_version(self):
        return self._prior_version

    @property
    def observation_count(self):
        return len(self._observations)

    def observation_ids(self):
        return frozenset(self._observations)

    def history(self):
        return tuple(self._history)

    def _normalize_prior(self, prior):
        if not isinstance(prior, dict):
            prior = dict(zip(self._hypotheses, prior))
        missing = [h for h in self._hypotheses if h not in prior]
        extra = [h for h in prior if h not in self._hypotheses]
        if missing or extra:
            raise ValueError(f"prior keys mismatch (missing={missing}, extra={extra})")
        weights = [prior[h] for h in self._hypotheses]
        if any(w < 0 for w in weights):
            raise ValueError("prior must be non-negative")
        if sum(weights) <= 0:
            raise ValueError("prior must have positive total mass")
        logs = [NEG_INF if w == 0 else math.log(w) for w in weights]
        normalizer = _logsumexp(logs)
        return tuple(lg - normalizer for lg in logs)

    def _add_log_vector(self, obs_id, vector):
        self._observations[obs_id] = vector
        self._tree.add(obs_id, vector)

    def add_observation(self, obs_id, likelihoods):
        """Register an observation; a repeated id never updates twice."""
        if obs_id in self._observations:
            raise DuplicateObservationError(
                f"observation id {obs_id!r} already exists"
            )
        vector = _to_log_vector(self._hypotheses, likelihoods)
        self._add_log_vector(obs_id, vector)
        return self._record(f"add:{obs_id}")

    def correct_observation(self, obs_id, likelihoods):
        """Replace an observation's likelihood contribution in place."""
        if obs_id not in self._observations:
            raise UnknownObservationError(
                f"observation id {obs_id!r} is not registered"
            )
        vector = _to_log_vector(self._hypotheses, likelihoods)
        self._observations[obs_id] = vector
        self._tree.update(obs_id, vector)
        return self._record(f"correct:{obs_id}")

    def retract_observation(self, obs_id):
        """Withdraw an observation; the tree recomputes affected nodes."""
        if obs_id not in self._observations:
            raise UnknownObservationError(
                f"observation id {obs_id!r} is not registered"
            )
        del self._observations[obs_id]
        self._tree.remove(obs_id)
        return self._record(f"retract:{obs_id}")

    def update_prior(self, prior, *, version):
        """Atomically move to a newer, strictly versioned prior."""
        if version <= self._prior_version:
            raise PriorVersionError(
                f"prior version {version} is not newer than "
                f"{self._prior_version}"
            )
        normalized = self._normalize_prior(prior)  # validates before mutation
        self._log_prior = normalized
        self._prior_version = version
        return self._record(f"prior:v{version}")

    def apply_batch(self, operations):
        """Apply a batch of operations; identical to one-at-a-time calls.

        Each operation is a tuple:
        ("add", obs_id, likelihoods), ("correct", obs_id, likelihoods),
        ("retract", obs_id) or ("prior", prior_mapping, version).
        """
        records = []
        for operation in operations:
            kind = operation[0]
            if kind == "add":
                record = self.add_observation(operation[1], operation[2])
            elif kind == "correct":
                record = self.correct_observation(operation[1], operation[2])
            elif kind == "retract":
                record = self.retract_observation(operation[1])
            elif kind == "prior":
                record = self.update_prior(operation[1], version=operation[2])
            else:
                raise ValueError(f"unknown batch operation kind: {kind!r}")
            records.append(record)
        return tuple(records)

    def _current_log_evidence(self):
        total = self._tree.total()
        joined = [lp + lt for lp, lt in zip(self._log_prior, total)]
        return _logsumexp(joined)

    def _record(self, operation):
        self._step += 1
        log_evidence = self._current_log_evidence()
        record = EvidenceRecord(
            step=self._step,
            operation=operation,
            log_evidence=log_evidence,
            prior_version=self._prior_version,
        )
        self._history.append(record)
        return record

    def log_evidence(self):
        """Normalized log evidence log P(observations); raise on conflict."""
        log_evidence = self._current_log_evidence()
        if log_evidence == NEG_INF:
            raise ConflictError(
                "zero evidence probability: observations rule out every "
                "hypothesis; posterior is undefined (not reset to uniform)"
            )
        return log_evidence

    def evidence(self):
        """Probability of the observations under the normalized prior."""
        return math.exp(self.log_evidence())

    def is_conflict(self):
        return self._current_log_evidence() == NEG_INF

    def posterior(self):
        """Normalized posterior mapping; raises ConflictError at zero evidence."""
        total = self._tree.total()
        log_evidence = _logsumexp(
            [lp + lt for lp, lt in zip(self._log_prior, total)]
        )
        if log_evidence == NEG_INF:
            raise ConflictError(
                "zero evidence probability: posterior is undefined "
                "(not reset to uniform)"
            )
        return {
            h: math.exp(lp + lt - log_evidence)
            for h, lp, lt in zip(self._hypotheses, self._log_prior, total)
        }

    def exact_posterior(self):
        """Return the exact (analytic, log-domain) posterior, labelled."""
        return ExactResult(
            label="exact",
            posterior=self.posterior(),
            log_evidence=self.log_evidence(),
            step=self._step,
            prior_version=self._prior_version,
        )


class ObservationShard:
    """A partition of observations with its own contribution tree.

    Shards can be filled independently (e.g. from different data sources or
    late-arriving files) and later combined via merge_shards.
    """

    def __init__(self, hypotheses, name=None):
        self._hypotheses = tuple(hypotheses)
        if not self._hypotheses:
            raise ValueError("hypothesis set must be non-empty")
        self.name = name
        self._tree = _ContributionTree(len(hypotheses))
        self._observations = {}

    @property
    def hypotheses(self):
        return self._hypotheses

    @property
    def observations(self):
        return dict(self._observations)

    def __len__(self):
        return len(self._observations)

    def add_observation(self, obs_id, likelihoods):
        if obs_id in self._observations:
            raise DuplicateObservationError(
                f"observation id {obs_id!r} already exists in shard"
            )
        vector = _to_log_vector(self._hypotheses, likelihoods)
        self._observations[obs_id] = vector
        self._tree.add(obs_id, vector)

    def log_total(self):
        return self._tree.total()


def merge_shards(hypotheses, prior, shards, *, prior_version=1):
    """Merge observation shards into one updater without double counting.

    An observation id appearing in several shards is included exactly once.
    If two shards carry conflicting likelihoods for the same id a ConflictError
    is raised, because silently picking either copy would hide a data error.
    """
    merged = BayesianUpdater(
        hypotheses, prior, prior_version=prior_version, _record_init=False
    )
    seen = {}
    for shard in shards:
        if tuple(shard.hypotheses) != tuple(hypotheses):
            raise ValueError("all shards must share the updater's hypothesis set")
        for obs_id, vector in shard.observations.items():
            if obs_id in seen:
                if seen[obs_id] != vector:
                    raise ConflictError(
                        f"conflicting replicates of observation {obs_id!r} "
                        "across shards"
                    )
                continue  # same id with same likelihood: count it once only
            seen[obs_id] = vector
            merged._add_log_vector(obs_id, vector)
    merged._record("merge")
    return merged


class ExactRationalModel:
    """Ground-truth reference for small models using fractions.Fraction.

    Operations mirror BayesianUpdater (add/retract, evidence, posterior) but
    every probability is an exact rational, so the float/log-domain model can
    be validated against exact fractions on small examples.
    """

    def __init__(self, hypotheses, prior):
        self._hypotheses = tuple(hypotheses)
        if len(set(self._hypotheses)) != len(self._hypotheses):
            raise ValueError("hypotheses must be distinct")
        self._prior = {h: Fraction(prior[h]) for h in self._hypotheses}
        if any(weight < 0 for weight in self._prior.values()):
            raise ValueError("prior must be non-negative")
        if sum(self._prior.values()) <= 0:
            raise ValueError("prior must have positive total mass")
        normalizer = sum(self._prior.values())
        self._prior = {h: w / normalizer for h, w in self._prior.items()}
        self._observations = {}

    @property
    def hypotheses(self):
        return self._hypotheses

    def add_observation(self, obs_id, likelihoods):
        if obs_id in self._observations:
            raise DuplicateObservationError(
                f"observation id {obs_id!r} already exists"
            )
        vector = {h: Fraction(likelihoods[h]) for h in self._hypotheses}
        if any(value < 0 for value in vector.values()):
            raise ValueError("likelihoods must be non-negative")
        self._observations[obs_id] = vector

    def retract_observation(self, obs_id):
        if obs_id not in self._observations:
            raise UnknownObservationError(
                f"observation id {obs_id!r} is not registered"
            )
        del self._observations[obs_id]

    def _weights(self):
        weights = {}
        for hypothesis in self._hypotheses:
            weight = self._prior[hypothesis]
            for vector in self._observations.values():
                weight *= vector[hypothesis]
            weights[hypothesis] = weight
        return weights

    def evidence(self):
        """Exact rational evidence P(observations); raise ConflictError on 0."""
        total = sum(self._weights().values())
        if total <= 0:
            raise ConflictError(
                "zero evidence probability (exact rational model)"
            )
        return total

    def posterior(self):
        weights = self._weights()
        total = sum(weights.values())
        if total <= 0:
            raise ConflictError(
                "zero evidence probability (exact rational model)"
            )
        return {h: w / total for h, w in weights.items()}


def empirical_posterior(hypotheses, prior, observations, *, rng, seed, samples):
    """Monte-Carlo estimate of the posterior using caller-injected randomness.

    Parameters
    ----------
    hypotheses, prior, observations: small-model description. observations
        maps obs id -> {hypothesis: non-negative likelihood}.
    rng: random.Random-compatible source created by the caller, e.g.
        ``random.Random(seed)``; nothing here seeds a global RNG.
    seed: the seed used by the caller, recorded for reproducibility.
    samples: number of posterior draws.

    Returns an EmpiricalResult labelled "empirical" carrying seed and sample
    count. Contrast it with BayesianUpdater.exact_posterior(), which is
    labelled "exact".
    """
    hypotheses = tuple(hypotheses)
    if samples <= 0:
        raise ValueError("samples must be positive")
    weights = []
    for hypothesis in hypotheses:
        weight = float(prior[hypothesis])
        for likelihood in observations.values():
            weight *= float(likelihood[hypothesis])
        weights.append(weight)
    total = sum(weights)
    if total <= 0.0:
        raise ConflictError(
            "zero evidence probability: cannot draw empirical posterior"
        )
    cumulative = list(accumulate(weights))
    counts = [0] * len(hypotheses)
    for _ in range(samples):
        target = rng.random() * total
        index = bisect_left(cumulative, target)
        if index >= len(hypotheses):
            index = len(hypotheses) - 1
        counts[index] += 1
    estimate = {
        hypothesis: count / samples
        for hypothesis, count in zip(hypotheses, counts)
    }
    return EmpiricalResult(
        label="empirical", seed=seed, samples=samples, estimate=estimate
    )
