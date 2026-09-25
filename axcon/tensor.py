"""Chunked sparse tensor with explicit axis labels and versioned deltas.

Coordinates and sizes are always explicit plain Python ints.  Storage is a
dict of chunks; each chunk is a dict mapping a full coordinate tuple to a
non-zero value.  Chunking bounds the memory touched by a single update and
allows fine-grained invalidation when a delta lands on one coordinate.
"""

from __future__ import annotations

from dataclasses import dataclass

from .errors import ValidationError


@dataclass(frozen=True)
class Delta:
    """An idempotent point update: ``tensor[coord] += value``.

    ``delta_id`` makes replays safe: applying the same Delta object twice is a
    no-op the second time, so a checkpoint crash cannot double-apply it.
    """

    delta_id: str
    coord: tuple
    value: float


def chunk_key_for(coord, blocks):
    return tuple(c // b for c, b in zip(coord, blocks))


class SparseTensor:
    """A sparse tensor keyed by explicit axis labels."""

    def __init__(self, name, axes, shape, *, block_size=4, entries=None, version=0):
        self.name = str(name)
        self.axes = tuple(axes)
        for label in self.axes:
            if not isinstance(label, str) or not label:
                raise ValidationError(f"axis label must be a non-empty string: {label!r}")
        self.shape = {}
        for label, size in dict(shape).items():
            if label not in self.axes:
                raise ValidationError(f"shape given for unknown label {label!r}")
            if not isinstance(size, int) or size < 0:
                raise ValidationError(f"illegal dimension for {label!r}: {size!r}")
            self.shape[label] = size
        for label in self.axes:
            if label not in self.shape:
                raise ValidationError(f"missing dimension for axis {label!r}")
        if block_size < 1:
            raise ValidationError("block_size must be >= 1")
        self.blocks = tuple(max(1, min(block_size, self.shape[l] or 1)) for l in self.axes)
        self.version = int(version)
        self.chunks = {}
        self._applied_delta_ids = set()
        self.dirty_chunks = set()
        if entries:
            for coord, value in dict(entries).items():
                self.set(coord, value)
            self.dirty_chunks.clear()

    # -- introspection -------------------------------------------------
    @property
    def ndim(self):
        return len(self.axes)

    @property
    def nnz(self):
        return sum(len(chunk) for chunk in self.chunks.values())

    def size(self, label):
        try:
            return self.shape[label]
        except KeyError:
            raise ValidationError(f"unknown axis label {label!r} on {self.name}")

    def iter_entries(self):
        for key in sorted(self.chunks):
            for coord, value in sorted(self.chunks[key].items()):
                yield coord, value

    # -- validation ----------------------------------------------------
    def validate_coord(self, coord):
        coord = tuple(coord)
        if len(coord) != self.ndim:
            raise ValidationError(
                f"{self.name}: expected {self.ndim} indices, got {coord!r}")
        for axis, (c, size) in enumerate(zip(coord, (self.shape[l] for l in self.axes))):
            if not isinstance(c, int) or c < 0 or c >= size:
                raise ValidationError(
                    f"{self.name}: index {c} out of range on axis {axis} "
                    f"(size {size})")
        return coord

    # -- mutation ------------------------------------------------------
    def set(self, coord, value):
        coord = self.validate_coord(coord)
        key = chunk_key_for(coord, self.blocks)
        chunk = self.chunks.setdefault(key, {})
        if value == 0:
            chunk.pop(coord, None)
            if not chunk:
                self.chunks.pop(key, None)
        else:
            chunk[coord] = value
        self.dirty_chunks.add(key)

    def get(self, coord, default=0.0):
        coord = tuple(coord)
        key = chunk_key_for(coord, self.blocks)
        return self.chunks.get(key, {}).get(coord, default)

    def apply_delta(self, delta):
        """Apply a Delta idempotently; returns True iff it was applied."""
        if delta.delta_id in self._applied_delta_ids:
            return False
        coord = self.validate_coord(delta.coord)
        self.set(coord, self.get(coord) + delta.value)
        self._applied_delta_ids.add(delta.delta_id)
        self.version += 1
        return True

    def clear_dirty(self):
        self.dirty_chunks.clear()

    def copy(self, name=None):
        clone = SparseTensor(name or self.name, self.axes, self.shape,
                             block_size=max(self.blocks), version=self.version)
        clone.chunks = {k: dict(v) for k, v in self.chunks.items()}
        clone._applied_delta_ids = set(self._applied_delta_ids)
        return clone

    def __repr__(self):
        return (f"SparseTensor({self.name!r}, axes={self.axes!r}, "
                f"nnz={self.nnz}, version={self.version})")
