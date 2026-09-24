"""Message and event definitions for the partitioned hash join simulator.

Everything is plain data; the local test scheduler decides delivery order,
clock advances and fault injection. No message service is involved.
"""
from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any, Optional

Key = Any
Row = tuple  # (key, payload)


# ---------------------------------------------------------------- build stream
@dataclass(frozen=True)
class BuildData:
    """One build-side row for a partition. seq is monotone per (epoch, partition)."""
    partition: int
    epoch: int
    seq: int
    key: Key
    payload: Any


@dataclass(frozen=True)
class BuildBarrier:
    """End barrier: all data messages with seq <= last_seq were sent for this
    (epoch, partition). The filter only becomes complete once every such
    message has actually arrived and been applied."""
    partition: int
    epoch: int
    last_seq: int


# ------------------------------------------------------------------ scan side
@dataclass(frozen=True)
class ScanBatch:
    """A batch of scan rows bound to a build snapshot (epoch)."""
    batch_id: str
    build_epoch: int
    rows: tuple  # tuple of (key, payload)


# ------------------------------------------------------------------ scheduler
# The scheduler consumes a script of these events, in order.
@dataclass(frozen=True)
class Send:
    msg: Any  # BuildData | BuildBarrier


@dataclass(frozen=True)
class CommitEpoch:
    """Atomically publish a new build snapshot (partition rebuild => new epoch)."""
    epoch: int
    rows: tuple  # full build relation for this epoch: ((key, payload), ...)


@dataclass(frozen=True)
class Submit:
    batch: ScanBatch


@dataclass(frozen=True)
class Tick:
    n: int = 1


@dataclass(frozen=True)
class Checkpoint:
    pass


@dataclass(frozen=True)
class Crash:
    """Simulated fault: in-memory, non-checkpointed progress is lost."""
    pass


@dataclass(frozen=True)
class Recover:
    pass


@dataclass(frozen=True)
class Ack:
    """Test consumer acknowledges an output id."""
    out_id: str


@dataclass(frozen=True)
class AckAll:
    pass
