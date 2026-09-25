"""Chunked pairwise contraction engine with spill and checkpointing.

The engine never expands the full Cartesian product: entries of the left
operand are joined against an in-memory index of the right operand keyed by
the shared-label coordinates.  The output accumulator is bounded by
``Budget.buffer_entries``; when it fills up it is spilled to a JSON run file
in ``workdir`` and merged at the end.  After every processed chunk a
checkpoint (done chunks, spill files, current buffer, contribution count) is
written, so an interrupted run resumes without re-applying any contribution.
"""

from __future__ import annotations

import json
import os
import shutil
import tempfile
from dataclasses import dataclass, field

from .errors import BudgetExceeded, ValidationError
from .tensor import SparseTensor


@dataclass
class Budget:
    """Resource limits for a single pairwise contraction."""

    buffer_entries: int = 100_000      # spill threshold for the output buffer
    max_contributions: int = 50_000_000  # abort threshold (work/output cap)


@dataclass
class StepStats:
    chunks_processed: int = 0
    chunks_skipped: int = 0
    contributions: int = 0
    spill_files: int = 0
    spilled_entries: int = 0
    output_nnz: int = 0
    resumed: bool = False


def extract_diagonals(tensor):
    """Collapse repeated labels: keep only entries whose repeated indices agree."""
    if len(set(tensor.axes)) == len(tensor.axes):
        return tensor
    unique_axes = tuple(dict.fromkeys(tensor.axes))
    positions = {label: [i for i, l in enumerate(tensor.axes) if l == label]
                 for label in unique_axes}
    out = SparseTensor(tensor.name + "|diag", unique_axes,
                       {l: tensor.shape[l] for l in unique_axes},
                       block_size=max(tensor.blocks))
    for coord, value in tensor.iter_entries():
        if all(len({coord[i] for i in pos}) == 1 for pos in positions.values()):
            out.set(tuple(coord[pos[0]] for pos in positions.values()), value)
    out.dirty_chunks.clear()
    return out


def _validate_pair(a, b, out_axes):
    for label in out_axes:
        if label not in a.shape and label not in b.shape:
            raise ValidationError(f"output label {label!r} not in either operand")
    if len(set(out_axes)) != len(out_axes):
        raise ValidationError(f"duplicate output labels: {out_axes!r}")
    for label in a.axes:
        if label in b.shape and a.shape[label] != b.shape[label]:
            raise ValidationError(
                f"label {label!r} size mismatch: {a.shape[label]} vs {b.shape[label]}")


def _encode(coord):
    return ",".join(str(c) for c in coord)


def _decode(text):
    return tuple(int(c) for c in text.split(",")) if text else ()


class _Checkpoint:
    def __init__(self, workdir):
        self.path = os.path.join(workdir, "checkpoint.json")
        self.done = set()
        self.spills = []
        self.buffer = {}
        self.contributions = 0
        self.resumed = False

    def load(self, a, b):
        if not os.path.exists(self.path):
            return
        with open(self.path, "r", encoding="utf-8") as fh:
            state = json.load(fh)
        if state.get("versions") != [a.name, a.version, b.name, b.version]:
            # Inputs changed since the checkpoint: discard and start over.
            for name in state.get("spills", []):
                _silent_remove(os.path.join(os.path.dirname(self.path), name))
            _silent_remove(self.path)
            return
        self.done = {tuple(map(int, k)) for k in state["done"]}
        self.spills = list(state["spills"])
        self.buffer = {_decode(k): v for k, v in state["buffer"].items()}
        self.contributions = state["contributions"]
        self.resumed = True

    def save(self, a, b):
        state = {
            "versions": [a.name, a.version, b.name, b.version],
            "done": sorted([list(k) for k in self.done]),
            "spills": self.spills,
            "buffer": {_encode(c): v for c, v in self.buffer.items()},
            "contributions": self.contributions,
        }
        tmp = self.path + ".tmp"
        with open(tmp, "w", encoding="utf-8") as fh:
            json.dump(state, fh)
        os.replace(tmp, self.path)


def _silent_remove(path):
    try:
        os.remove(path)
    except OSError:
        pass


def _spill(buffer, workdir, seq):
    name = f"spill_{seq:04d}.json"
    with open(os.path.join(workdir, name), "w", encoding="utf-8") as fh:
        json.dump({_encode(c): v for c, v in buffer.items()}, fh)
    return name


def _merge(spills, buffer, workdir):
    merged = {}
    for name in spills:
        with open(os.path.join(workdir, name), "r", encoding="utf-8") as fh:
            for key, value in json.load(fh).items():
                coord = _decode(key)
                merged[coord] = merged.get(coord, 0) + value
    for coord, value in buffer.items():
        merged[coord] = merged.get(coord, 0) + value
    return {c: v for c, v in merged.items() if v != 0}


def contract_pair(a, b, out_axes, *, budget=None, workdir=None, chunk_hook=None,
                  out_name=None):
    """Contract two sparse tensors onto ``out_axes``.

    ``chunk_hook`` (if given) is called after each chunk is committed to the
    checkpoint; tests use it to simulate a crash.  Returns ``(tensor, stats)``.
    Raises ``BudgetExceeded`` when the contribution cap is hit.
    """
    budget = budget or Budget()
    a = extract_diagonals(a)
    b = extract_diagonals(b)
    out_axes = tuple(out_axes)
    _validate_pair(a, b, out_axes)

    shared = [l for l in a.axes if l in b.shape]
    a_pos = {l: i for i, l in enumerate(a.axes)}
    b_pos = {l: i for i, l in enumerate(b.axes)}
    out_shape = {}
    for label in out_axes:
        out_shape[label] = a.shape[label] if label in a_pos else b.shape[label]

    b_index = {}
    for coord_b, value_b in b.iter_entries():
        key = tuple(coord_b[b_pos[l]] for l in shared)
        b_index.setdefault(key, []).append((coord_b, value_b))

    owns_workdir = workdir is None
    workdir = workdir or tempfile.mkdtemp(prefix="axcon_")
    os.makedirs(workdir, exist_ok=True)
    ckpt = _Checkpoint(workdir)
    ckpt.load(a, b)

    stats = StepStats(resumed=ckpt.resumed, contributions=ckpt.contributions)
    buffer = dict(ckpt.buffer)
    spills = list(ckpt.spills)
    stats.spill_files = len(spills)

    def flush():
        nonlocal buffer
        if not buffer:
            return
        spills.append(_spill(buffer, workdir, len(spills)))
        stats.spilled_entries += len(buffer)
        stats.spill_files = len(spills)
        buffer = {}

    try:
        for key in sorted(a.chunks):
            if key in ckpt.done:
                stats.chunks_skipped += 1
                continue
            for coord_a, value_a in a.chunks[key].items():
                join_key = tuple(coord_a[a_pos[l]] for l in shared)
                for coord_b, value_b in b_index.get(join_key, ()):
                    env = {}
                    for label, pos in a_pos.items():
                        env[label] = coord_a[pos]
                    for label, pos in b_pos.items():
                        if label not in env:
                            env[label] = coord_b[pos]
                    out_coord = tuple(env[l] for l in out_axes)
                    buffer[out_coord] = buffer.get(out_coord, 0) + value_a * value_b
                    stats.contributions += 1
                    if stats.contributions > budget.max_contributions:
                        raise BudgetExceeded(
                            f"contribution budget {budget.max_contributions} "
                            f"exceeded", stats=stats)
                    if len(buffer) >= budget.buffer_entries:
                        flush()
            ckpt.done.add(key)
            ckpt.buffer = buffer
            ckpt.spills = spills
            ckpt.contributions = stats.contributions
            ckpt.save(a, b)
            stats.chunks_processed += 1
            if chunk_hook is not None:
                chunk_hook(key)
    except BaseException:
        if owns_workdir:
            shutil.rmtree(workdir, ignore_errors=True)
        raise

    flush()
    merged = _merge(spills, buffer, workdir)
    result = SparseTensor(out_name or f"({a.name}*{b.name})", out_axes, out_shape,
                          entries=merged)
    result.dirty_chunks.clear()
    stats.output_nnz = result.nnz

    for name in spills:
        _silent_remove(os.path.join(workdir, name))
    _silent_remove(ckpt.path)
    if owns_workdir:
        shutil.rmtree(workdir, ignore_errors=True)
    return result, stats
