"""Sort-key encoding: multi-field specs, direction, null placement, id tie-break.

A sort spec is a tuple of fields, each field a triple::

    (field_name, descending, nulls_first)

Keys are encoded so that a single ascending tuple comparison yields the
desired order; the primary key is appended as the final tie-breaker, which
makes ordering total and stable for equal field values.
"""
from functools import total_ordering


@total_ordering
class Reverse:
    """Wrapper that inverts comparison order (for descending fields)."""

    __slots__ = ("value",)

    def __init__(self, value):
        self.value = value

    def __lt__(self, other):
        return other.value < self.value

    def __eq__(self, other):
        return isinstance(other, Reverse) and self.value == other.value

    def __hash__(self):
        return hash(("Reverse", self.value))

    def __repr__(self):
        return "Reverse(%r)" % (self.value,)


def field(name, desc=False, nulls_first=False):
    """Convenience constructor for one sort-spec field."""
    return (name, bool(desc), bool(nulls_first))


def encode_field(value, desc, nulls_first):
    """Encode one field value as a comparable (marker, payload) pair."""
    if value is None:
        return (0 if nulls_first else 1, None)
    marker = 1 if nulls_first else 0
    return (marker, Reverse(value) if desc else value)


def build_key(spec, record, pk):
    """Build the full sort key for ``record``; ``pk`` breaks ties stably."""
    parts = [encode_field(record.get(name), desc, nf) for name, desc, nf in spec]
    parts.append(pk)
    return tuple(parts)


def key_values(spec, record):
    """Raw (unencoded) field values, used for display/snapshot output."""
    return tuple(record.get(name) for name, _, _ in spec)
