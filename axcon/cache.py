"""Caches: chunk-level join indexes and version-bound subexpressions."""
from __future__ import annotations


def build_join_index(tensor, key_labels, bucket=None, bucket_of=None):
    """Map shared-label key -> [(coord, value)] for one tensor."""
    positions = {lab: p for p, lab in enumerate(tensor.labels)}
    index = {}
    for coord, value in tensor.entries.items():
        if bucket is not None and bucket_of(coord) != bucket:
            continue
        key = tuple(coord[positions[l]] for l in key_labels)
        index.setdefault(key, []).append((coord, value))
    return index


class ChunkIndexCache:
    """Per-bucket join indexes bound to tensor versions.

    Coordinates hash into ``num_buckets`` chunks; a delta only
    invalidates the buckets its own coordinates land in, so unrelated
    chunks stay cached.
    """

    def __init__(self, num_buckets=4):
        if num_buckets < 1:
            raise ValueError("num_buckets must be >= 1")
        self.num_buckets = num_buckets
        self._parts = {}
        self._known_versions = {}
        self.bucket_builds = 0

    def bucket_of(self, coord):
        return (coord[0] if coord else 0) % self.num_buckets

    def buckets_for_coords(self, coords):
        return {self.bucket_of(tuple(c)) for c in coords}

    def invalidate(self, tensor_name, buckets):
        for bucket in buckets:
            self._parts.pop((tensor_name, bucket), None)

    def invalidate_delta(self, tensor, delta_entries):
        """Drop only the buckets the delta's coordinates land in."""
        self.invalidate(tensor.name, self.buckets_for_coords(delta_entries))
        self._known_versions[tensor.name] = tensor.version

    def get_join_index(self, tensor, key_labels):
        key_labels = tuple(key_labels)
        if self._known_versions.get(tensor.name) != tensor.version:
            # version moved without a matching invalidate_delta: play safe
            # and rebuild every bucket of this tensor.
            self.invalidate(tensor.name, range(self.num_buckets))
            self._known_versions[tensor.name] = tensor.version
        merged = {}
        for bucket in range(self.num_buckets):
            part = self._parts.get((tensor.name, bucket))
            if part is None or part[0] != key_labels:
                part = (key_labels,
                        build_join_index(tensor, key_labels, bucket=bucket,
                                         bucket_of=self.bucket_of))
                self._parts[(tensor.name, bucket)] = part
                self.bucket_builds += 1
            for key, pairs in part[1].items():
                merged.setdefault(key, []).extend(pairs)
        return merged


class SubexpressionCache:
    """Caches step results keyed by structure plus input tensor versions."""

    def __init__(self):
        self._store = {}
        self.hits = 0
        self.misses = 0

    def get(self, signature, versions):
        key = (signature, frozenset(versions.items()))
        if key in self._store:
            self.hits += 1
            return self._store[key]
        self.misses += 1
        return None

    def put(self, signature, versions, tensor):
        self._store[(signature, frozenset(versions.items()))] = tensor
