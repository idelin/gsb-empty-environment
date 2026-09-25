"""End-to-end demo: batched broadcast matmul + VJP/JVP/HVP on views.

Run from the repo root:  python3.11 examples/run_example.py
"""
import os
import sys

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

from tinymat import (Tensor, inner, matmul, matmul_backward, matmul_hvp,
                     matmul_jvp)


def show(title, value):
    print(f"{title:<34} {value}")


print("=== 1. Batched matmul, A broadcast to multiple batches ===")
a = Tensor.from_nested([[1.0, 2.0, 3.0],
                        [4.0, 5.0, 6.0]])                       # (2,3)
b = Tensor.from_nested([[[1.0, 0.0], [0.0, 1.0], [1.0, 1.0]],
                        [[2.0, 1.0], [1.0, 2.0], [0.0, 1.0]],
                        [[1.0, 1.0], [1.0, 0.0], [2.0, 0.0]]])  # (3,3,2)
c = matmul(a, b)
show("A (2,3) @ B (3,3,2) ->", f"shape {c.shape}")
for i, row in enumerate(c.to_nested()):
    show(f"  batch {i}", row)

print("\n=== 2. Backward: grad_A sums over the 3 broadcast batches ===")
seed = Tensor.from_nested([[[1.0, 1.0], [1.0, 1.0]],
                           [[2.0, 2.0], [2.0, 2.0]],
                           [[3.0, 3.0], [3.0, 3.0]]])
grad_a, grad_b = matmul_backward(seed, a, b)
show("grad_A (reduced to (2,3))", grad_a.to_nested())
show("grad_B shape", grad_b.shape)

print("\n=== 3. Vector special cases (numpy matmul semantics) ===")
v = Tensor.from_nested([1.0, 2.0, 3.0])
show("v @ v (scalar)", matmul(v, v).to_nested())
show("v @ B[0] ((3,)@(3,2))", matmul(v, b.slice_dim(0, 0, 1)
                                      .view((3, 2), (2, 1))).to_nested())
show("A @ v ((2,3)@(3,))", matmul(a, v).to_nested())

print("\n=== 4. Non-contiguous views compose (no copies) ===")
base = Tensor.from_nested([[float(i * 4 + j) for j in range(4)]
                           for i in range(6)])
view = (base.transpose(1, 0)          # (4,6)
        .slice_dim(0, 0, 4, 2)        # (2,6)  every other row
        .slice_dim(1, 1, 6, 2)        # (2,3)  odd columns
        .broadcast_to((5, 2, 3)))     # stride-0 batch
show("view shape / buffer length", f"{view.shape} / {len(view.data)}")
w = Tensor.from_nested([[1.0, 0.5], [0.0, 1.0], [2.0, -1.0]])
out = matmul(view, w, workspace_budget=12)   # tiny workspace -> many tiles
show("view @ W (batch 4)", out.to_nested()[4])

print("\n=== 5. JVP: d(A@B)[dA,dB] = dA@B + A@dB ===")
da = Tensor.from_nested([[0.1, 0.0, 0.0], [0.0, 0.1, 0.0]])
db = Tensor.from_nested([[[0.0, 0.1], [0.1, 0.0], [0.0, 0.0]],
                         [[0.1, 0.0], [0.0, 0.1], [0.0, 0.0]],
                         [[0.0, 0.0], [0.0, 0.0], [0.1, 0.1]]])
jvp = matmul_jvp(a, b, da, db)
show("JVP batch 0", jvp.to_nested()[0])

print("\n=== 6. HVP of L = <seed, A@B> along (U, V) ===")
u = Tensor.from_nested([[1.0, 0.0, 0.0], [0.0, 1.0, 0.0]])
vv = Tensor.from_nested([[[0.0, 0.0], [0.0, 0.0], [1.0, 0.0]],
                         [[0.0, 0.0], [0.0, 0.0], [0.0, 1.0]],
                         [[0.0, 0.0], [0.0, 0.0], [0.0, 0.0]]])
h_a, h_b = matmul_hvp(seed, a, b, u, vv)
show("HVP w.r.t. A", h_a.to_nested())
show("HVP w.r.t. B batch 0", h_b.to_nested()[0])

print("\n=== 7. Edge cases ===")
z = matmul(Tensor.zeros((2, 0)), Tensor.zeros((0, 4)))
show("inner dim 0 -> zeros", f"shape {z.shape}, all zero: "
     f"{all(x == 0.0 for row in z.to_nested() for x in row)}")
buf = [2.0]
shared = Tensor(buf, (2, 3), (0, 0))            # 6 aliases of one cell
ones_seed = Tensor.from_nested([[1.0, 1.0], [1.0, 1.0]])
b_mat = Tensor.from_nested([[1.0, 2.0], [3.0, 4.0], [5.0, 6.0]])
g_shared, _ = matmul_backward(ones_seed, shared, b_mat)
show("grad of 6-way shared cell", f"{g_shared.data} "
     f"(= 6 logical contributions summed physically)")
