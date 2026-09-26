"""Sequential Bayesian updating over a finite hypothesis set.

Normalization is done in the log domain. Observations may arrive late, be
corrected, or be retracted; their log-likelihood contributions live in a
segment tree so retraction/correction never divides by a (possibly zero)
likelihood. When the total evidence is zero the updater enters a conflict
state instead of resetting to a uniform posterior.
"""

from __future__ import annotations

import math
from dataclasses import dataclass
from typing import Iterable, Mapping

NEG_INF = float("-inf")


class ConflictError(Exception):
    """Raised when total evidence is zero and no normalized posterior exists."""


@dataclass(frozen=True)
class StepRecord:
    """One entry of the evidence history."""

    seq: int
    event: str
    detail: str
    log_evidence: float
    conflict: bool


class _SumTree:
    """Segment tree holding elementwise sums of fixed-dimension vectors."""

    def __init__(self, dim: int, capacity: int = 4):
        self.dim = dim
        self.size = 1
        while self.size < max(1, capacity):
            self.size <<= 1
        self.nodes = [[0.0] * dim for _ in range(2 * self.size)]

    def _pull(self, i: int) -> None:
        left = self.nodes[2 * i]
        right = self.nodes[2 * i + 1]
        self.nodes[i] = [a + b for a, b in zip(left, right)]

    def _grow(self, min_capacity: int) -> None:
        if min_capacity <= self.size:
            return
        leaves = self.nodes[self.size:]
        while self.size < min_capacity:
            self.size <<= 1
        self.nodes = [[0.0] * self.dim for _ in range(2 * self.size)]
        self.nodes[self.size:self.size + len(leaves)] = leaves
        for i in range(self.size - 1, 0, -1):
            self._pull(i)

    def set(self, slot: int, vec) -> None:
        if slot >= self.size:
            self._grow(slot + 1)
        i = self.size + slot
        self.nodes[i] = list(vec)
        i //= 2
        while i:
            self._pull(i)
            i //= 2

    def total(self):
        return list(self.nodes[1])


class _Model:
    """Hypothesis set plus prior, tagged with a version number."""

    def __init__(self, priors: Mapping[str, float], version: int):
        if not priors:
            raise ValueError("hypothesis set must be non-empty")
        for hyp, p in priors.items():
            if p <= 0:
                raise ValueError(f"prior for {hyp!r} must be positive")
        self.priors = dict(priors)
        self.hyp_ids = tuple(priors)
        self.index = {h: i for i, h in enumerate(self.hyp_ids)}
        log_total = math.log(sum(priors.values()))
        self.log_prior = [math.log(p) - log_total for p in priors.values()]
        self.version = version

    def log_vector(self, likelihoods: Mapping[str, float]):
        vec = []
        for hyp in self.hyp_ids:
            lik = likelihoods.get(hyp, 1.0)
            if lik < 0:
                raise ValueError(f"negative likelihood for {hyp!r}")
            vec.append(math.log(lik) if lik > 0 else NEG_INF)
        return vec


class BayesianUpdater:
    """Maintains posterior over hypotheses as observations come and go.

    Likelihood dicts may be partial: a hypothesis missing from an
    observation's likelihood mapping is treated as neutral (likelihood 1).
    """

    def __init__(self, priors: Mapping[str, float], version: int = 1):
        self._model = _Model(priors, version)
        self._obs: dict = {}
        self._slots: dict = {}
        self._free: list = []
        self._next_slot = 0
        self._tree = _SumTree(len(self._model.hyp_ids))
        self._log_post = list(self._model.log_prior)
        self._log_evidence = 0.0
        self._history = [StepRecord(0, "init", f"model v{version}", 0.0, False)]

    @property
    def hypotheses(self):
        return self._model.hyp_ids

    @property
    def version(self) -> int:
        return self._model.version

    @property
    def history(self):
        return list(self._history)

    @property
    def observation_ids(self):
        return tuple(self._obs)

    @property
    def in_conflict(self) -> bool:
        return self._log_evidence == NEG_INF

    @property
    def log_evidence(self) -> float:
        if self.in_conflict:
            raise ConflictError("total evidence is zero")
        return self._log_evidence

    def evidence(self) -> float:
        return 0.0 if self.in_conflict else math.exp(self._log_evidence)

    def posterior(self) -> dict:
        if self.in_conflict:
            raise ConflictError(
                "total evidence is zero; posterior is undefined "
                "(refusing to reset to uniform)"
            )
        return {h: math.exp(lp) for h, lp in zip(self._model.hyp_ids, self._log_post)}

    def log_posterior(self) -> dict:
        if self.in_conflict:
            raise ConflictError("total evidence is zero; posterior is undefined")
        return dict(zip(self._model.hyp_ids, self._log_post))

    def has(self, obs_id) -> bool:
        return obs_id in self._obs

    def _alloc_slot(self) -> int:
        if self._free:
            return self._free.pop()
        slot = self._next_slot
        self._next_slot += 1
        return slot

    def _refresh(self, event: str, detail: str) -> StepRecord:
        total = self._tree.total()
        log_w = [lp + lt for lp, lt in zip(self._model.log_prior, total)]
        peak = max(log_w)
        if peak == NEG_INF:
            self._log_post = None
            self._log_evidence = NEG_INF
            log_z, conflict = NEG_INF, True
        else:
            log_z = peak + math.log(sum(math.exp(w - peak) for w in log_w))
            self._log_post = [w - log_z for w in log_w]
            self._log_evidence = log_z
            conflict = False
        rec = StepRecord(len(self._history), event, detail, log_z, conflict)
        self._history.append(rec)
        return rec

    def apply(self, obs_id, likelihoods: Mapping[str, float]):
        """Add an observation. Duplicate ids are ignored (never applied twice)."""
        if obs_id in self._obs:
            return None
        vec = self._model.log_vector(likelihoods)
        slot = self._alloc_slot()
        self._tree.set(slot, vec)
        self._obs[obs_id] = dict(likelihoods)
        self._slots[obs_id] = slot
        return self._refresh("apply", str(obs_id))

    def apply_batch(self, items: Iterable) -> StepRecord:
        """Insert several observations with a single renormalization.

        The resulting posterior is identical to applying them one by one.
        """
        applied = 0
        for obs_id, likelihoods in items:
            if obs_id in self._obs:
                continue
            vec = self._model.log_vector(likelihoods)
            slot = self._alloc_slot()
            self._tree.set(slot, vec)
            self._obs[obs_id] = dict(likelihoods)
            self._slots[obs_id] = slot
            applied += 1
        return self._refresh("batch", f"{applied} observations")

    def correct(self, obs_id, likelihoods: Mapping[str, float]) -> StepRecord:
        """Replace the likelihoods of an existing observation."""
        if obs_id not in self._obs:
            raise KeyError(obs_id)
        vec = self._model.log_vector(likelihoods)
        self._tree.set(self._slots[obs_id], vec)
        self._obs[obs_id] = dict(likelihoods)
        return self._refresh("correct", str(obs_id))

    def retract(self, obs_id) -> StepRecord:
        """Remove an observation's contribution (works even after zeros)."""
        if obs_id not in self._obs:
            raise KeyError(obs_id)
        slot = self._slots.pop(obs_id)
        self._tree.set(slot, [0.0] * len(self._model.hyp_ids))
        self._free.append(slot)
        del self._obs[obs_id]
        return self._refresh("retract", str(obs_id))

    def update_model(self, priors: Mapping[str, float], version: int) -> float:
        """Atomically switch hypothesis set/prior and recompute all nodes.

        Returns the new normalized log evidence. On conflict nothing is
        committed and the previous model, tree, and posterior stay intact.
        """
        if version <= self._model.version:
            raise ValueError("model version must strictly increase")
        new_model = _Model(priors, version)
        tree = _SumTree(len(new_model.hyp_ids), capacity=max(1, self._next_slot))
        for obs_id, lik in self._obs.items():
            tree.set(self._slots[obs_id], new_model.log_vector(lik))
        total = tree.total()
        log_w = [lp + lt for lp, lt in zip(new_model.log_prior, total)]
        peak = max(log_w)
        if peak == NEG_INF:
            raise ConflictError("model update would zero all evidence; state unchanged")
        log_z = peak + math.log(sum(math.exp(w - peak) for w in log_w))
        self._model = new_model
        self._tree = tree
        self._log_post = [w - log_z for w in log_w]
        self._log_evidence = log_z
        rec = StepRecord(len(self._history), "model", f"v{version}", log_z, False)
        self._history.append(rec)
        return log_z

    def merge(self, other: "BayesianUpdater") -> "BayesianUpdater":
        """Combine two shards over the same model into a new updater.

        Observations shared by both shards are counted once; shared ids with
        disagreeing likelihoods raise ConflictError.
        """
        if self._model.hyp_ids != other._model.hyp_ids:
            raise ValueError("shards use different hypothesis sets")
        if self._model.log_prior != other._model.log_prior:
            raise ValueError("shards use different priors")
        merged = BayesianUpdater(
            self._model.priors, version=max(self.version, other.version)
        )
        for source in (self, other):
            for obs_id, lik in source._obs.items():
                if obs_id in merged._obs:
                    if merged._obs[obs_id] != lik:
                        raise ConflictError(
                            f"observation {obs_id!r} disagrees across shards"
                        )
                    continue
                merged.apply(obs_id, lik)
        return merged
