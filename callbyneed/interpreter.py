"""Call-by-need interpreter.

Each ``let`` binding becomes a :class:`Thunk` shared by every reference
to its name (an expression graph, not a tree). A thunk is forced at most
once:

* ``unevaluated`` -> forced on first demand
* ``blackhole``   -> currently being forced; re-entering means a cycle
* ``evaluated``   -> the memoized value is returned
* ``failed``      -> the memoized exception is re-raised (same instance)

Because unevaluated thunks are never touched, dead code such as an
unused ``1 / 0`` binding never raises.
"""

from __future__ import annotations

from dataclasses import dataclass

from .ast_nodes import BinOp, IntLit, Let, Neg, Node, Var
from .errors import BlackHoleError, DivByZeroError, UnboundNameError
from .parser import parse
from .tracer import Tracer

UNEVALUATED = "unevaluated"
BLACKHOLE = "blackhole"
EVALUATED = "evaluated"
FAILED = "failed"


class Thunk:
    __slots__ = ("name", "name_span", "expr", "env", "state", "value", "error", "eval_count")

    def __init__(self, name: str, name_span: tuple[int, int], expr: Node, env: "Env") -> None:
        self.name = name
        self.name_span = name_span
        self.expr = expr
        self.env = env
        self.state = UNEVALUATED
        self.value: int | None = None
        self.error: Exception | None = None
        self.eval_count = 0  # how many times the expression was actually evaluated

    def __repr__(self) -> str:
        return f"<Thunk {self.name} state={self.state} evals={self.eval_count}>"


class Env:
    """Lexical scope: a chain of frames mapping names to shared thunks."""

    __slots__ = ("parent", "slots")

    def __init__(self, parent: "Env | None" = None) -> None:
        self.parent = parent
        self.slots: dict[str, Thunk] = {}

    def lookup(self, name: str) -> Thunk | None:
        env: Env | None = self
        while env is not None:
            if name in env.slots:
                return env.slots[name]
            env = env.parent
        return None


def force(thunk: Thunk, tracer: Tracer, registry: "list[Thunk] | None" = None) -> int:
    if thunk.state == EVALUATED:
        tracer.cached(thunk)
        return thunk.value
    if thunk.state == FAILED:
        tracer.cached_error(thunk)
        raise thunk.error
    if thunk.state == BLACKHOLE:
        raise BlackHoleError(tracer.cycle_from(thunk.name), thunk.name_span)
    thunk.state = BLACKHOLE
    thunk.eval_count += 1
    tracer.enter(thunk)
    try:
        value = evaluate(thunk.expr, thunk.env, tracer, registry)
    except Exception as exc:
        thunk.state = FAILED
        thunk.error = exc
        tracer.raised(thunk, exc)
        raise
    else:
        thunk.state = EVALUATED
        thunk.value = value
        tracer.produced(thunk, value)
        return value
    finally:
        tracer.exit(thunk)


def evaluate(
    node: Node, env: Env, tracer: Tracer, registry: "list[Thunk] | None" = None
) -> int:
    if isinstance(node, IntLit):
        return node.value
    if isinstance(node, Var):
        thunk = env.lookup(node.name)
        if thunk is None:
            raise UnboundNameError(f"unbound name {node.name!r}", node.span)
        return force(thunk, tracer, registry)
    if isinstance(node, Neg):
        return -evaluate(node.operand, env, tracer, registry)
    if isinstance(node, BinOp):
        left = evaluate(node.left, env, tracer, registry)
        right = evaluate(node.right, env, tracer, registry)
        if node.op == "+":
            return left + right
        if node.op == "-":
            return left - right
        if node.op == "*":
            return left * right
        if node.op == "/":
            if right == 0:
                raise DivByZeroError(f"division by zero in {left} / {right}", node.span)
            return left // right
        raise AssertionError(f"unknown operator {node.op!r}")
    if isinstance(node, Let):
        child = Env(env)
        for binding in node.bindings:
            thunk = Thunk(binding.name, binding.name_span, binding.expr, child)
            child.slots[binding.name] = thunk
            if registry is not None:
                registry.append(thunk)
        return evaluate(node.body, child, tracer, registry)
    raise AssertionError(f"unknown node {node!r}")


@dataclass
class RunResult:
    value: int
    trace: list[str]


class Interpreter:
    """Parses and runs programs; keeps every created thunk for inspection."""

    def __init__(self, trace: bool = False) -> None:
        self.tracer = Tracer(enabled=trace)
        self.thunks: list[Thunk] = []

    def run(self, src: str) -> int:
        return self.run_node(parse(src))

    def run_node(self, node: Node) -> int:
        return evaluate(node, Env(), self.tracer, self.thunks)

    def force(self, thunk: Thunk) -> int:
        return force(thunk, self.tracer, self.thunks)

    def thunk_named(self, name: str) -> Thunk:
        for thunk in self.thunks:
            if thunk.name == name:
                return thunk
        raise KeyError(name)

    @property
    def trace_lines(self) -> list[str]:
        return self.tracer.lines
