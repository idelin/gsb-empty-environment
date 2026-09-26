"""Two differential interpreters over the same Program.

run_unfused: op-by-op execution, one temporary buffer per op.
run_fused  : executes fused single-pass kernels from fuser.compile_program.

Both count element reads / writes / allocated buffers.  Both raise
ElementwiseError that pinpoints the *original* expression, the logical
output index and the operand indices.  The fused kernel preserves the
unfused error semantics exactly: on an arithmetic failure it falls back to
a diagnostic replay in original op order (elementwise short-circuit per
op), so the reported location is identical to the unfused interpreter.
"""

from __future__ import annotations

from dataclasses import dataclass

from .fuser import Kernel, MutateUnit, ReduceUnit, StoreUnit, compile_program
from .program import ELEMENTWISE
from .tensor import (Tensor, align_strides, broadcast_shape, default_strides,
                     iter_indices, project_index)


@dataclass
class Stats:
    buffers_allocated: int = 0
    element_reads: int = 0
    element_writes: int = 0


class ElementwiseError(Exception):
    """Arithmetic failure located back to the original expression."""

    def __init__(self, op, out_index, operand_indices, cause):
        self.op_label = op.label()
        self.op_kind = op.kind
        self.out_index = tuple(out_index)
        self.operand_indices = {k: tuple(v) for k, v in operand_indices.items()}
        detail = ", ".join(f"{name}{idx}" for name, idx in self.operand_indices.items())
        super().__init__(
            f"{cause} in expression '{self.op_label}' at output index "
            f"{self.out_index}; operand indices: {detail}")

    def key(self):
        return (self.op_label, self.out_index,
                tuple(sorted(self.operand_indices.items())))


def apply_op(kind, vals):
    if kind == "add":
        return vals[0] + vals[1]
    if kind == "sub":
        return vals[0] - vals[1]
    if kind == "mul":
        return vals[0] * vals[1]
    if kind == "div":
        return vals[0] / vals[1]
    if kind == "neg":
        return -vals[0]
    raise ValueError(f"unknown elementwise op {kind!r}")


def _exec_elementwise_unfused(op, values, stats):
    args = [values[a] for a in op.args]
    shape = broadcast_shape(*(t.shape for t in args))
    out = Tensor.full(shape, 0.0, name=op.result)
    stats.buffers_allocated += 1
    strides = [t.aligned_strides(shape) for t in args]
    for idx in iter_indices(shape):
        vals = []
        for tensor, st in zip(args, strides):
            stats.element_reads += 1
            vals.append(tensor.get(project_index(idx, tensor.shape, st)))
        try:
            result = apply_op(op.kind, vals)
        except ZeroDivisionError as exc:
            operand_indices = {
                name: project_index(idx, tensor.shape, st)
                for name, tensor, st in zip(op.args, args, strides)
            }
            raise ElementwiseError(op, idx, operand_indices, exc) from exc
        stats.element_writes += 1
        out._write(idx, result)
    values[op.result] = out


def _exec_reduce(op, values, stats, order="forward"):
    tensor = values[op.args[0]]
    indices = list(tensor.indices())
    if order == "reverse":
        indices.reverse()
    acc = 0.0
    for idx in indices:
        stats.element_reads += 1
        acc += tensor.get(idx)
    out = Tensor.full((), 0.0, name=op.result)
    stats.buffers_allocated += 1
    stats.element_writes += 1
    out._write((), acc)
    values[op.result] = out


def _exec_mutate(op, values, stats):
    target = values[op.target]
    arg = values[op.args[1]]
    strides = arg.aligned_strides(target.shape)
    for idx in iter_indices(target.shape):
        stats.element_reads += 2
        stats.element_writes += 1
        target._write(idx, target.get(idx) + arg.get(project_index(idx, arg.shape, strides)))
    target.storage.version += 1


def _exec_store(op, values, stats):
    src = values[op.args[0]].broadcast_to(op.out.shape)
    for idx in op.out.indices():
        stats.element_reads += 1
        stats.element_writes += 1
        op.out._write(idx, src.get(idx))
    op.out.storage.version += 1


def run_unfused(program, env):
    values = dict(env)
    stats = Stats()
    for op in program.ops:
        if op.kind in ELEMENTWISE:
            _exec_elementwise_unfused(op, values, stats)
        elif op.kind == "reduce_sum":
            _exec_reduce(op, values, stats)
        elif op.kind == "mutate":
            _exec_mutate(op, values, stats)
        elif op.kind == "store":
            _exec_store(op, values, stats)
        else:
            raise ValueError(f"unknown op kind {op.kind!r}")
    return values, stats


def _diagnose(kernel, input_tensors):
    """Replay the kernel op-by-op in original order (elementwise
    short-circuit) to find the canonical first failure, exactly as the
    unfused interpreter would report it (same per-op broadcast shapes and
    traversal order).  Only runs on the error path."""
    produced = {}
    for step in kernel.steps:
        arg_shapes = [produced[a][1] if a in produced else input_tensors[a].shape
                      for a in step.args]
        step_shape = broadcast_shape(*arg_shapes)
        buf = {}
        for idx in iter_indices(step_shape):
            vals = []
            operand_indices = {}
            for a in step.args:
                if a in produced:
                    prev_buf, prev_shape = produced[a]
                    st = align_strides(prev_shape, default_strides(prev_shape),
                                       step_shape)
                    proj = project_index(idx, prev_shape, st)
                    vals.append(prev_buf[proj])
                    operand_indices[a] = proj
                else:
                    st = align_strides(input_tensors[a].shape,
                                       default_strides(input_tensors[a].shape),
                                       step_shape)
                    proj = project_index(idx, input_tensors[a].shape, st)
                    vals.append(input_tensors[a].get(proj))
                    operand_indices[a] = proj
            try:
                buf[idx] = apply_op(step.kind, vals)
            except ZeroDivisionError as exc:
                return ElementwiseError(step, idx, operand_indices, exc)
        produced[step.result] = (buf, step_shape)
    raise AssertionError("diagnostic replay did not reproduce the failure")


def _exec_kernel(kernel, values, stats=None, trace=False):
    shape = kernel.shape
    if kernel.store is not None:
        out = kernel.store.out
    else:
        out = Tensor.full(shape, 0.0, name=kernel.result_name)
        if stats is not None:
            stats.buffers_allocated += 1
    input_tensors = {v: values[v] for v in kernel.input_vars}
    offsets = {v: input_tensors[v].offset for v in kernel.input_vars}
    trace_bufs = ({step.result: Tensor.full(shape, 0.0, name=step.result)
                   for step in kernel.steps} if trace else None)
    for idx in iter_indices(shape):
        temps = {}
        for step in kernel.steps:
            vals = []
            for a in step.args:
                if a in temps:
                    vals.append(temps[a])
                else:
                    if stats is not None:
                        stats.element_reads += 1
                    tensor = input_tensors[a]
                    pos = offsets[a] + sum(i * s for i, s in zip(idx, kernel.in_strides[a]))
                    vals.append(tensor.storage.data[pos])
            try:
                temps[step.result] = apply_op(step.kind, vals)
            except ZeroDivisionError as exc:
                raise _diagnose(kernel, input_tensors) from exc
            if trace:
                trace_bufs[step.result]._write(idx, temps[step.result])
        if stats is not None:
            stats.element_writes += 1
        out._write(idx, temps[kernel.result_name])
    values[kernel.result_name] = out
    if kernel.store is not None:
        out.storage.version += 1
    if trace:
        for step in kernel.steps[:-1]:
            values[step.result] = trace_bufs[step.result]
    return out


def run_fused(program, env, trace=False):
    """Execute the fused program.  With trace=True the fused intermediates
    are materialized as well (verification instrumentation only; stats are
    not collected in that mode)."""
    fused = compile_program(program, env)
    values = dict(env)
    stats = Stats()
    for unit in fused.units:
        if isinstance(unit, Kernel):
            _exec_kernel(unit, values, None if trace else stats, trace)
        elif isinstance(unit, ReduceUnit):
            _exec_reduce(unit.op, values, stats)
        elif isinstance(unit, MutateUnit):
            _exec_mutate(unit.op, values, stats)
        elif isinstance(unit, StoreUnit):
            _exec_store(unit.op, values, stats)
        else:
            raise ValueError(f"unknown unit {unit!r}")
    return values, stats
