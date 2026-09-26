"""Atomic plan-to-file rendering with cancellation.

The rendered PCM is written to a ``.part`` file and atomically renamed into
place; the ``.complete`` marker (carrying the input digest) is published
only after the full file exists. A cancelled write removes the partial file
and never publishes the complete-file marker.
"""

from __future__ import annotations

import array
import json
import os
from typing import Callable, Optional, Sequence

from .apply import verify_plan_input
from .plan import Plan

PART_SUFFIX = ".part"
COMPLETE_SUFFIX = ".complete"


class WriteCancelled(Exception):
    """Raised when ``cancel_check`` aborts a write in progress."""


def write_output(
    path: str,
    samples: Sequence[float],
    sample_rate: int,
    plan: Plan,
    chunk_size: int = 8192,
    cancel_check: Optional[Callable[[], bool]] = None,
) -> str:
    """Render ``plan`` to ``path`` as little-endian float32 PCM.

    Returns ``path`` on success. On cancellation or any failure, no output
    file and no ``.complete`` marker are left behind by this call.
    """
    if chunk_size <= 0:
        raise ValueError("chunk_size must be positive")
    verify_plan_input(plan, samples, sample_rate)
    curve = plan.gain_curve()
    tmp_path = path + PART_SUFFIX
    marker_path = path + COMPLETE_SUFFIX
    total = len(samples)
    try:
        with open(tmp_path, "wb") as fh:
            for lo in range(0, total, chunk_size):
                if cancel_check is not None and cancel_check():
                    raise WriteCancelled("output write cancelled")
                hi = min(lo + chunk_size, total)
                chunk = array.array(
                    "f", (samples[n] * curve.gain_at(n)
                          for n in range(lo, hi)))
                fh.write(chunk.tobytes())
        os.replace(tmp_path, path)
    except BaseException:
        if os.path.exists(tmp_path):
            os.remove(tmp_path)
        raise

    marker = {
        "complete": True,
        "input_digest": plan.input_digest,
        "num_samples": plan.num_samples,
        "sample_rate": plan.sample_rate,
        "plan_status": plan.status,
    }
    marker_tmp = marker_path + ".tmp"
    with open(marker_tmp, "w", encoding="utf-8") as fh:
        json.dump(marker, fh, indent=2, sort_keys=True)
    os.replace(marker_tmp, marker_path)
    return path
