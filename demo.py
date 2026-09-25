"""Demo: Hessian-vector products with minigrad (pure stdlib, Python 3.11)."""

import math

from minigrad import Tensor, grad, hvp


def show(title, got, want=None):
    got_s = "[" + ", ".join(f"{v:+.8f}" for v in got) + "]"
    if want is None:
        print(f"  {title:<28}{got_s}")
    else:
        want_s = "[" + ", ".join(f"{v:+.8f}" for v in want) + "]"
        err = max(abs(a - b) for a, b in zip(got, want))
        print(f"  {title:<28}{got_s}   expected {want_s}   max|err|={err:.2e}")


print("== 1. Quadratic: f(x) = 0.5 x'Ax + b'x, HVP should equal A v ==")
A = Tensor([2.0, 0.5, -1.0,
            0.5, 3.0, 0.25,
            -1.0, 0.25, 1.5], (3, 3))  # symmetric
b = Tensor([1.0, -2.0, 0.5], (3, 1))
x = Tensor([0.5, -1.0, 2.0], (3, 1), requires_grad=True)
v = Tensor([1.5, 0.5, -1.0], (3, 1))
f = (x.T @ A @ x).sum() * 0.5 + (b * x).sum()
av = [sum(A.data[i * 3 + j] * v.data[j] for j in range(3)) for i in range(3)]
show("HVP (engine)", hvp(f, x, v).data, av)

print("== 2. Nonlinear: f(x) = exp(w.x) + sum(x^3), HVP vs finite differences ==")
W = [0.5, -1.0, 2.0]
XS = [0.3, -0.7, 1.2]
V = [1.5, 0.5, -1.0]


def f_plain(xs):
    return math.exp(sum(wi * xi for wi, xi in zip(W, xs))) + sum(xi ** 3 for xi in xs)


def grad_plain(xs, h=1e-5):
    out = []
    for i in range(len(xs)):
        xp, xm = list(xs), list(xs)
        xp[i] += h
        xm[i] -= h
        out.append((f_plain(xp) - f_plain(xm)) / (2 * h))
    return out


h = 1e-3
gp = grad_plain([x0 + h * vi for x0, vi in zip(XS, V)])
gm = grad_plain([x0 - h * vi for x0, vi in zip(XS, V)])
hv_fd = [(a - b_) / (2 * h) for a, b_ in zip(gp, gm)]

w = Tensor(W)
x2 = Tensor(XS, requires_grad=True)
f2 = (w * x2).sum().exp() + (x2 * x2 * x2).sum()
show("HVP (engine)", hvp(f2, x2, Tensor(V)).data, hv_fd)

print("== 3. Linear: f(x) = c'x, Hessian is zero ==")
c = Tensor([1.5, -0.5, 2.0])
x3 = Tensor(XS, requires_grad=True)
show("HVP (engine)", hvp((c * x3).sum() + 3.0, x3, Tensor(V)).data, [0.0, 0.0, 0.0])

print("== 4. Repeated differentiation leaves no residue ==")
x4 = Tensor(XS, requires_grad=True)
f4 = (w * x4).sum().exp() + (x4 * x4 * x4).sum()
g1 = grad(f4, x4)
g2 = grad(f4, x4)
h1 = hvp(f4, x4, Tensor(V))
h2 = hvp(f4, x4, Tensor(V))
print(f"  grad stable across calls:     {g1.data == g2.data}")
print(f"  hvp  stable across calls:     {h1.data == h2.data}")
print(f"  input data unpolluted:        {x4.data == XS}")
