"""Dense-enumeration oracle used to verify the chunked engine on small inputs.

This intentionally expands the full Cartesian product of every axis, so it is
only suitable for tiny tensors -- exactly what an oracle should be.
"""

from __future__ import annotations

import itertools

from .errors import ValidationError


def validate_inputs(tensors, out_axes):
    """Shared validation for both the oracle and the engine."""
    out_axes = tuple(out_axes)
    if len(set(out_axes)) != len(out_axes):
        raise ValidationError(f"duplicate output labels: {out_axes!r}")
    names = [t.name for t in tensors]
    if len(set(names)) != len(names):
        raise ValidationError(f"tensor names must be unique: {names!r}")
    dims = {}
    for t in tensors:
        for label in t.axes:
            size = t.shape[label]
            if label in dims and dims[label] != size:
                raise ValidationError(
                    f"label {label!r} has inconsistent sizes "
                    f"{dims[label]} and {size}")
            dims[label] = size
    for label in out_axes:
        if label not in dims:
            raise ValidationError(f"output label {label!r} not present in inputs")
    return dims


def dense_contract(tensors, out_axes):
    """Contract ``tensors`` onto ``out_axes`` by brute-force enumeration.

    Handles repeated labels (diagonals), broadcasting, and scalars.  Returns a
    dict mapping output coordinate tuples to values (zeros omitted).
    """
    tensors = list(tensors)
    out_axes = tuple(out_axes)
    dims = validate_inputs(tensors, out_axes)
    labels = []
    for t in tensors:
        for label in t.axes:
            if label not in labels:
                labels.append(label)
    ranges = [range(dims[l]) for l in labels]
    result = {}
    for assignment in itertools.product(*ranges):
        env = dict(zip(labels, assignment))
        product = 1
        for t in tensors:
            seen = {}
            coord = []
            consistent = True
            for label in t.axes:
                value = env[label]
                if label in seen and seen[label] != value:
                    consistent = False
                    break
                seen[label] = value
                coord.append(value)
            if not consistent:
                product = 0
                break
            product *= t.get(tuple(coord))
            if product == 0:
                break
        if product:
            out_coord = tuple(env[l] for l in out_axes)
            result[out_coord] = result.get(out_coord, 0) + product
    return {c: v for c, v in result.items() if v != 0}
