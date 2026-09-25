"""Plan executor: chunked hash joins, spill-to-disk, checkpoints, budgets.

The executor never materializes a dense cartesian product: each step is a
hash join over sorted entry chunks of the left operand probing an index on
the right operand. Memory pressure spills partial accumulators to disk; every
chunk boundary is checkpointed so an interrupted run can resume without
re-applying work.
"""
from __future__ import annotations

import json
import os
import pickle
import tempfile
from dataclasses import dataclass

from .cache import build_join_index
from .tensor import SparseTensor


class ExecutionInterrupted(Exception):
    """Simulated crash after a checkpoint; execution can be resumed."""


class BudgetExceeded(Exception):
    """Raised internally when the chunk budget is exhausted."""
    pass


@dataclass
class Budget:
    max_entries_in_memory: int = 100_000
    chunk_size: int = 256
    max_chunks: int | None = None
    interrupt_after_chunks: int | None = None  # test hook: simulate a crash


@dataclass
class ExecutionResult:
    tensor: SparseTensor | None
    completed: bool
    plan: object
    stats: dict


class Executor:
    def __init__(self, budget=None, workdir=None, index_cache=None,
                 subexpr_cache=None):
        self.budget = budget or Budget()
        self.workdir = workdir or tempfile.mkdtemp(prefix="axcon-")
        os.makedirs(self.workdir, exist_ok=True)
        self.index_cache = index_cache
        self.subexpr_cache = subexpr_cache
        self._spilled_bytes = 0
        self._spill_seq = 0

    # ------------------------------------------------------------------ API
    def execute(self, plan, tensors, resume=False):
        names = [t.name for t in tensors]
        if len(set(names)) != len(names):
            raise ValueError(f"tensor names must be unique: {names!r}")
        self._spilled_bytes = 0
        self._spill_seq = 0
        for step in plan.steps:
            step.chunks = 0
            step.spilled_bytes = 0
            step.actual_nnz = None
        state = self._load_state(resume, tensors)
        intermediates = {}
        for i in range(state["steps_done"]):
            intermediates[i] = self._load_step_result(i)
        sigs, vers = self._signatures(plan, tensors)
        abort_reason = None
        try:
            for si, step in enumerate(plan.steps):
                if si < state["steps_done"]:
                    continue
                cached = self._subexpr_lookup(sigs[si], vers[si])
                if cached is not None:
                    intermediates[si] = cached
                    step.actual_nnz = cached.nnz
                    self._persist_step(si, cached)
                    state.update(steps_done=si + 1, step=-1, chunk=0,
                                 spill_parts=[])
                    self._save_state(state)
                    continue
                a = self._operand(step.left, tensors, intermediates)
                b = self._operand(step.right, tensors, intermediates)
                if si == state["step"] and state["chunk"]:
                    acc = self._load_acc()
                    start_chunk = state["chunk"]
                    spill_parts = list(state["spill_parts"])
                else:
                    acc, start_chunk, spill_parts = {}, 0, []
                acc, spill_parts = self._run_step(
                    step, a, b, acc, start_chunk, spill_parts, si, state)
                entries = self._merge_spills(acc, spill_parts)
                result = SparseTensor(f"step{si}", step.out_labels,
                                      step.out_shape, entries)
                step.actual_nnz = result.nnz
                intermediates[si] = result
                if self.subexpr_cache is not None:
                    self.subexpr_cache.put(sigs[si], vers[si], result)
                self._persist_step(si, result)
                state.update(steps_done=si + 1, step=-1, chunk=0,
                             spill_parts=[])
                self._save_state(state)
                self._clear_acc()
        except ExecutionInterrupted:
            abort_reason = "interrupted"
        except BudgetExceeded:
            abort_reason = "budget-exceeded"
        if abort_reason is None:
            final = self._finalize(
                self._final_operand(plan, tensors, intermediates), plan)
            plan.stats.update(
                completed=True,
                chunks=state["chunks_done"],
                spilled_bytes=self._spilled_bytes,
                actual_intermediate_total=sum(s.actual_nnz or 0
                                           for s in plan.steps))
            self._save_state(self._fresh_state(tensors))
            return ExecutionResult(final, True, plan, dict(plan.stats))
        plan.stats.update(completed=False, abort_reason=abort_reason,
                       chunks=state["chunks_done"],
                       spilled_bytes=self._spilled_bytes)
        return ExecutionResult(None, False, plan, dict(plan.stats))

    # -------------------------------------------------------------- one step
    def _run_step(self, step, a, b, acc, start_chunk, spill_parts, si, state):
        key_labels = [l for l in a.labels if l in b.labels]
        index_b = self._join_index(b, key_labels)
        pos_a = {lab: p for p, lab in enumerate(a.labels)}
        pos_b = {lab: p for p, lab in enumerate(b.labels)}
        entries = sorted(a.entries.items())
        chunks = [entries[k:k + self.budget.chunk_size]
                  for k in range(0, len(entries), self.budget.chunk_size)]
        for ci in range(start_chunk, len(chunks)):
            for coord_a, value_a in chunks[ci]:
                key = tuple(coord_a[pos_a[l]] for l in key_labels)
                for coord_b, value_b in index_b.get(key, ()):
                    assign = {}
                    for lab, p in pos_a.items():
                        assign[lab] = coord_a[p]
                    for lab, p in pos_b.items():
                        assign[lab] = coord_b[p]
                    out_coord = tuple(assign[l] for l in step.out_labels)
                    merged = acc.get(out_coord, 0.0) + value_a * value_b
                    if merged == 0:
                        acc.pop(out_coord, None)
                    else:
                        acc[out_coord] = merged
            state["chunks_done"] += 1
            step.chunks += 1
            if len(acc) > self.budget.max_entries_in_memory:
                spill_parts.append(self._spill(acc, step))
                acc = {}
            state.update(step=si, chunk=ci + 1,
                         spill_parts=list(spill_parts))
            self._save_state(state, acc)
            if (self.budget.interrupt_after_chunks is not None
                    and state["chunks_done"] >= self.budget.interrupt_after_chunks):
                raise ExecutionInterrupted(
                    f"interrupted after {state['chunks_done']} chunks")
            if (self.budget.max_chunks is not None
                    and state["chunks_done"] >= self.budget.max_chunks):
                raise BudgetExceeded(
                    f"chunk budget {self.budget.max_chunks} exhausted")
        return acc, spill_parts

    def _join_index(self, tensor, key_labels):
        if self.index_cache is not None:
            return self.index_cache.get_join_index(tensor, key_labels)
        return build_join_index(tensor, key_labels)

    # ----------------------------------------------------------------- spill
    def _spill(self, acc, step):
        path = os.path.join(self.workdir, f"spill-{self._spill_seq}.pkl")
        self._spill_seq += 1
        with open(path, "wb") as fh:
            pickle.dump(acc, fh)
        size = os.path.getsize(path)
        self._spilled_bytes += size
        step.spilled_bytes += size
        return path

    def _merge_spills(self, acc, parts):
        # Spill parts are merged back one file at a time. The final result
        # itself must fit in memory (a production system would use an
        # external sort-merge here); the budget bounds the join phase.
        for path in parts:
            with open(path, "rb") as fh:
                part = pickle.load(fh)
            for coord, value in part.items():
                merged = acc.get(coord, 0.0) + value
                if merged == 0:
                    acc.pop(coord, None)
                else:
                    acc[coord] = merged
            os.remove(path)
        return acc

    # ------------------------------------------------------------- finalizing
    @staticmethod
    def _operand(ref, tensors, intermediates):
        return tensors[ref.index] if ref.kind == "input" \
            else intermediates[ref.index]

    def _final_operand(self, plan, tensors, intermediates):
        if plan.steps:
            return intermediates[len(plan.steps) - 1]
        return tensors[0]

    def _finalize(self, tensor, plan):
        out_labels = plan.output.labels
        if tensor.labels == out_labels:
            tensor.name = "result"
            return tensor
        out = {}
        for coord, value in tensor.entries.items():
            assign = dict(zip(tensor.labels, coord))
            out_coord = tuple(assign[l] for l in out_labels)
            out[out_coord] = out.get(out_coord, 0.0) + value
        out = {c: v for c, v in out.items() if v != 0}
        return SparseTensor("result", out_labels, plan.output.shape, out)

    # ----------------------------------------------------------- signatures/cache
    def _subexpr_lookup(self, sig, vers):
        if self.subexpr_cache is None:
            return None
        return self.subexpr_cache.get(sig, vers)

    @staticmethod
    def _signatures(plan, tensors):
        sigs, vers = [], []
        for step in plan.steps:
            def resolve(ref):
                if ref.kind == "input":
                    tensor = tensors[ref.index]
                    return ("in", tensor.name), {tensor.name: tensor.version}
                return sigs[ref.index], vers[ref.index]
            sig_left, ver_left = resolve(step.left)
            sig_right, ver_right = resolve(step.right)
            sigs.append(("step", sig_left, sig_right, step.contracted,
                          step.out_labels))
            merged = dict(ver_left)
            merged.update(ver_right)
            vers.append(merged)
        return sigs, vers

    # ------------------------------------------------------------ persistence
    @staticmethod
    def _versions(tensors):
        return {t.name: t.version for t in tensors}

    def _fresh_state(self, tensors):
        return {"steps_done": 0, "step": -1, "chunk": 0, "chunks_done": 0,
                "spill_parts": [], "versions": self._versions(tensors)}

    def _state_path(self):
        return os.path.join(self.workdir, "checkpoint.json")

    def _load_state(self, resume, tensors):
        fresh = self._fresh_state(tensors)
        if not resume or not os.path.exists(self._state_path()):
            return fresh
        with open(self._state_path()) as fh:
            state = json.load(fh)
        if state.get("versions") != fresh["versions"]:
            return fresh  # inputs changed: invalidate the checkpoint
        return state

    def _save_state(self, state, acc=None):
        tmp = self._state_path() + ".tmp"
        with open(tmp, "w") as fh:
            json.dump(state, fh)
        os.replace(tmp, self._state_path())
        if acc is not None:
            with open(self._acc_path(), "wb") as fh:
                pickle.dump(acc, fh)

    def _acc_path(self):
        return os.path.join(self.workdir, "acc.pkl")

    def _load_acc(self):
        if not os.path.exists(self._acc_path()):
            return {}
        with open(self._acc_path(), "rb") as fh:
            return pickle.load(fh)

    def _clear_acc(self):
        if os.path.exists(self._acc_path()):
            os.remove(self._acc_path())

    def _step_path(self, i):
        return os.path.join(self.workdir, f"step-{i}.pkl")

    def _persist_step(self, i, tensor):
        with open(self._step_path(i), "wb") as fh:
            pickle.dump((tensor.labels, tensor.shape, tensor.entries), fh)

    def _load_step_result(self, i):
        with open(self._step_path(i), "rb") as fh:
            labels, shape, entries = pickle.load(fh)
        return SparseTensor(f"step{i}", labels, shape, entries)
