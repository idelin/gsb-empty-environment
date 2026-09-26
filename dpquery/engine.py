"""Private query engine: budget accounting, canonical cache, in-flight dedup.

Rules enforced here:

* An identical specification on the same dataset version reuses the published
  noisy result and spends no additional epsilon.
* Any change of query semantics (including a filter threshold), data version,
  or mechanism parameter is a cache miss and is charged fresh.
* Concurrent identical queries execute the mechanism exactly once: followers
  receive the same published value, and the budget is charged exactly once.
"""

from __future__ import annotations

import json
import threading
from dataclasses import dataclass, replace
from typing import Any

from .accountant import BudgetAccountant
from .mechanisms import LaplaceMechanism
from .models import Query, SyntheticDataset


@dataclass(frozen=True)
class Release:
    """A published mechanism output."""

    value: float
    epsilon: float
    sensitivity: float
    mechanism: str
    dataset_name: str
    dataset_version: str
    cache_key: str
    reused: bool
    spent_total: float


class _InFlight:
    def __init__(self) -> None:
        self.condition = threading.Condition()
        self.done = False
        self.error: BaseException | None = None


class PrivateQueryEngine:
    def __init__(
        self,
        dataset: SyntheticDataset,
        total_epsilon: float,
        seed: int | None = None,
    ) -> None:
        if not isinstance(dataset, SyntheticDataset):
            raise TypeError("only SyntheticDataset is accepted; real data is not allowed")
        self.dataset = dataset
        self.accountant = BudgetAccountant(total_epsilon=total_epsilon)
        self.mechanism = LaplaceMechanism(seed=seed)
        self._cache: dict[str, Release] = {}
        self._inflight: dict[str, _InFlight] = {}
        self._lock = threading.Lock()

    def cache_key(self, query: Query, epsilon: float, mechanism: str = "laplace") -> str:
        """Deterministic identity of (query semantics, data version, mechanism)."""
        key_obj = {
            "mechanism": mechanism,
            "epsilon": float(epsilon),
            "dataset": {"name": self.dataset.name, "version": self.dataset.version},
            "spec": json.loads(query.canonical_spec()),
        }
        return json.dumps(key_obj, sort_keys=True, separators=(",", ":"), ensure_ascii=False)

    def cache_size(self) -> int:
        with self._lock:
            return len(self._cache)

    def release(
        self,
        query: Query,
        epsilon: float,
        mechanism: str = "laplace",
    ) -> Release:
        if mechanism != "laplace":
            raise ValueError(f"unsupported mechanism: {mechanism!r}")
        if epsilon <= 0:
            raise ValueError("epsilon must be positive")

        key = self.cache_key(query, epsilon, mechanism)

        with self._lock:
            cached = self._cache.get(key)
            if cached is not None:
                return replace(cached, reused=True, spent_total=self.accountant.spent_epsilon)

            flight = self._inflight.get(key)
            if flight is None:
                flight = _InFlight()
                self._inflight[key] = flight
                leader = True
                self.accountant.reserve(epsilon)  # may raise BudgetExhaustedError
            else:
                leader = False
            cond = flight.condition

        if not leader:
            with cond:
                while not flight.done:
                    cond.wait()
                if flight.error is not None:
                    raise flight.error
            with self._lock:
                cached = self._cache[key]
            return replace(cached, reused=True, spent_total=self.accountant.spent_epsilon)

        # Leader: compute and publish exactly one mechanism draw.
        try:
            true_value, sensitivity = self._evaluate(query)
            value = self.mechanism.release(true_value, sensitivity, epsilon)
            result = Release(
                value=value,
                epsilon=epsilon,
                sensitivity=sensitivity,
                mechanism=mechanism,
                dataset_name=self.dataset.name,
                dataset_version=self.dataset.version,
                cache_key=key,
                reused=False,
                spent_total=self.accountant.spent_epsilon,
            )
            with self._lock:
                self._cache[key] = result
                self._inflight.pop(key, None)
            with cond:
                flight.done = True
                cond.notify_all()
        except BaseException as exc:
            with self._lock:
                self.accountant.refund(epsilon)
                self._inflight.pop(key, None)
            with cond:
                flight.error = exc
                flight.done = True
                cond.notify_all()
            raise

        return result

    def _evaluate(self, query: Query) -> tuple[float, float]:
        rows = self.dataset.records
        selected: list[Any] = []
        for row in rows:
            if all(pred.matches(row) for pred in query.predicates):
                selected.append(row)

        if query.kind == "count":
            return float(len(selected)), 1.0

        lo, hi = query.bounds  # type: ignore[misc]
        total = 0.0
        for row in selected:
            x = row[query.column]
            total += min(hi, max(lo, float(x)))
        sensitivity = max(abs(lo), abs(hi))
        return total, float(sensitivity)
