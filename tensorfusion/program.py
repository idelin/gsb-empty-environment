"""Straight-line intermediate representation (a tape of Ops).

Op kinds:
  elementwise : add / sub / mul / div / neg   (pure, broadcastable)
  reduce_sum  : reduction over all elements    (fusion barrier)
  mutate      : in-place `target += arg`       (bumps storage version)
  store       : copy a value into an existing Tensor view (may alias inputs)
"""

from __future__ import annotations

from dataclasses import dataclass, field

ELEMENTWISE = ("add", "sub", "mul", "div", "neg")


@dataclass
class Op:
    kind: str
    result: str | None = None
    args: tuple = ()
    out: object = None      # Tensor view, for kind == "store"
    target: str | None = None  # variable name, for kind == "mutate"

    def label(self):
        if self.kind in ELEMENTWISE:
            return f"{self.result} = {self.kind}({', '.join(self.args)})"
        if self.kind == "reduce_sum":
            return f"{self.result} = reduce_sum({self.args[0]})"
        if self.kind == "mutate":
            return f"{self.target} += {self.args[1]}  # in place"
        if self.kind == "store":
            name = getattr(self.out, "name", None) or "out"
            return f"store({self.args[0]} -> {name})"
        return f"{self.kind}{self.args}"


class Program:
    def __init__(self, inputs=()):
        self.inputs = list(inputs)
        self.ops: list[Op] = []
        self._counter = 0

    def _fresh(self):
        self._counter += 1
        return f"t{self._counter}"

    def _ew(self, kind, *args, name=None):
        name = name or self._fresh()
        self.ops.append(Op(kind=kind, result=name, args=tuple(args)))
        return name

    def add(self, a, b, name=None):
        return self._ew("add", a, b, name=name)

    def sub(self, a, b, name=None):
        return self._ew("sub", a, b, name=name)

    def mul(self, a, b, name=None):
        return self._ew("mul", a, b, name=name)

    def div(self, a, b, name=None):
        return self._ew("div", a, b, name=name)

    def neg(self, a, name=None):
        return self._ew("neg", a, name=name)

    def reduce_sum(self, a, name=None):
        name = name or self._fresh()
        self.ops.append(Op(kind="reduce_sum", result=name, args=(a,)))
        return name

    def mutate(self, target, arg):
        self.ops.append(Op(kind="mutate", target=target, args=(target, arg)))

    def store(self, a, out):
        self.ops.append(Op(kind="store", args=(a,), out=out))

    def consumers(self):
        """var name -> list of Ops that read it."""
        result = {}
        for op in self.ops:
            for a in op.args:
                result.setdefault(a, []).append(op)
        return result

    def describe(self):
        return "\n".join(f"  {op.label()}" for op in self.ops)
