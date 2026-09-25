"""Replayable end-to-end scenario for the axcon planner.

The scenario is a fixed list of operations replayed by ``run_scenario``; the
transcript is deterministic (fixed seeds) so it can be diffed across runs.

    python3 examples/replay_demo.py
"""

import os
import shutil
import sys
import tempfile

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

from axcon import (Budget, ContractionCache, Delta, SparseTensor,
                   contract_many, contract_pair, dense_contract)


def build_inputs():
    a = SparseTensor("A", ("i", "k"), {"i": 2, "k": 6},
                     entries={(0, 0): 1, (0, 3): 2, (1, 1): 3, (1, 4): 1})
    b = SparseTensor("B", ("k", "j"), {"k": 6, "j": 2},
                     entries={(0, 0): 2, (1, 1): 1, (3, 1): 4, (4, 0): 2})
    c = SparseTensor("C", ("j", "m"), {"j": 2, "m": 5},
                     entries={(0, 0): 1, (0, 2): 3, (1, 1): 2, (1, 4): 1})
    return a, b, c


def run_scenario(workdir=None):
    log = []
    own = workdir is None
    workdir = workdir or tempfile.mkdtemp(prefix="axcon_demo_")
    a, b, c = build_inputs()
    cache = ContractionCache()

    log.append("== 1. plan + contract A(i,k) B(k,j) C(j,m) -> (i,m) ==")
    res = contract_many([a, b, c], ("i", "m"), cache=cache,
                        workdir=os.path.join(workdir, "main"))
    log.append("chosen plan:\n" + res.plan.describe())
    log.append(f"axis mapping: {res.axis_mapping}")
    log.append(f"result nnz={res.tensor.nnz} "
               f"est_intermediate={res.stats.est_intermediate_nnz} "
               f"actual_intermediate={res.stats.actual_intermediate_nnz}")
    oracle = dense_contract([a, b, c], ("i", "m"))
    assert dict(res.tensor.iter_entries()) == oracle
    log.append("matches dense oracle: True")

    log.append("== 2. cached re-run, then delta invalidation ==")
    again = contract_many([a, b, c], ("i", "m"), cache=cache,
                          workdir=os.path.join(workdir, "main2"))
    log.append(f"cache hits on replay: {again.stats.cache_hits}")
    a.clear_dirty()
    a.apply_delta(Delta("demo-delta-1", (0, 3), 5))
    log.append(f"delta touched chunks: {sorted(a.dirty_chunks)} "
               f"(version now {a.version})")
    after = contract_many([a, b, c], ("i", "m"), cache=cache,
                          workdir=os.path.join(workdir, "main3"))
    assert dict(after.tensor.iter_entries()) == dense_contract([a, b, c], ("i", "m"))
    log.append(f"re-planned after delta, cache hits: {after.stats.cache_hits}")
    assert not a.apply_delta(Delta("demo-delta-1", (0, 3), 5))
    log.append("re-applying same delta id: no-op (idempotent)")

    log.append("== 3. spill + simulated crash + checkpoint resume ==")
    budget = Budget(buffer_entries=2)
    step_dir = os.path.join(workdir, "spill_step")
    calls = {"n": 0}

    def crash_hook(_key):
        calls["n"] += 1
        if calls["n"] == 2:
            raise RuntimeError("simulated crash")

    try:
        contract_pair(b, c, ("k", "m"), budget=budget, workdir=step_dir,
                      chunk_hook=crash_hook)
    except RuntimeError as exc:
        log.append(f"crashed as planned: {exc}")
    partial, stats = contract_pair(b, c, ("k", "m"), budget=budget,
                                   workdir=step_dir)
    assert dict(partial.iter_entries()) == dense_contract([b, c], ("k", "m"))
    log.append(f"resumed={stats.resumed} chunks_skipped={stats.chunks_skipped} "
               f"spill_files={stats.spill_files} result matches oracle: True")

    log.append("== 4. budget abort marks the plan incomplete ==")
    tight = Budget(max_contributions=2)
    aborted = contract_many([a, b, c], ("i", "m"), budget=tight,
                            workdir=os.path.join(workdir, "tight"))
    log.append(f"complete={aborted.complete} error={aborted.error!r}")
    assert not aborted.complete and aborted.tensor is None

    if own:
        shutil.rmtree(workdir, ignore_errors=True)
    log.append("SCENARIO OK")
    return log


if __name__ == "__main__":
    for line in run_scenario():
        print(line)
