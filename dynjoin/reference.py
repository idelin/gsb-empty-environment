"""Independent filter-less nested-loop join used as the correctness oracle."""
from __future__ import annotations

from enum import Enum


class JoinType(Enum):
    INNER = "inner"
    LEFT = "left"


class NullMode(Enum):
    REJECT = "reject"  # NULL never matches anything
    SAFE = "safe"      # NULL = NULL is a match


def keys_equal(a, b, null_mode):
    if a is None or b is None:
        return null_mode is NullMode.SAFE and a is None and b is None
    return a == b


def nested_loop_join(scan_rows, build_rows, join_type, null_mode):
    """scan_rows / build_rows: lists of (key, payload). Preserves the full
    Cartesian multiplicity of duplicate keys."""
    out = []
    for s_key, s_payload in scan_rows:
        matched = False
        for b_key, b_payload in build_rows:
            if keys_equal(s_key, b_key, null_mode):
                matched = True
                out.append((s_key, s_payload, b_key, b_payload))
        if not matched and join_type is JoinType.LEFT:
            out.append((s_key, s_payload, None, None))
    return out
