"""Chunked application of a serialized plan, with input-digest verification."""
from __future__ import annotations

import hashlib
import json
import os
from array import array

from .planner import Plan, input_digest


class PlanInvalidError(Exception):
    """Raised when the audio no longer matches the plan's input digest."""


def _verify(plan: Plan, samples, sample_rate: int) -> None:
    if len(samples) != plan.num_samples:
        raise PlanInvalidError(
            f"length mismatch: plan expects {plan.num_samples} samples, "
            f"got {len(samples)}"
        )
    if int(sample_rate) != plan.sample_rate:
        raise PlanInvalidError(
            f"sample-rate mismatch: plan expects {plan.sample_rate}, "
            f"got {sample_rate}"
        )
    if input_digest(samples, sample_rate) != plan.input_digest:
        raise PlanInvalidError(
            "input digest mismatch: source audio changed since planning; "
            "the plan is invalid and must be recomputed"
        )


def apply_plan(plan: Plan, samples, sample_rate=None, chunks=None,
               verify: bool = True) -> array:
    """Apply the plan and return a new float32 array.

    `chunks` is an optional list of (start, end) ranges partitioning the
    signal. The gain at sample n is a pure function of n, so any chunking
    yields bit-identical output to a whole-buffer apply.
    """
    sr = plan.sample_rate if sample_rate is None else int(sample_rate)
    if verify:
        _verify(plan, samples, sr)
    out = array("f", bytes(4 * len(samples)))
    if chunks is None:
        chunks = [(0, len(samples))]
    for start, end in chunks:
        for n in range(start, end):
            out[n] = float(samples[n]) * plan.gain_at(n)
    return out


def apply_plan_to_file(plan: Plan, samples, out_path: str, sample_rate=None,
                       chunk_size: int = 4096, cancel_check=None,
                       verify: bool = True) -> bool:
    """Render to `out_path + '.part'` in chunks, then publish a marker.

    The completion marker `out_path + '.complete'` is written only after the
    full render succeeds. If `cancel_check(written, total)` returns True the
    render stops, no marker is published, and False is returned.
    """
    sr = plan.sample_rate if sample_rate is None else int(sample_rate)
    if verify:
        _verify(plan, samples, sr)
    part_path = out_path + ".part"
    marker_path = out_path + ".complete"
    if os.path.exists(marker_path):
        os.remove(marker_path)
    total = len(samples)
    with open(part_path, "wb") as fh:
        for start in range(0, total, chunk_size):
            end = min(start + chunk_size, total)
            block = apply_plan(plan, samples, sr, chunks=[(start, end)],
                               verify=False)
            fh.write(block[start:end].tobytes())
            if cancel_check is not None and cancel_check(end, total):
                return False
    h = hashlib.sha256()
    with open(part_path, "rb") as fh:
        for block in iter(lambda: fh.read(1 << 20), b""):
            h.update(block)
    marker = {
        "complete": True,
        "input_digest": plan.input_digest,
        "num_samples": total,
        "sample_rate": sr,
        "output_file": os.path.basename(part_path),
        "output_sha256": h.hexdigest(),
    }
    with open(marker_path, "w", encoding="utf-8") as fh:
        json.dump(marker, fh, indent=2, sort_keys=True)
    return True
