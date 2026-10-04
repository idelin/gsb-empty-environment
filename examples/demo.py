"""Runnable tour of tinyreduce: python3 examples/demo.py"""
import math
import os
import sys

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

from tinyreduce import (Tensor, EmptyReductionError, NonDifferentiableError,
                        value_and_grad)

line = lambda t: print("\n=== %s ===" % t)

line("1. Multi-axis reductions, keepdims, negative axes")
t = Tensor([[1.0, 2.0, 3.0], [4.0, 5.0, 6.0]])
print("t        =", t)
print("sum      =", t.sum().item())
print("sum ax=0 =", t.sum(axis=0).to_flat())
print("sum ax=-1, keepdims:", t.sum(axis=-1, keepdims=True).to_nested())
print("mean     =", t.mean().item(), " min =", t.min().item(),
      " max(ax=1) =", t.max(axis=1).to_flat())

line("2. Masks (truthy = excluded)")
mask = Tensor([[0, 1, 0], [0, 0, 1]])
print("sum masked:", t.sum(mask=mask).item(),
      " mean masked:", t.mean(mask=mask).item(),
      " max masked:", t.max(mask=mask).item())

line("3. Non-contiguous views reduce correctly")
v = t[::-1, ::2]                      # reversed rows, every 2nd column
print("view t[::-1, ::2] =", v.to_nested(), "-> sum:", v.sum().item(),
      " max:", v.max().item())

line("4. Chunked tree execution + peak workspace")
big = Tensor([float(i % 7) for i in range(1024)])
for cs in (None, 1, 8, 1024):
    out = big.sum(chunk_size=cs)
    print(f"chunk_size={str(cs):>5}: sum={out.item():8.1f}  "
          f"{out.reduction_stats}")

line("5. Gradients")
g_t = Tensor([1.0, 5.0, 3.0]).requires_grad_()
g_t.sum().backward()
print("d sum  =", g_t.grad.to_flat())
g_t.zero_grad()
g_t.max().backward()
print("d max  =", g_t.grad.to_flat())

line("6. Max ties: equal split (never silent first-element)")
out, g = value_and_grad(Tensor([1.0, 3.0, 3.0, 2.0]), "max")
print("value =", out.item(), " grad =", g.to_flat())
print("nondifferentiable points:", g.nondiff_indices)

line("7. Higher-order: marked points refuse to differentiate silently")
try:
    g.backward(Tensor.ones(g.shape))
except NonDifferentiableError as e:
    print("NonDifferentiableError:", e)
t2 = Tensor([1.0, 2.0, 3.0]).requires_grad_()
_, g2 = value_and_grad(t2, "max")          # unique max: smooth here
g2.backward(Tensor.ones(g2.shape))
print("d2(max) at unique max =", t2.grad.to_flat())

line("8. Empty reduction semantics")
print("sum([]) =", Tensor([]).sum().item())
for op in ("mean", "min", "max"):
    try:
        getattr(Tensor([]), op)()
    except EmptyReductionError as e:
        print(f"{op}([]) -> EmptyReductionError: {e}")

line("9. Aliased gradient accumulation (overlapping views)")
base = Tensor([1.0, 2.0, 3.0, 4.0]).requires_grad_()
base[0:3].sum().backward()
base[1:4].sum().backward()
print("grad of overlapping slices [0:3] and [1:4]:", base.grad.to_flat())

line("10. Infinities")
print("max([1, +inf]) =", Tensor([1.0, math.inf]).max().item(),
      " sum([+inf, -inf]) =", Tensor([math.inf, -math.inf]).sum().item())
