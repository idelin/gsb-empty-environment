"""Demo of tinyreduce: multi-axis reductions with gradients.

Run from the repo root:  python3.11 examples/demo.py
"""
import math
import os
import sys

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

from tinyreduce import (Tensor, GradStore, WorkspaceTracker,
                        UndefinedReductionError, NonDifferentiableError,
                        reduce, backward)
from tinyreduce.verify import (run_enumeration, run_alias_enumeration,
                               finite_difference_check)


def main():
    print("=" * 72)
    print("1. Basic multi-axis reductions (keepdims, negative axes)")
    print("=" * 72)
    t = Tensor.from_nested([[1.0, 2.0, 3.0], [4.0, 5.0, 6.0]])
    print("t =", t)
    for op in ("sum", "mean", "min", "max"):
        r = reduce(t, op, axes=-1, keepdims=True)
        print(f"  {op:4s} axes=-1 keepdims -> shape {r.value.shape} "
              f"values {r.value.logical_values()}")
    r = reduce(t, "sum", axes=(1, 0))  # axis order does not matter
    print("  sum  axes=(1,0) ->", r.value.get(()))

    print()
    print("=" * 72)
    print("2. Gradients: sum broadcasts, mean divides by included count")
    print("=" * 72)
    r = reduce(t, "mean", axes=1)
    g = backward(t, r, Tensor.from_nested([1.0, 2.0]))
    print("  d mean(axes=1)/dx with upstream [1,2]:", g.logical_values())

    print()
    print("=" * 72)
    print("3. Ties: marked non-differentiable, never silently first-resolved")
    print("=" * 72)
    t2 = Tensor.from_nested([2.0, 1.0, 1.0, 3.0])
    r2 = reduce(t2, "min")
    print("  min of", t2.logical_values(), "=", r2.value.get(()))
    print("  nondiff outputs:", sorted(r2.nondiff_outputs),
          " nondiff input buffer positions:", sorted(r2.nondiff_inputs))
    print("  uniform policy grad:", backward(t2, r2, 1.0).logical_values())
    print("  first   policy grad:",
          backward(t2, r2, 1.0, tie_policy="first").logical_values())
    try:
        backward(t2, r2, 1.0, on_nondiff="raise")
    except NonDifferentiableError as e:
        print("  strict mode:", e)

    print()
    print("=" * 72)
    print("4. Masks and non-contiguous views")
    print("=" * 72)
    tm = Tensor.from_nested([1.0, 99.0, 3.0], mask_nested=[True, False, True])
    print("  masked sum:", reduce(tm, "sum").value.get(()),
          " masked mean:", reduce(tm, "mean").value.get(()))
    base = Tensor.from_nested([[1.0, 9.0, 2.0, 8.0], [3.0, 7.0, 4.0, 6.0]])
    view = base.slice_axis(1, 0, 4, 2).transpose((1, 0))
    print("  strided+transposed view:", view.shape, view.logical_values())
    rv = reduce(view, "max", axes=0)
    print("  max over axes=0:", rv.value.logical_values(),
          " grad:", backward(view, rv, 1.0).logical_values())

    print()
    print("=" * 72)
    print("5. Empty reduction semantics")
    print("=" * 72)
    e = Tensor([], (0,))
    print("  sum of empty:", reduce(e, "sum").value.get(()))
    for op in ("mean", "min", "max"):
        try:
            reduce(e, op)
        except UndefinedReductionError as exc:
            print(f"  {op} of empty -> UndefinedReductionError: {exc}")

    print()
    print("=" * 72)
    print("6. Chunked tree execution: identical results, recorded workspace")
    print("=" * 72)
    big = Tensor([float((i * 7) % 5) for i in range(10)], (10,))
    for chunk in (1, 3, 1 << 30):
        r = reduce(big, "min", chunk_size=chunk)
        print(f"  chunk={chunk:<11d} min={r.value.get(())} "
              f"ties_grad={backward(big, r, 1.0).logical_values()} "
              f"peak_workspace={r.workspace_peak}")

    print()
    print("=" * 72)
    print("7. Aliased views accumulate gradients into one buffer")
    print("=" * 72)
    data = [1.0, 2.0, 3.0, 4.0, 5.0]
    root = Tensor(data, (5,))
    a, b = root.slice_axis(0, 0, 3), root.slice_axis(0, 2, 5)
    store = GradStore()
    backward(a, reduce(a, "sum"), 1.0, store=store)
    backward(b, reduce(b, "sum"), 1.0, store=store)
    print("  overlapping views [0:3] and [2:5], shared grad buffer:",
          store.buffer_for(root))

    print()
    print("=" * 72)
    print("8. Independent numerical verification")
    print("=" * 72)
    report = run_enumeration()
    print(f"  enumeration : {report['configs']} configs, "
          f"{report['reductions']} reductions, "
          f"{report['grad_checks']} grad checks, "
          f"{report['nondiff_checks']} nondiff checks, "
          f"failures={len(report['failures'])}, "
          f"peak workspace={report['peak_workspace']}")
    alias = run_alias_enumeration()
    print(f"  alias enum  : {alias['configs']} configs, "
          f"failures={len(alias['failures'])}")
    errors = finite_difference_check()
    print("  finite diff :", {k: f"{v:.2e}" for k, v in errors.items()})


if __name__ == "__main__":
    main()
