"""Build executor: single-flight concurrency, all-or-nothing publishing.

A build writes its outputs into a private staging directory. The builder
publishes only when *every* declared output is present; any missing output
or raised exception fails the build, cleans up the staging directory and
publishes nothing. Concurrent builds of the same key share one in-flight
execution ("single flight"); a forced rebuild of identical content is
deduped by the cache, different content is flagged as a conflict.
"""
from __future__ import annotations

import shutil
import tempfile
import threading
from dataclasses import dataclass
from pathlib import Path

from .cache import BuildCache, CacheEntry


class BuildError(Exception):
    """A build did not produce every required output."""


@dataclass
class BuildNode:
    key: str
    outputs: tuple
    action: object  # Callable[[Path], None]: writes outputs into the staging dir

    def __post_init__(self):
        if not self.outputs:
            raise ValueError("a build node must declare at least one output")
        self.outputs = tuple(self.outputs)


@dataclass
class BuildResult:
    key: str
    status: str  # "cache-hit" | "published" | "deduped"
    entry: CacheEntry


class _Flight:
    __slots__ = ("done", "result", "error")

    def __init__(self):
        self.done = threading.Event()
        self.result: BuildResult | None = None
        self.error: BaseException | None = None


class Builder:
    def __init__(self, cache: BuildCache, work_root: str | Path | None = None):
        self.cache = cache
        self.work_root = Path(work_root) if work_root else None
        if self.work_root is not None:
            self.work_root.mkdir(parents=True, exist_ok=True)
        self._lock = threading.Lock()
        self._flights: dict[str, _Flight] = {}

    def build(self, node: BuildNode, *, force: bool = False) -> BuildResult:
        if not force:
            hit = self.cache.lookup(node.key)
            if hit is not None:
                return BuildResult(node.key, "cache-hit", hit)

        with self._lock:
            flight = self._flights.get(node.key)
            leader = flight is None
            if leader:
                flight = _Flight()
                self._flights[node.key] = flight

        if not leader:
            flight.done.wait()
            if flight.error is not None:
                raise flight.error
            return flight.result

        try:
            flight.result = self._run(node, force=force)
            return flight.result
        except BaseException as exc:
            flight.error = exc
            raise
        finally:
            flight.done.set()
            with self._lock:
                self._flights.pop(node.key, None)

    def _run(self, node: BuildNode, *, force: bool) -> BuildResult:
        if not force:
            hit = self.cache.lookup(node.key)
            if hit is not None:
                return BuildResult(node.key, "cache-hit", hit)

        staging = Path(tempfile.mkdtemp(prefix="build-", dir=self.work_root))
        try:
            node.action(staging)

            missing = [name for name in node.outputs
                       if not (staging / name).is_file()]
            if missing:
                raise BuildError(
                    f"key {node.key!r} missing outputs {missing}; "
                    "publishing nothing")

            outputs = {name: (staging / name).read_bytes()
                       for name in node.outputs}
            entry, published = self.cache.publish(node.key, outputs)
            return BuildResult(node.key,
                               "published" if published else "deduped", entry)
        finally:
            shutil.rmtree(staging, ignore_errors=True)
