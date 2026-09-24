"""Build side: versioned snapshots plus per-partition filter construction.

Two separate objects exist per epoch:

* the *snapshot table* -- the complete build relation, published atomically
  by ``commit_epoch`` and what the join actually probes;
* the *dynamic filter* -- built incrementally from the data/barrier message
  stream, purely an optimization to skip probes.

A partition's filter is complete only after its barrier arrived AND every
data message up to the barrier's last_seq has arrived and been applied.
Early barriers, re-sent data and out-of-order delivery are all handled.
"""
from __future__ import annotations

from collections import defaultdict
from typing import Any, Callable, Optional

from .events import BuildBarrier, BuildData
from .filters import ExactSetFilter

FilterFactory = Callable[[], Any]  # returns ExactSetFilter | BloomFilter


def default_partitioner(num_partitions: int) -> Callable[[Any], int]:
    def part(key: Any) -> int:
        return hash(repr(key)) % num_partitions
    return part


class PartitionState:
    """Filter-build state for one (epoch, partition)."""

    __slots__ = ("epoch", "partition", "filter", "received", "barrier",
                 "next_expected", "complete")

    def __init__(self, epoch: int, partition: int, filter_obj: Any) -> None:
        self.epoch = epoch
        self.partition = partition
        self.filter = filter_obj
        self.received: set[int] = set()      # seqs applied (dedup on resend)
        self.barrier: Optional[int] = None   # last_seq from barrier, if seen
        self.next_expected = 1               # contiguous-prefix tracker
        self.complete = False

    def apply_data(self, msg: BuildData) -> bool:
        """Apply a data message. Returns True if it was new (not a resend)."""
        if msg.seq in self.received:
            return False  # re-sent data: idempotent
        self.received.add(msg.seq)
        self.filter.add(msg.key)
        self._advance()
        return True

    def apply_barrier(self, msg: BuildBarrier) -> None:
        # Barrier may arrive before some/all data, or be re-sent; keep the max.
        self.barrier = msg.last_seq if self.barrier is None else max(self.barrier, msg.last_seq)
        self._advance()

    def _advance(self) -> None:
        while self.next_expected in self.received:
            self.next_expected += 1
        if self.barrier is not None and self.next_expected > self.barrier:
            self.complete = True  # barrier seen AND all prior messages applied

    # -- checkpoint (de)serialization --
    def to_dict(self) -> dict:
        return {
            "received": sorted(self.received),
            "barrier": self.barrier,
            "next_expected": self.next_expected,
            "complete": self.complete,
        }

    def restore_meta(self, data: dict, rebuild_keys: list[Any]) -> None:
        self.received = set(data["received"])
        self.barrier = data["barrier"]
        self.next_expected = data["next_expected"]
        self.complete = data["complete"]
        for key in rebuild_keys:  # filter itself is rebuilt from the keys
            self.filter.add(key)


class EpochEntry:
    """One build version: snapshot table + per-partition filter states."""

    def __init__(self, epoch: int, rows: tuple, num_partitions: int,
                 partitioner: Callable[[Any], int], filter_factory: FilterFactory) -> None:
        self.epoch = epoch
        # Snapshot table with full duplicate multiplicity: key -> [payloads].
        self.table: dict[Any, list] = defaultdict(list)
        for key, payload in rows:
            self.table[key].append(payload)
        self.partitioner = partitioner
        self.partitions: list[PartitionState] = [
            PartitionState(epoch, p, filter_factory()) for p in range(num_partitions)
        ]
        self.refs = 0  # running scan batches pinning this version

    def partition_of(self, key: Any) -> int:
        return self.partitioner(key)


class BuildSide:
    """Registry of epochs. Old epochs are released once no scan batch uses them."""

    def __init__(self, num_partitions: int = 4,
                 filter_factory: FilterFactory | None = None,
                 partitioner: Callable[[Any], int] | None = None) -> None:
        self.num_partitions = num_partitions
        self.filter_factory = filter_factory or ExactSetFilter
        self.partitioner = partitioner or default_partitioner(num_partitions)
        self.epochs: dict[int, EpochEntry] = {}
        self.current_epoch: Optional[int] = None
        # completions observed since last drain, consumed by the engine:
        # list of (epoch, partition)
        self.completion_events: list[tuple[int, int]] = []

    # ------------------------------------------------------------ snapshots
    def commit_epoch(self, epoch: int, rows: tuple) -> None:
        """Publish a new build version (partition rebuild => new epoch)."""
        if epoch in self.epochs:
            raise ValueError(f"epoch {epoch} already committed")
        self.epochs[epoch] = EpochEntry(epoch, rows, self.num_partitions,
                                        self.partitioner, self.filter_factory)
        self.current_epoch = epoch
        self._gc()

    def acquire(self, epoch: int) -> EpochEntry:
        entry = self.epochs[epoch]  # KeyError => batch bound to unknown snapshot
        entry.refs += 1
        return entry

    def release(self, epoch: int) -> None:
        entry = self.epochs[epoch]
        entry.refs -= 1
        self._gc()

    def _gc(self) -> None:
        """Old epochs (and their filters) are freed once unreferenced; the
        current epoch is always kept. Never mix old filters with the new
        build table: lookups go through the epoch the batch is bound to."""
        for epoch in [e for e, en in self.epochs.items()
                      if en.refs <= 0 and e != self.current_epoch]:
            del self.epochs[epoch]

    # ------------------------------------------------------------- messages
    def handle(self, msg: BuildData | BuildBarrier) -> None:
        entry = self.epochs.get(msg.epoch)
        if entry is None:
            return  # message for a released/unknown epoch: nothing to apply to
        state = entry.partitions[msg.partition]
        was = state.complete
        if isinstance(msg, BuildData):
            state.apply_data(msg)
        else:
            state.apply_barrier(msg)
        if state.complete and not was:
            self.completion_events.append((msg.epoch, msg.partition))

    def take_completions(self) -> list[tuple[int, int]]:
        events, self.completion_events = self.completion_events, []
        return events

    # ------------------------------------------------------------ checkpoint
    def filters_to_dict(self) -> dict:
        out = {}
        for epoch, entry in self.epochs.items():
            out[epoch] = {p: s.to_dict() for p, s in enumerate(entry.partitions)}
        return out

    def restore_filters(self, data: dict, data_log: list[BuildData]) -> None:
        """Restore partition completeness metadata; rebuild each filter by
        replaying the keys of the data messages it had applied."""
        for epoch_s, parts in data.items():
            entry = self.epochs.get(int(epoch_s))
            if entry is None:
                continue
            for p_s, meta in parts.items():
                state = entry.partitions[int(p_s)]
                keys = [m.key for m in data_log
                        if m.epoch == int(epoch_s) and m.partition == int(p_s)
                        and m.seq in set(meta["received"])]
                state.restore_meta(meta, keys)
