"""Exact permutation tests via combinatorial-rank sharded enumeration.

The full permutation space is enumerated by rank.  Ranks can be split
into disjoint shards that are accumulated independently and merged, and
progress can be checkpointed to disk and resumed later.

Tail rules (the observed arrangement is always part of the space, so the
smallest attainable exact p-value is 1 / size):

- ``two-sided``: |T| >= |T_obs|   (equality included)
- ``greater``:   T >= T_obs       (equality included)
- ``less``:      T <= T_obs       (equality included)

Floating-point statistics are compared with a small relative tolerance so
that algebraically equal statistics are treated as equal.
"""
from __future__ import annotations

import json
from dataclasses import asdict, dataclass
from pathlib import Path
from typing import Iterator, Optional, Tuple

ALTERNATIVES = ("two-sided", "greater", "less")


def _tolerance(observed: float) -> float:
    return 1e-9 * max(1.0, abs(observed))


def is_extreme(stat: float, observed: float, alternative: str, tol: float) -> bool:
    """Tail rule with equality included on the threshold."""
    if alternative == "two-sided":
        return abs(stat) >= abs(observed) - tol
    if alternative == "greater":
        return stat >= observed - tol
    if alternative == "less":
        return stat <= observed + tol
    raise ValueError(f"unknown alternative: {alternative!r}")


@dataclass
class ExactAccumulator:
    """Resumable running tally over a (shard of the) permutation space."""

    n_extreme: int = 0
    n_total: int = 0

    def add(self, stat: float, observed: float, alternative: str, tol: float) -> None:
        self.n_total += 1
        if is_extreme(stat, observed, alternative, tol):
            self.n_extreme += 1

    def merge(self, other: "ExactAccumulator") -> "ExactAccumulator":
        return ExactAccumulator(
            n_extreme=self.n_extreme + other.n_extreme,
            n_total=self.n_total + other.n_total,
        )

    def to_dict(self) -> dict:
        return asdict(self)

    @classmethod
    def from_dict(cls, data: dict) -> "ExactAccumulator":
        return cls(n_extreme=int(data["n_extreme"]), n_total=int(data["n_total"]))


@dataclass
class ExactResult:
    """Exact p-value from full enumeration (not an estimate)."""

    p_value: float
    observed_statistic: float
    n_extreme: int
    n_total: int
    alternative: str
    design: str
    method: str = "exact (full enumeration)"
    includes_observed_arrangement: bool = True


def shard_ranges(total: int, n_shards: int) -> Iterator[Tuple[int, int]]:
    """Split ``range(total)`` into ``n_shards`` contiguous [start, end) shards."""
    if n_shards < 1:
        raise ValueError("n_shards must be >= 1")
    base, rem = divmod(total, n_shards)
    start = 0
    for i in range(n_shards):
        length = base + (1 if i < rem else 0)
        yield (start, start + length)
        start += length


def accumulate_shard(
    space,
    observed: float,
    alternative: str,
    start: int,
    end: int,
    accumulator: Optional[ExactAccumulator] = None,
) -> ExactAccumulator:
    """Accumulate tail counts over ranks [start, end); resumable via the
    passed-in / returned accumulator."""
    acc = accumulator if accumulator is not None else ExactAccumulator()
    tol = _tolerance(observed)
    for rank in range(start, end):
        acc.add(space.statistic(rank), observed, alternative, tol)
    return acc


def _checkpoint_file(path) -> None:
    Path(path).parent.mkdir(parents=True, exist_ok=True)


def exact_test(
    space,
    alternative: str = "two-sided",
    n_shards: int = 1,
    checkpoint_path: Optional[str] = None,
    resume: bool = False,
) -> ExactResult:
    """Enumerate the whole permutation space in shards and return the exact p-value.

    With ``checkpoint_path`` set, progress (next shard index + accumulator)
    is written after every shard; pass ``resume=True`` to continue an
    interrupted run from the checkpoint instead of starting over.
    """
    if alternative not in ALTERNATIVES:
        raise ValueError(f"alternative must be one of {ALTERNATIVES}")
    observed = space.statistic(space.observed_rank())
    ranges = list(shard_ranges(space.size, n_shards))

    acc = ExactAccumulator()
    first_shard = 0
    if resume and checkpoint_path is not None and Path(checkpoint_path).exists():
        state = json.loads(Path(checkpoint_path).read_text())
        if state.get("alternative") != alternative or state.get("design") != space.design:
            raise ValueError("checkpoint does not match this test configuration")
        acc = ExactAccumulator.from_dict(state["accumulator"])
        first_shard = int(state["next_shard"])

    for shard_index in range(first_shard, len(ranges)):
        start, end = ranges[shard_index]
        acc = accumulate_shard(space, observed, alternative, start, end, acc)
        if checkpoint_path is not None:
            _checkpoint_file(checkpoint_path)
            Path(checkpoint_path).write_text(json.dumps({
                "design": space.design,
                "alternative": alternative,
                "next_shard": shard_index + 1,
                "n_shards": len(ranges),
                "accumulator": acc.to_dict(),
            }))

    if acc.n_total != space.size:
        raise RuntimeError(
            f"enumerated {acc.n_total} of {space.size} permutations; "
            "shards did not cover the whole space"
        )
    return ExactResult(
        p_value=acc.n_extreme / acc.n_total,
        observed_statistic=observed,
        n_extreme=acc.n_extreme,
        n_total=acc.n_total,
        alternative=alternative,
        design=space.design,
    )
