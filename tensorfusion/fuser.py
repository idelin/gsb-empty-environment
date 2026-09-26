"""Fusion of adjacent pure elementwise ops into single-pass loop kernels.

Safety rules (checked statically, with concrete evidence on violation):
  * never fuse across a reduction (accumulation order would change);
  * never fuse across an in-place mutation of a read variable
    (version dependency: ops before/after observe different versions);
  * never fuse a store whose output view aliases an input with a
    different index mapping (single-pass write would corrupt reads);
  * intermediates that escape a group are materialized (group splits).

compile_program is conservative: it only emits kernels that are provably
safe.  attempt_fuse is the strict entry point used for diagnostics: it
raises IllegalFusion carrying a list of Evidence objects.
"""

from __future__ import annotations

from dataclasses import dataclass, field

from .program import ELEMENTWISE
from .tensor import align_strides, broadcast_shape, default_strides, iter_indices


@dataclass
class Evidence:
    kind: str            # "alias" | "version" | "reduction"
    detail: str
    positions: tuple = ()

    def __str__(self):
        return f"[{self.kind}] {self.detail}"


class IllegalFusion(Exception):
    def __init__(self, evidences):
        self.evidences = list(evidences)
        super().__init__("illegal fusion:\n" + "\n".join(f"  {e}" for e in self.evidences))


@dataclass
class Kernel:
    """A fused single-pass elementwise kernel with an explicit loop plan."""
    steps: list           # elementwise Ops, evaluated in order per element
    shape: tuple          # broadcast output shape (the loop nest)
    in_strides: dict      # env var -> strides aligned to `shape`
    input_vars: list
    result_name: str
    store: object = None  # optional final store Op writing into an existing view

    @property
    def kind(self):
        return "kernel"


@dataclass
class ReduceUnit:
    op: object
    kind: str = "reduce_sum"


@dataclass
class MutateUnit:
    op: object
    kind: str = "mutate"


@dataclass
class StoreUnit:
    op: object
    kind: str = "store"


@dataclass
class FusedProgram:
    units: list

    def kernels(self):
        return [u for u in self.units if isinstance(u, Kernel)]


def alias_evidence(out_view, in_tensor, shape):
    """Return Evidence if writing `out_view` in a single pass over `shape`
    can corrupt reads of `in_tensor`; None if safe.

    Safe cases: disjoint storages, disjoint flat positions, or an identical
    elementwise mapping (each slot is read before it is written in the same
    loop iteration, e.g. in-place `x = f(x)`).
    """
    if out_view.storage.sid != in_tensor.storage.sid:
        return None
    in_strides = in_tensor.aligned_strides(shape)
    out_positions, in_positions = set(), set()
    identical = True
    for idx in iter_indices(shape):
        wp = out_view.offset + sum(i * s for i, s in zip(idx, out_view.strides))
        rp = in_tensor.offset + sum(i * s for i, s in zip(idx, in_strides))
        out_positions.add(wp)
        in_positions.add(rp)
        if wp != rp:
            identical = False
    common = sorted(out_positions & in_positions)
    if not common or identical:
        return None
    shown = common[:8]
    suffix = "..." if len(common) > 8 else ""
    return Evidence(
        "alias",
        f"output view (storage s{out_view.storage.sid}) overlaps input "
        f"'{in_tensor.name}' at flat positions {shown}{suffix} with a "
        f"different index mapping; a single-pass fused loop would overwrite "
        f"values that later iterations still need to read",
        tuple(common),
    )


def _can_extend(group, candidate, consumers):
    group_set = {id(op) for op in group}
    for op in group:
        for consumer in consumers.get(op.result, ()):
            if id(consumer) not in group_set and consumer is not candidate:
                return False
    return True


def _build_kernel(group, known, store=None):
    """known: var -> (shape, strides) for env tensors and materialized
    temporaries (the latter are dense contiguous buffers)."""
    produced = {op.result for op in group}
    input_vars = []
    for op in group:
        for a in op.args:
            if a not in produced and a not in input_vars:
                input_vars.append(a)
    shapes = [known[v][0] for v in input_vars]
    if store is not None:
        shapes.append(store.out.shape)
    shape = broadcast_shape(*shapes)
    in_strides = {v: align_strides(known[v][0], known[v][1], shape)
                  for v in input_vars}
    return Kernel(steps=list(group), shape=shape, in_strides=in_strides,
                  input_vars=input_vars, result_name=group[-1].result,
                  store=store)


def _store_safe(store_op, group, env, shape):
    if store_op.out.shape != shape:
        return False
    produced = {op.result for op in group}
    for op in group:
        for a in op.args:
            if a not in produced and a in env:
                if alias_evidence(store_op.out, env[a], shape) is not None:
                    return False
    return True


def compile_program(program, env):
    """Conservative fusion: emits only provably safe kernels."""
    consumers = program.consumers()
    known = {name: (t.shape, t.strides) for name, t in env.items()}
    units = []
    ops = program.ops
    i = 0
    while i < len(ops):
        op = ops[i]
        if op.kind in ELEMENTWISE:
            group = [op]
            i += 1
            while (i < len(ops) and ops[i].kind in ELEMENTWISE
                   and _can_extend(group, ops[i], consumers)):
                group.append(ops[i])
                i += 1
            store = None
            if (i < len(ops) and ops[i].kind == "store"
                    and ops[i].args[0] == group[-1].result):
                kernel = _build_kernel(group, known)
                if _store_safe(ops[i], group, env, kernel.shape):
                    store = ops[i]
                    i += 1
            kernel = _build_kernel(group, known, store)
            known[kernel.result_name] = (kernel.shape,
                                         default_strides(kernel.shape))
            units.append(kernel)
        elif op.kind == "reduce_sum":
            units.append(ReduceUnit(op))
            known[op.result] = ((), ())
            i += 1
        elif op.kind == "mutate":
            units.append(MutateUnit(op))
            i += 1
        elif op.kind == "store":
            units.append(StoreUnit(op))
            i += 1
        else:
            raise ValueError(f"unknown op kind {op.kind!r}")
    return FusedProgram(units)


def attempt_fuse(program, env):
    """Try to fuse the whole program into one kernel; raise IllegalFusion
    with concrete dependency evidence when that is not legal."""
    evidences = []
    ew_ops = [op for op in program.ops if op.kind in ELEMENTWISE]
    for idx, op in enumerate(program.ops):
        if op.kind == "reduce_sum":
            before = any(o.kind in ELEMENTWISE for o in program.ops[:idx])
            after = any(o.kind in ELEMENTWISE for o in program.ops[idx + 1:])
            if before and after:
                evidences.append(Evidence(
                    "reduction",
                    f"'{op.label()}' is a reduction barrier: fusing "
                    f"elementwise ops across it would reorder accumulation "
                    f"and change floating-point results"))
        elif op.kind == "mutate":
            before = [o.label() for o in program.ops[:idx]
                      if o.kind in ELEMENTWISE and op.target in o.args]
            after = [o.label() for o in program.ops[idx + 1:]
                     if o.kind in ELEMENTWISE and op.target in o.args]
            if before and after:
                version = env[op.target].version
                evidences.append(Evidence(
                    "version",
                    f"in-place mutation '{op.label()}' bumps '{op.target}' "
                    f"from version {version} to {version + 1}: "
                    f"'{before[-1]}' reads the old version while "
                    f"'{after[0]}' must observe the new one; a fused kernel "
                    f"would read a single version"))
        elif op.kind == "store":
            produced = {o.result for o in ew_ops}
            read_vars = []
            for o in ew_ops:
                for a in o.args:
                    if a not in produced and a in env and a not in read_vars:
                        read_vars.append(a)
            shapes = [env[v].shape for v in read_vars] + [op.out.shape]
            try:
                shape = broadcast_shape(*shapes)
            except ValueError:
                shape = op.out.shape
            for v in read_vars:
                ev = alias_evidence(op.out, env[v], shape)
                if ev is not None:
                    evidences.append(ev)
    if evidences:
        raise IllegalFusion(evidences)
    return compile_program(program, env)


def format_plan(kernel):
    """Render the explicit loop plan of a fused kernel."""
    lines = [f"kernel {kernel.result_name}: shape={kernel.shape}"]
    for d, size in enumerate(kernel.shape):
        lines.append("  " * (d + 1) + f"for i{d} in range({size}):")
    body = "  " * (len(kernel.shape) + 1)

    def ref(var):
        terms = [f"{s}*i{d}" for d, s in enumerate(kernel.in_strides[var]) if s]
        return f"{var}[{' + '.join(terms) if terms else '0'}]"

    for step in kernel.steps:
        args = ", ".join(ref(a) if a in kernel.in_strides else a
                         for a in step.args)
        lines.append(f"{body}{step.result} = {step.kind}({args})")
    if kernel.store is not None:
        name = getattr(kernel.store.out, "name", None) or "out"
        lines.append(f"{body}store -> {name}")
    else:
        lines.append(f"{body}write {kernel.result_name}")
    return "\n".join(lines)
