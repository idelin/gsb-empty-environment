"""Coordinate builds so concurrent duplicate builds collapse into one.

This is the classic "single flight" pattern: while a build for a given cache
key is running, any number of concurrent callers wait on the same build
instead of executing it again. When the leader finishes publishing, waiters
simply observe the cache (the artifacts are identical by construction of the
key); if the leader fails, every waiter receives the same failure and no
entry or staging data is left behind.
"""

from __future__ import annotations

import enum
import threading
from dataclasses import dataclass
from pathlib import Path
from typing import Any, Callable, Optional, Sequence

from .cache import BuildCache, PublishResult


class BuildStatus(enum.Enum):
    PUBLISHED = "published"
    DEDUPLICATED = "deduplicated"
    CACHE_HIT = "cache_hit"
    COALESCED = "coalesced"


@dataclass(frozen=True)
class BuildOutcome:
    key: str
    status: BuildStatus
    leader: bool
    result: Optional[PublishResult] = None
    error: Optional[BaseException] = None

    @property
    def reusable(self) -> bool:
        return self.status in (
            BuildStatus.PUBLISHED,
            BuildStatus.DEDUPLICATED,
            BuildStatus.CACHE_HIT,
            BuildStatus.COALESCED,
        )


Builder = Callable[[str, Path], None]
"""A builder receives (cache_key, staging_dir) and must create every declared
output file inside staging_dir before returning."""


@dataclass
class _Flight:
    event: threading.Event
    error: Optional[BaseException] = None
    result: Optional[PublishResult] = None


class BuildCoordinator:
    def __init__(self, cache: BuildCache) -> None:
        self.cache = cache
        self._lock = threading.Lock()
        self._flights: dict[str, _Flight] = {}

    def in_flight(self, key: str) -> bool:
        with self._lock:
            return key in self._flights

    def build(
        self,
        key: str,
        builder: Builder,
        *,
        package: str,
        version: Any,
        inputs: Any,
        outputs: Sequence[str],
    ) -> BuildOutcome:
        """Build and publish *key*, coalescing concurrent duplicate builds."""
        if self.cache.has(key):
            return BuildOutcome(key, BuildStatus.CACHE_HIT, leader=False)

        flight: Optional[_Flight] = None
        leader = False
        with self._lock:
            existing = self._flights.get(key)
            if existing is None:
                flight = _Flight(event=threading.Event())
                self._flights[key] = flight
                leader = True
            else:
                flight = existing

        if not leader:
            # Concurrent duplicate build: wait for the leader's outcome.
            flight.event.wait()
            if flight.error is not None:
                return BuildOutcome(
                    key, BuildStatus.COALESCED, leader=False, error=flight.error
                )
            if self.cache.has(key):
                return BuildOutcome(key, BuildStatus.COALESCED, leader=False)
            # Leader reported success without an entry appearing: surface the
            # same failure rather than silently claiming a cache hit.
            return BuildOutcome(
                key,
                BuildStatus.COALESCED,
                leader=False,
                error=RuntimeError("leader build completed but cache entry is missing"),
            )

        # Leader path: run the build into a fresh staging directory.
        staging = self.cache.tmp_dir / f".build-{key[:8]}-{threading.get_ident()}"
        if staging.exists():
            import shutil

            shutil.rmtree(staging)
        staging.mkdir(parents=True)
        error: Optional[BaseException] = None
        result: Optional[PublishResult] = None
        try:
            builder(key, staging)
            result = self.cache.publish(
                key,
                staging,
                package=package,
                version=version,
                inputs=inputs,
                outputs=outputs,
                attempt=str(threading.get_ident()),
            )
        except BaseException as exc:  # propagate to all waiters, then re-raise
            error = exc
        finally:
            import shutil

            shutil.rmtree(staging, ignore_errors=True)
            with self._lock:
                flight.error = error
                flight.result = result
                flight.event.set()
                self._flights.pop(key, None)

        if error is not None:
            raise error
        status = (
            BuildStatus.PUBLISHED
            if result is PublishResult.PUBLISHED
            else BuildStatus.DEDUPLICATED
        )
        return BuildOutcome(key, status, leader=True, result=result)
