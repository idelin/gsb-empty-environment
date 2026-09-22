"""Dynamic dependency build kernel.

Single-threaded cooperative scheduler with controllable step boundaries.
"""
from __future__ import annotations

import os
import shutil
import uuid
from dataclasses import dataclass, field
from typing import Callable, Iterable

from . import identity as I
from .cache import CacheEntry, CacheStore
from .depgraph import CycleError, DependencyGraph
from .ledger import Ledger
from .taskapi import NodeSpec, Observation, TaskContext, build_cache_key
from .wal import WriteAheadLog
from .workspace import Workspace


class BuildError(Exception):
    pass


class TaskFailed(BuildError):
    def __init__(self, node: str, original: BaseException):
        self.node = node
        self.original = original
        super().__init__(f"task {node!r} failed: {original!r}")


class StaleGeneration(BuildError):
    def __init__(self, node: str, attempted: int, current: int):
        self.node = node
        self.attempted = attempted
        self.current = current
        super().__init__(
            f"stale generation for {node!r}: attempt gen {attempted} < {current}"
        )


@dataclass
class Request:
    rid: str
    node: str
    cancelled: bool = False
    outcome: dict | None = None  # {"status": "ok"|"error"|"cancelled", ...}
    inflight: bool = False


@dataclass
class Attempt:
    id: str
    node: str
    generation: int
    spec: NodeSpec
    context: TaskContext
    staging_dir: str
    staged: dict[str, str] = field(default_factory=dict)  # identity -> temp path
    observed: tuple[Observation, ...] = ()
    cache_key: str = ""
    output_digests: tuple[tuple[str, str], ...] = ()
    producer_deps: set[str] = field(default_factory=set)
    delivered: bool = False
    preserve_for_late_commit: bool = False


# Phase hook receives (kernel, phase, node). Raise CrashNow to simulate failure.
PhaseHook = Callable[["Kernel", str, str], None]


class CrashNow(Exception):
    """Raised from a phase hook to simulate a process crash at that boundary."""
