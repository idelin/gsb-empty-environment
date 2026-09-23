"""Build side: versioned partitions with epoch, monotonic seq and end barrier.

A partition is complete only when the barrier has arrived AND every seq up to
last_seq has been applied. Barrier-first, duplicate redelivery and out-of-order
data are all handled. A rebuild creates a new epoch; old epochs stay alive
while running scan batches reference them and are released afterwards.
"""
from __future__ import annotations

from .filters import BloomFilter, ExactFilter
from .messages import NULL, BuildData


class BuildPartition:
    def __init__(self, partition_id, epoch, filter_kind="exact",
                 bloom_size=1024, bloom_hashes=3):
        self.partition_id = partition_id
        self.epoch = epoch
        self.filter_kind = filter_kind
        self.bloom_size = bloom_size
        self.bloom_hashes = bloom_hashes
        self.rows = {}        # nkey -> [(key, payload, seq), ...] in apply order
        self.applied = set()  # seq numbers already applied (dedup / reorder)
        self.barrier = None   # last_seq once the barrier arrived
        self.refs = 0         # live scan-batch snapshots bound to this version
        self.released = False
        self._filter = None   # transient cache, rebuilt lazily

    def __getstate__(self):
        state = self.__dict__.copy()
        state["_filter"] = None  # filters hold an unpicklable hash_fn; rebuild
        return state

    def apply(self, msg: BuildData) -> bool:
        """Idempotent apply. Returns False for duplicate redelivery."""
        if msg.seq in self.applied:
            return False
        self.applied.add(msg.seq)
        nkey = NULL if msg.key is None else msg.key
        self.rows.setdefault(nkey, []).append((msg.key, msg.payload, msg.seq))
        self._filter = None
        return True

    def set_barrier(self, last_seq):
        self.barrier = last_seq

    @property
    def complete(self):
        return self.barrier is not None and len(self.applied) >= self.barrier

    def get_filter(self, hash_fn):
        """The dynamic filter. Only meaningful once `complete` is true; the
        engine never consults it before then."""
        if self._filter is None:
            if self.filter_kind == "exact":
                filt = ExactFilter()
            else:
                filt = BloomFilter(self.bloom_size, self.bloom_hashes, hash_fn)
            for nkey in self.rows:
                filt.add(nkey)
            self._filter = filt
        return self._filter

    def probe(self, nkey):
        return self.rows.get(nkey, ())


class BuildSide:
    def __init__(self, num_partitions, filter_kind="exact",
                 bloom_size=1024, bloom_hashes=3):
        self.num_partitions = num_partitions
        self.filter_kind = filter_kind
        self.bloom_size = bloom_size
        self.bloom_hashes = bloom_hashes
        self.current = [
            BuildPartition(p, 1, filter_kind, bloom_size, bloom_hashes)
            for p in range(num_partitions)
        ]
        self.by_version = {(p, 1): self.current[p] for p in range(num_partitions)}

    def route(self, msg):
        """Route by (partition, epoch); unknown/released epochs return None."""
        return self.by_version.get((msg.partition, msg.epoch))

    def new_partition(self, partition, epoch):
        return BuildPartition(partition, epoch, self.filter_kind,
                              self.bloom_size, self.bloom_hashes)

    def maybe_release(self, bp):
        """Release an old epoch once no scan batch references it."""
        if (bp.refs <= 0 and not bp.released
                and bp is not self.current[bp.partition_id]):
            bp.released = True
            bp.rows.clear()
            bp.applied.clear()
            self.by_version.pop((bp.partition_id, bp.epoch), None)
