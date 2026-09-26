"""End-to-end demo of the fusion engine.  Run: python3.11 examples/run_demo.py"""

import os
import sys

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

from tensorfusion import (IllegalFusion, Program, Tensor, attempt_fuse,
                          compile_program, format_plan, run_exhaustive,
                          run_fused, run_unfused, verify_gradients)
from tensorfusion.interp import ElementwiseError, _exec_reduce, Stats
from tensorfusion.verify import Verdict, compare_tensors


def section(title):
    print(f"\n=== {title} ===")


def demo_fusion_and_stats():
    section("1. Fusion: explicit loop plan + buffer/read/write stats")
    program = Program(inputs=["a", "b", "c"])
    t1 = program.add("a", "b")
    t2 = program.mul(t1, "c")
    t3 = program.div(t2, "b")
    out = program.reduce_sum(t3)
    print("program:")
    print(program.describe())
    env = {
        "a": Tensor.from_values([1, 2, 3, 4, 5, 6], (2, 3), name="a"),
        "b": Tensor.from_values([0.5, 1.5, 2.0], (3,), name="b"),
        "c": Tensor.from_values([2, 3, 4, 5, 6, 7], (2, 3), name="c"),
    }
    fused = compile_program(program, env)
    for kernel in fused.kernels():
        print(format_plan(kernel))
    values_u, stats_u = run_unfused(program, env)
    values_f, stats_f = run_fused(program, env)
    print(f"unfused: buffers={stats_u.buffers_allocated} "
          f"reads={stats_u.element_reads} writes={stats_u.element_writes}")
    print(f"fused  : buffers={stats_f.buffers_allocated} "
          f"reads={stats_f.element_reads} writes={stats_f.element_writes}")
    saved = stats_u.buffers_allocated - stats_f.buffers_allocated
    print(f"buffer allocations reduced by {saved} "
          f"({stats_u.buffers_allocated} -> {stats_f.buffers_allocated})")
    cmp = compare_tensors(values_u[out], values_f[out])
    print(f"forward result: {values_f[out].to_flat()} -> {cmp.verdict.value}")


def demo_error_location():
    section("2. Division by zero located to the original expression")
    program = Program(inputs=["a", "b"])
    t1 = program.add("a", "b")
    t2 = program.div(t1, "b")
    print("program:")
    print(program.describe())
    env = {
        "a": Tensor.from_values([1.0, 2.0, 3.0, 4.0], (2, 2), name="a"),
        "b": Tensor.from_values([1.0, 0.0], (2,), name="b"),
    }
    for name, runner in (("unfused", run_unfused), ("fused", run_fused)):
        try:
            runner(program, env)
        except ElementwiseError as exc:
            print(f"{name}: {exc}")


def demo_gradients():
    section("3. Gradient-graph consistency before/after fusion")
    program = Program(inputs=["a", "b", "c"])
    t1 = program.add("a", "b")
    t2 = program.mul(t1, "c")
    t3 = program.div(t2, "b")
    out = program.reduce_sum(t3)
    env = {
        "a": Tensor.from_values([1.0, 2.0, 3.0, 4.0, 5.0, 6.0], (2, 3), name="a"),
        "b": Tensor.from_values([0.5, 1.5, 2.0], (3,), name="b"),
        "c": Tensor.from_values([2.0, 3.0, 4.0, 5.0, 6.0, 7.0], (2, 3), name="c"),
    }
    report = verify_gradients(program, env, wrt=["a", "b", "c"], seed_var=out)
    for name, cmp in report.forward.items():
        print(f"forward {name}: {cmp.verdict.value}")
    for name, cmp in report.gradients.items():
        print(f"grad    {name}: {cmp.verdict.value}")
    for name, (ok, err) in report.finite_difference.items():
        print(f"finite-difference check {name}: "
              f"{'OK' if ok else 'FAIL'} (max err {err:.2e})")


def demo_reduction_honesty():
    section("4. Reduction reordering is reported honestly (not bitwise)")
    from tensorfusion.program import Op
    env = {"x": Tensor.from_values([1.0, 1e-16, -1.0, 1e-16], (4,), name="x")}
    values_fwd, values_rev = dict(env), dict(env)
    op = Op(kind="reduce_sum", result="s", args=("x",))
    _exec_reduce(op, values_fwd, Stats(), order="forward")
    _exec_reduce(op, values_rev, Stats(), order="reverse")
    cmp = compare_tensors(values_fwd["s"], values_rev["s"])
    print(f"forward-order sum = {values_fwd['s'].to_flat()}, "
          f"reversed-order sum = {values_rev['s'].to_flat()}")
    print(f"verdict: {cmp}")
    assert cmp.verdict is Verdict.CLOSE


def demo_illegal_fusion():
    section("5. Illegal fusion returns concrete dependency evidence")

    print("-- alias hazard (store into a transposed view of an input):")
    x = Tensor.from_values([1, 2, 3, 4], (2, 2), name="x")
    xt = x.transpose()
    c = Tensor.from_values([10, 20, 30, 40], (2, 2), name="c")
    program = Program(inputs=["x", "c"])
    t1 = program.add("x", "c")
    program.store(t1, xt)
    try:
        attempt_fuse(program, {"x": x, "c": c})
    except IllegalFusion as exc:
        print(exc)

    print("-- version dependency (in-place mutation between reads):")
    x = Tensor.from_values([1, 2, 3], (3,), name="x")
    b = Tensor.from_values([10, 20, 30], (3,), name="b")
    d = Tensor.from_values([100, 200, 300], (3,), name="d")
    program = Program(inputs=["x", "b", "d"])
    t1 = program.add("x", "b")
    program.mutate("x", "d")
    t2 = program.mul(t1, "x")
    try:
        attempt_fuse(program, {"x": x, "b": b, "d": d})
    except IllegalFusion as exc:
        print(exc)

    print("-- reduction barrier:")
    a = Tensor.from_values([1, 2, 3, 4], (2, 2), name="a")
    program = Program(inputs=["a", "b2"])
    t1 = program.add("a", "b2")
    s = program.reduce_sum(t1)
    t2 = program.mul(s, "a")
    env = {"a": a, "b2": Tensor.from_values([1, 1, 1, 1], (2, 2), name="b2")}
    try:
        attempt_fuse(program, env)
    except IllegalFusion as exc:
        print(exc)
    fused = compile_program(program, env)
    print(f"conservative compile still works: "
          f"{[u.kind for u in fused.units]}")


def demo_exhaustive():
    section("6. Exhaustive small-shape differential check")
    summary = run_exhaustive()
    print(summary.describe())


if __name__ == "__main__":
    demo_fusion_and_stats()
    demo_error_location()
    demo_gradients()
    demo_reduction_honesty()
    demo_illegal_fusion()
    demo_exhaustive()
