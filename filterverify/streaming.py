"""Streaming adapter: run an unknown streamable implementation in blocks.

Protocol expected from an implementation under test::

    class Impl:
        latency_samples: int          # declared group delay (samples)
        def reset(self) -> None: ...  # clear all state
        def process(self, block: list[float]) -> list[float]:
            ...                       # returns one output sample per input

Optionally an implementation may expose ``coefficients: list[float]``
(FIR taps); the verifier then adds conservative full-band bounds derived
from the coefficients instead of relying on measured grid points alone.
"""
from __future__ import annotations

import random
from typing import Protocol, Sequence, runtime_checkable


@runtime_checkable
class StreamableFilter(Protocol):
    latency_samples: int

    def reset(self) -> None: ...

    def process(self, block: Sequence[float]) -> list[float]: ...


def run_blocks(impl: StreamableFilter, samples: Sequence[float], partition: Sequence[int]) -> list[float]:
    """Feed ``samples`` through ``impl`` in consecutive blocks of ``partition`` sizes."""
    if sum(partition) != len(samples):
        raise ValueError("partition must sum to len(samples)")
    impl.reset()
    out: list[float] = []
    pos = 0
    for size in partition:
        block = list(samples[pos : pos + size])
        result = impl.process(block)
        if len(result) != len(block):
            raise ValueError(
                f"implementation returned {len(result)} samples for a "
                f"{len(block)}-sample block; sample-rate changes are unsupported"
            )
        out.extend(result)
        pos += size
    return out


def random_partition(
    n: int, rng: random.Random, max_blocks: int = 12, min_block: int = 1
) -> list[int]:
    """Random partition of n into 2..max_blocks blocks, each >= min_block."""
    max_blocks = min(max_blocks, n // max(1, min_block))
    if max_blocks < 2:
        return [n]
    for _ in range(64):
        count = rng.randint(2, max_blocks)
        cuts = sorted(rng.sample(range(1, n), count - 1))
        sizes = [b - a for a, b in zip([0] + cuts, cuts + [n])]
        if all(s >= min_block for s in sizes):
            return sizes
    return [n // 2, n - n // 2]
