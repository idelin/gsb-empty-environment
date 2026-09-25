"""Brute-force dense oracle for tiny problems (verification only)."""
from __future__ import annotations

import itertools

from .planner import collect_sizes


def dense_contract(tensors, output_labels):
    """Enumerate every label assignment; only feasible for tiny sizes."""
    sizes = collect_sizes(tensors)
    output_labels = tuple(output_labels)
    labels = sorted(sizes)
    if any(sizes[l] == 0 for l in labels):
        return {}
    result = {}
    ranges = [range(sizes[l]) for l in labels]
    for values in itertools.product(*ranges):
        assign = dict(zip(labels, values))
        product = 1.0
        for tensor in tensors:
            coord = tuple(assign[l] for l in tensor.labels)
            value = tensor.entries.get(coord, 0.0)
            if value == 0:
                product = 0.0
                break
            product *= value
        if product:
            out_coord = tuple(assign[l] for l in output_labels)
            result[out_coord] = result.get(out_coord, 0.0) + product
    return {c: v for c, v in result.items() if v != 0}
