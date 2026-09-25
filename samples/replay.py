"""Replayable end-to-end sample.

Deterministic scenario covering: plan enumeration, cheapest-order pick,
chunked execution, delta apply/retract with cache reuse, checkpoint
interrupt + resume with idempotent delta, spill-to-disk, and budget abort.
Run as:  python -m samples.replay
"""
from __future__ import annotations

import os
import shutil
import tempfile

from axcon import (Budget, ChunkIndexCache, Executor, SparseTensor,
                   SubexpressionCache, dense_contract, enumerate_plans)


def _fmt_entries(entries):
    parts = []
    for coord, value in sorted(entries.items()):
        shown = int(value) if float(value).is_integer() else round(value, 6)
        parts.append(f"{coord}:{shown}")
    return " ".join(parts) if parts else "<empty>"


def build_tensors():
    a = SparseTensor("A", ("i", "j"), (3, 4),
                     {(0, 0): 1.0, (0, 2): 2.0, (1, 1): 3.0, (2, 3): 4.0})
    b = SparseTensor("B", ("j", "k"), (4, 2),
                     {(0, 0): 5.0, (1, 1): 6.0, (2, 0): 7.0, (3, 1): 8.0})
    c = SparseTensor("C", ("k", "l"), (2, 3),
                     {(0, 0): 1.0, (0, 2): 2.0, (1, 1): 3.0})
    return a, b, c


def replay(workdir):
    log = []
    a, b, c = build_tensors()
    tensors = [a, b, c]

    plans = enumerate_plans(tensors, ("i", "l"))
    for n, p in enumerate(plans):
        first = p.steps[0]
        log.append(f"plan{n}: est_total={p.est_intermediate_total:.3f} "
                   f"first_contract={first.contracted} "
                   f"est_first={first.est_nnz:.3f}")
    plan = plans[0]
    log.append(f"chosen: est_total={plan.est_intermediate_total:.3f} "
               f"axis_map={plan.output.labels}->{plan.output.shape}")

    result = Executor(Budget(chunk_size=2),
                      workdir=os.path.join(workdir, "run1")).execute(plan, tensors)
    oracle = dense_contract(tensors, ("i", "l"))
    assert result.completed and result.tensor.entries == oracle
    log.append(f"run1: completed={result.completed} "
               f"entries={_fmt_entries(result.tensor.entries)}")
    log.append(f"run1: chunks={result.stats['chunks']} "
               f"spilled={result.stats['spilled_bytes']} "
               f"actual_intermediate={result.stats['actual_intermediate_total']}")

    # delta + version-bound cache reuse (delta on C: step0 = A*B stays cached)
    index_cache = ChunkIndexCache(num_buckets=2)
    subexpr = SubexpressionCache()
    cached_exec = Executor(Budget(chunk_size=2),
                           workdir=os.path.join(workdir, "run2"),
                           index_cache=index_cache, subexpr_cache=subexpr)
    cached_exec.execute(plan, tensors)
    c.apply_delta("d1", {(0, 1): 2.0})
    index_cache.invalidate_delta(c, {(0, 1): 2.0})
    r3 = cached_exec.execute(plan, tensors)
    assert r3.tensor.entries == dense_contract(tensors, ("i", "l"))
    log.append(f"delta d1: cache_hits={subexpr.hits} cache_misses={subexpr.misses} "
               f"entries={_fmt_entries(r3.tensor.entries)}")
    c.retract_delta("d1")
    index_cache.invalidate_delta(c, {(0, 1): 2.0})
    r4 = cached_exec.execute(plan, tensors)
    assert r4.tensor.entries == oracle
    log.append(f"retract d1: restored={r4.tensor.entries == oracle}")

    # checkpoint interrupt + idempotent delta replay
    b.apply_delta("d2", {(3, 0): 1.0})
    crashy = Executor(Budget(chunk_size=1, interrupt_after_chunks=1),
                      workdir=os.path.join(workdir, "run3"))
    r5 = crashy.execute(plan, tensors)
    log.append(f"crash: completed={r5.completed} reason={r5.stats['abort_reason']}")
    reapplied = b.apply_delta("d2", {(3, 0): 1.0})
    log.append(f"delta d2 reapply: {reapplied} version={b.version}")
    resumer = Executor(Budget(chunk_size=1),
                       workdir=os.path.join(workdir, "run3"))
    r6 = resumer.execute(plan, tensors, resume=True)
    oracle6 = dense_contract(tensors, ("i", "l"))
    assert r6.completed and r6.tensor.entries == oracle6
    log.append(f"resume: completed={r6.completed} "
               f"entries={_fmt_entries(r6.tensor.entries)}")

    # memory pressure -> spill to disk
    spilly = Executor(Budget(chunk_size=1, max_entries_in_memory=1),
                      workdir=os.path.join(workdir, "run4"))
    r7 = spilly.execute(plan, tensors)
    assert r7.completed and r7.tensor.entries == oracle6
    log.append(f"spill: completed={r7.completed} "
               f"spilled_bytes={r7.stats['spilled_bytes']}")

    # budget abort -> explicitly marked incomplete
    stingy = Executor(Budget(chunk_size=1, max_chunks=1),
                      workdir=os.path.join(workdir, "run5"))
    r8 = stingy.execute(plan, tensors)
    log.append(f"budget: completed={r8.completed} "
               f"reason={r8.stats['abort_reason']}")
    return log


def main():
    workdir = tempfile.mkdtemp(prefix="axcon-replay-")
    try:
        for line in replay(workdir):
            print(line)
    finally:
        shutil.rmtree(workdir, ignore_errors=True)


if __name__ == "__main__":
    main()
