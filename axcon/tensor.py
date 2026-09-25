"""Sparse tensor with explicit axis labels, versions and idempotent deltas."""
from __future__ import annotations

from .errors import ValidationError


def validate_labels_shape(labels, shape):
    if len(labels) != len(shape):
        raise ValidationError(
            f"labels {labels!r} and shape {shape!r} have different ranks")
    for label in labels:
        if not isinstance(label, str) or not label:
            raise ValidationError(f"illegal axis label {label!r}")
    for dim in shape:
        if isinstance(dim, bool) or not isinstance(dim, int) or dim < 0:
            raise ValidationError(f"illegal dimension {dim!r}")


class SparseTensor:
    """COO-style sparse tensor.

    - Repeated labels within one tensor denote a diagonal: only coordinates
      whose repeated-label indices are equal are accepted.
    - ``version`` bumps on every applied mutation and binds caches.
    - ``apply_delta`` is idempotent per ``delta_id``; ``retract_delta``
      withdraws a previously applied delta.
    """

    def __init__(self, name, labels, shape, entries=None):
        self.name = name
        self.labels = tuple(labels)
        self.shape = tuple(shape)
        validate_labels_shape(self.labels, self.shape)
        self._entries = {}
        self._deltas = {}
        self.version = 0
        if entries:
            self.apply_delta("init", entries)

    @property
    def entries(self):
        return dict(self._entries)

    @property
    def nnz(self):
        return len(self._entries)

    @property
    def delta_ids(self):
        return tuple(sorted(self._deltas))

    def axis_size(self, label):
        return self.shape[self.labels.index(label)]

    def check_coord(self, coord):
        if len(coord) != len(self.labels):
            raise ValidationError(
                f"coordinate {coord!r} does not match rank {len(self.labels)}")
        seen = {}
        for label, index in zip(self.labels, coord):
            if isinstance(index, bool) or not isinstance(index, int):
                raise ValidationError(f"illegal index {index!r}")
            size = self.shape[self.labels.index(label)]
            if index < 0 or index >= size:
                raise ValidationError(
                    f"index {index} out of bounds for axis {label!r} "
                    f"of size {size}")
            if label in seen and seen[label] != index:
                raise ValidationError(
                    f"coordinate {coord!r} violates diagonal constraint "
                    f"of repeated label {label!r}")
            seen[label] = index

    def get(self, coord, default=0.0):
        return self._entries.get(tuple(coord), default)

    def _add(self, coord, value):
        merged = self._entries.get(coord, 0.0) + value
        if merged == 0:
            self._entries.pop(coord, None)
        else:
            self._entries[coord] = merged

    def apply_delta(self, delta_id, entries):
        """Apply a delta; returns False (no-op) if already applied."""
        if delta_id in self._deltas:
            return False
        normalized = {}
        for coord, value in entries.items():
            coord = tuple(coord)
            self.check_coord(coord)
            if value == 0:
                continue
            normalized[coord] = normalized.get(coord, 0.0) + value
        self._deltas[delta_id] = normalized
        for coord, value in normalized.items():
            self._add(coord, value)
        self.version += 1
        return True

    def retract_delta(self, delta_id):
        """Withdraw a previously applied delta; returns False if unknown."""
        if delta_id not in self._deltas:
            return False
        normalized = self._deltas.pop(delta_id)
        for coord, value in normalized.items():
            self._add(coord, -value)
        self.version += 1
        return True

    def __repr__(self):
        return (f"SparseTensor({self.name!r}, labels={self.labels!r}, "
                f"shape={self.shape!r}, nnz={self.nnz}, version={self.version})")
