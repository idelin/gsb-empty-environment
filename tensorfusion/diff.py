"""Exhaustive small-shape differential testing between the two interpreters.

Enumerates broadcast-compatible shape triples, fills tensors with boundary
values (0, -0.0, subnormals, huge, inf, nan), runs a set of elementwise
chains through both interpreters and checks:
  * results are BITWISE identical (pure elementwise fusion must not change
    a single bit);
  * division-by-zero raises ElementwiseError with identical original
    expression, output index and operand indices;
  * the fused interpreter never allocates more buffers.
"""

from __future__ import annotations

import itertools
from dataclasses import dataclass

from .interp import ElementwiseError, run_fused, run_unfused
from .program import Program
from .tensor import Tensor, broadcast_shape, numel
from .verify import Verdict, compare_tensors

SHAPES = [(), (1,), (2,), (3,), (2, 1), (1, 2), (2, 3)]

POOL = [0.0, -0.0, 1.0, -1.0, 2.5, -3.75, 1e-300, -1e-300, 1e300,
        float("inf"), float("-inf"), float("nan")]


def _fill(shape, offset):
    return [POOL[(offset + k) % len(POOL)] for k in range(numel(shape))]


def _programs():
    def chain1(p):
        t1 = p.add("a", "b")
        return p.mul(t1, "c")

    def chain2(p):
        t1 = p.sub("a", "b")
        t2 = p.div(t1, "c")
        return p.neg(t2)

    def chain3(p):
        t1 = p.mul("a", "a")
        t2 = p.add(t1, "b")
        return p.div(t2, "c")

    def chain4(p):
        t1 = p.div("a", "c")
        return p.add(t1, "b")

    return [chain1, chain2, chain3, chain4]


@dataclass
class DiffSummary:
    cases: int = 0
    error_cases: int = 0
    bitwise_ok: int = 0
    buffers_unfused: int = 0
    buffers_fused: int = 0
    reads_unfused: int = 0
    reads_fused: int = 0
    writes_unfused: int = 0
    writes_fused: int = 0
    mismatches: list = None

    def __post_init__(self):
        if self.mismatches is None:
            self.mismatches = []

    def describe(self):
        lines = [
            f"cases={self.cases} bitwise_ok={self.bitwise_ok} "
            f"error_cases={self.error_cases} mismatches={len(self.mismatches)}",
            f"buffers: unfused={self.buffers_unfused} fused={self.buffers_fused} "
            f"(saved {self.buffers_unfused - self.buffers_fused})",
            f"reads: unfused={self.reads_unfused} fused={self.reads_fused}; "
            f"writes: unfused={self.writes_unfused} fused={self.writes_fused}",
        ]
        return "\n".join(lines)


def run_exhaustive():
    summary = DiffSummary()
    fills = [(0, 4, 8), (5, 1, 9), (2, 7, 3)]
    for chain in _programs():
        program = Program(inputs=["a", "b", "c"])
        result = chain(program)
        for shape_a, shape_b, shape_c in itertools.product(SHAPES, repeat=3):
            try:
                broadcast_shape(shape_a, shape_b, shape_c)
            except ValueError:
                continue
            for off_a, off_b, off_c in fills:
                env = {
                    "a": Tensor.from_values(_fill(shape_a, off_a), shape_a, name="a"),
                    "b": Tensor.from_values(_fill(shape_b, off_b), shape_b, name="b"),
                    "c": Tensor.from_values(_fill(shape_c, off_c), shape_c, name="c"),
                }
                summary.cases += 1
                tag = f"{chain.__name__} {shape_a}/{shape_b}/{shape_c} offs=({off_a},{off_b},{off_c})"
                err_u = err_f = None
                try:
                    values_u, stats_u = run_unfused(program, env)
                except ElementwiseError as exc:
                    err_u = exc
                try:
                    values_f, stats_f = run_fused(program, env)
                except ElementwiseError as exc:
                    err_f = exc
                if (err_u is None) != (err_f is None):
                    summary.mismatches.append(f"{tag}: error presence differs")
                    continue
                if err_u is not None:
                    summary.error_cases += 1
                    if err_u.key() != err_f.key():
                        summary.mismatches.append(
                            f"{tag}: error location differs: "
                            f"{err_u.key()} vs {err_f.key()}")
                    continue
                summary.buffers_unfused += stats_u.buffers_allocated
                summary.buffers_fused += stats_f.buffers_allocated
                summary.reads_unfused += stats_u.element_reads
                summary.reads_fused += stats_f.element_reads
                summary.writes_unfused += stats_u.element_writes
                summary.writes_fused += stats_f.element_writes
                if stats_f.buffers_allocated > stats_u.buffers_allocated:
                    summary.mismatches.append(f"{tag}: fused allocated more buffers")
                cmp = compare_tensors(values_u[result], values_f[result])
                if cmp.verdict is Verdict.BITWISE:
                    summary.bitwise_ok += 1
                else:
                    summary.mismatches.append(f"{tag}: not bitwise identical: {cmp}")
    return summary
