"""Differential-privacy query engine over synthetic data.

Rules enforced here:

* A published noisy answer is keyed by ``(data_version, canonical query)``.
  Repeating the exact same query on the exact same data version returns the
  stored mechanism output and spends **zero** additional budget.
* Any change in query semantics (predicates, field, bounds, epsilon, threshold,
  kind) or in the data version produces a different key, a fresh mechanism run,
  and a new ledger charge.
* Field order inside predicates / records is irrelevant under canonicalization.
* The free-form ``threshold`` parameter is part of the key: changing it forbids
  reuse even though it does not change the true answer.
* Concurrent identical calls are coalesced: only one mechanism output is ever
  produced and the budget is charged once.
* Only :class:`~dp_synth.synthetic_data.SyntheticData` is accepted.
"""

from __future__ import annotations

import threading
from dataclasses import dataclass
from types import MappingProxyType
from typing import Any, Dict, List, Optional, Tuple

from .budget import BudgetLedger, BudgetExceededError
from .mechanism import Rng, laplace_mechanism, noisy_histogram
from .queries import Query, canonical_query_id, true_answer
from .synthetic_data import SyntheticData


@dataclass(frozen=True)
class Publication:
    query_id: str
    data_version: str
    epsilon: float
    value: Any
    coalesced: int = 0
    reused: bool = False


class _Inflight:
    def __init__(self) -> None:
        self.event = threading.Event()
        self.publication: Optional[Publication] = None
        self.error: Optional[BaseException] = None
        self.coalesced = 0


class DPQueryEngine:
    def __init__(self, total_epsilon_per_version: float = 4.0, rng_seed: int = 0) -> None:
        if total_epsilon_per_version <= 0:
            raise ValueError("total_epsilon_per_version must be positive")
        self.total_epsilon_per_version = float(total_epsilon_per_version)
        self._rng = Rng(rng_seed)
        self._ledgers: Dict[str, BudgetLedger] = {}
        self._cache: Dict[Tuple[str, str], Publication] = {}
        self._inflight: Dict[Tuple[str, str], _Inflight] = {}
        self._lock = threading.Lock()

    def _ledger(self, version: str) -> BudgetLedger:
        ledger = self._ledgers.get(version)
        if ledger is None:
            ledger = BudgetLedger(version, self.total_epsilon_per_version)
            self._ledgers[version] = ledger
        return ledger

    def _get_or_join(self, key: Tuple[str, str]) -> Tuple[Optional[Publication], Optional[_Inflight], bool]:
        """Return (publication, inflight, is_leader). Caller holds no lock."""
        with self._lock:
            cached = self._cache.get(key)
            if cached is not None:
                return cached, None, False
            flight = self._inflight.get(key)
            if flight is not None:
                flight.coalesced += 1
                return None, flight, False
            flight = _Inflight()
            self._inflight[key] = flight
            return None, flight, True

    def run(self, query: Query, data: SyntheticData) -> Publication:
        if not isinstance(data, SyntheticData):
            raise TypeError(
                "only SyntheticData is accepted; this engine processes synthetic data only"
            )
        version = data.version
        query_id = canonical_query_id(query)
        key = (version, query_id)
        ledger = self._ledger(version)

        cached_or_flight = self._get_or_join(key)
        cached, flight, is_leader = cached_or_flight
        if cached is not None:
            ledger.note_reuse(query_id, "exact repeated query, stored output reused")
            return cached
        assert flight is not None
        if not is_leader:
            flight.event.wait()
            if flight.error is not None:
                raise flight.error
            assert flight.publication is not None
            ledger.note_reuse(query_id, f"coalesced with {flight.coalesced} concurrent duplicate(s)")
            return flight.publication

        return self._run_leader(key, query, data, query_id, ledger, flight)

    def _run_leader(
        self,
        key: Tuple[str, str],
        query: Query,
        data: SyntheticData,
        query_id: str,
        ledger: BudgetLedger,
        flight: _Inflight,
    ) -> Publication:
        try:
            ledger.charge(query_id, float(query.epsilon), note=f"new {query.kind} publication")
            truth = true_answer(query, data)
            if query.kind == "histogram":
                noisy = noisy_histogram(truth, query.epsilon, self._rng)
                value = MappingProxyType(dict(noisy))
            else:
                noisy = laplace_mechanism(truth, query.epsilon, query.sensitivity, self._rng)
                value = float(noisy)
            publication = Publication(
                query_id=query_id,
                data_version=data.version,
                epsilon=float(query.epsilon),
                value=value,
                coalesced=0,
                reused=False,
            )
            with self._lock:
                self._cache[key] = publication
                self._inflight.pop(key, None)
            flight.publication = publication
            return publication
        except BaseException as exc:
            with self._lock:
                self._inflight.pop(key, None)
            flight.error = exc
            raise
        finally:
            flight.event.set()

    def cached_keys(self, version: Optional[str] = None) -> List[Tuple[str, str]]:
        with self._lock:
            return sorted(k for k in self._cache if version is None or k[0] == version)

    def ledger_report(self, version: str) -> str:
        return self._ledger(version).report()

    def remaining_budget(self, version: str) -> float:
        return self._ledger(version).remaining
