"""Pure single-sample function graphs: nodes, builder, forward evaluation."""
from __future__ import annotations

from dataclasses import dataclass, field

from . import tensor as T

ELEMENTWISE_BINARY = ("add", "sub", "mul", "div")
ELEMENTWISE_UNARY = ("neg", "exp", "tanh", "log", "square")

#: Ops the batch transform refuses: side effects or cross-sample collectives.
#: They remain runnable in the single-sample graph (identity semantics).
SIDE_EFFECT_OPS = {
    "emit": "side-effecting sink 'emit' cannot be replayed per sample",
    "cross_sample_sum": "collective 'cross_sample_sum' mixes values across samples",
}


@dataclass
class Node:
    name: str
    op: str
    inputs: tuple[str, ...] = ()
    params: dict = field(default_factory=dict)


@dataclass
class Graph:
    nodes: dict[str, Node]  # insertion order == topological order
    inputs: list[str]
    outputs: list[str]
    input_shapes: dict[str, tuple[int, ...]]

    def topo(self) -> list[Node]:
        return list(self.nodes.values())


class GraphBuilder:
    def __init__(self):
        self._nodes: dict[str, Node] = {}
        self._inputs: list[str] = []
        self._input_shapes: dict[str, tuple[int, ...]] = {}
        self._counter = 0

    def input(self, name: str, shape) -> str:
        if name in self._nodes:
            raise ValueError(f"duplicate node {name!r}")
        self._nodes[name] = Node(name, "input")
        self._inputs.append(name)
        self._input_shapes[name] = tuple(int(s) for s in shape)
        return name

    def add_node(self, op: str, inputs, params=None, name: str | None = None) -> str:
        if name is None:
            name = f"n{self._counter}"
            self._counter += 1
        if name in self._nodes:
            raise ValueError(f"duplicate node {name!r}")
        for i in inputs:
            if i not in self._nodes:
                raise ValueError(f"unknown input node {i!r}")
        self._nodes[name] = Node(name, op, tuple(inputs), dict(params or {}))
        return name

    # elementwise
    def add(self, a, b): return self.add_node("add", (a, b))
    def sub(self, a, b): return self.add_node("sub", (a, b))
    def mul(self, a, b): return self.add_node("mul", (a, b))
    def div(self, a, b): return self.add_node("div", (a, b))
    def neg(self, a): return self.add_node("neg", (a,))
    def exp(self, a): return self.add_node("exp", (a,))
    def tanh(self, a): return self.add_node("tanh", (a,))
    def log(self, a): return self.add_node("log", (a,))
    def square(self, a): return self.add_node("square", (a,))
    # linear algebra / shape
    def matmul(self, a, b): return self.add_node("matmul", (a, b))
    def transpose(self, a, perm): return self.add_node("transpose", (a,), {"perm": tuple(perm)})
    def reshape(self, a, shape): return self.add_node("reshape", (a,), {"shape": tuple(shape)})
    def reduce_sum(self, a, axis): return self.add_node("reduce_sum", (a,), {"axis": int(axis)})
    def reduce_mean(self, a, axis): return self.add_node("reduce_mean", (a,), {"axis": int(axis)})
    # ops the batch transform rejects (runnable single-sample)
    def emit(self, tag, a): return self.add_node("emit", (a,), {"tag": tag})
    def cross_sample_sum(self, a): return self.add_node("cross_sample_sum", (a,))

    def build(self, outputs) -> Graph:
        outputs = list(outputs)
        keep: set[str] = set()
        stack = list(outputs)
        while stack:
            name = stack.pop()
            if name in keep:
                continue
            if name not in self._nodes:
                raise ValueError(f"unknown output node {name!r}")
            keep.add(name)
            stack.extend(self._nodes[name].inputs)
        nodes = {n: node for n, node in self._nodes.items() if n in keep}
        inputs = [n for n in self._inputs if n in keep]
        return Graph(nodes=nodes, inputs=inputs, outputs=outputs,
                     input_shapes={n: self._input_shapes[n] for n in inputs})


def infer_shape(op: str, in_shapes, params) -> tuple[int, ...]:
    if op in ELEMENTWISE_BINARY:
        return T.broadcast_shape(in_shapes[0], in_shapes[1])
    if op in ELEMENTWISE_UNARY:
        return tuple(in_shapes[0])
    if op == "matmul":
        a, b = in_shapes
        if len(a) < 2 or len(b) < 2 or a[-1] != b[-2]:
            raise ValueError(f"matmul: incompatible shapes {a} and {b}")
        return T.broadcast_shape(a[:-2], b[:-2]) + (a[-2], b[-1])
    if op == "transpose":
        perm = tuple(p % len(in_shapes[0]) for p in params["perm"])
        return tuple(in_shapes[0][p] for p in perm)
    if op == "reshape":
        shape = tuple(params["shape"])
        if T.numel(shape) != T.numel(in_shapes[0]):
            raise ValueError(f"reshape: {in_shapes[0]} -> {shape} changes element count")
        return shape
    if op in ("reduce_sum", "reduce_mean"):
        axis = params["axis"] % len(in_shapes[0])
        return tuple(in_shapes[0][:axis] + in_shapes[0][axis + 1:])
    if op in SIDE_EFFECT_OPS:
        return tuple(in_shapes[0])
    raise ValueError(f"no shape rule for op {op!r}")


def apply_op(op: str, args, params) -> T.Tensor:
    if op == "add": return T.add(args[0], args[1])
    if op == "sub": return T.sub(args[0], args[1])
    if op == "mul": return T.mul(args[0], args[1])
    if op == "div": return T.div(args[0], args[1])
    if op == "neg": return T.neg(args[0])
    if op == "exp": return T.exp(args[0])
    if op == "tanh": return T.tanh(args[0])
    if op == "log": return T.log(args[0])
    if op == "square": return T.square(args[0])
    if op == "matmul": return T.matmul(args[0], args[1])
    if op == "transpose": return T.transpose(args[0], params["perm"])
    if op == "reshape": return T.reshape(args[0], params["shape"])
    if op == "reduce_sum": return T.reduce_sum(args[0], params["axis"])
    if op == "reduce_mean": return T.reduce_mean(args[0], params["axis"])
    if op in SIDE_EFFECT_OPS:  # single-sample semantics: identity
        return args[0]
    raise ValueError(f"unknown op {op!r}")


def evaluate(graph: Graph, env: dict[str, T.Tensor]) -> dict[str, T.Tensor]:
    """Forward-evaluate every node; returns {node_name: Tensor}."""
    values: dict[str, T.Tensor] = {}
    for name in graph.inputs:
        if name not in env:
            raise KeyError(f"missing input {name!r}")
        values[name] = env[name]
    for node in graph.nodes.values():
        if node.op == "input":
            continue
        values[node.name] = apply_op(node.op, [values[i] for i in node.inputs], node.params)
    return values


def format_graph(graph: Graph) -> str:
    lines = []
    for node in graph.nodes.values():
        if node.op == "input":
            lines.append(f"  {node.name}: input {graph.input_shapes[node.name]}")
        else:
            params = f" {node.params}" if node.params else ""
            lines.append(f"  {node.name}: {node.op}({', '.join(node.inputs)}){params}")
    lines.append(f"  outputs: {', '.join(graph.outputs)}")
    return "\n".join(lines)
