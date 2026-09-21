"""call-by-need 核心：表达式图、thunk、黑洞检测与调试轨迹。

一个 thunk 是图中的一个节点，保存“表达式 + 词法环境”。它有三种状态::

    suspended（未求值） -> running（求值中） -> done（已完成）

- ``done`` 时缓存值 **或** 异常：之后无论被引用多少次都直接命中缓存。
- 在 ``running`` 状态再次被强制，说明强制链上出现环，即自引用黑洞；
  轨迹器里保存的强制栈可还原出完整的环（直接/间接都能定位）。
"""
from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any

from . import ast
from .errors import (
    BlackHoleError,
    ChainEntry,
    DivByZeroError,
    EvalError,
    UnboundVariableError,
)
from .parser import parse
from .sourcemap import SourceMap, Span


# ---------------------------------------------------------------- 环境

class Env:
    """词法环境：本层绑定 + 指向上层的父指针。"""

    def __init__(self, parent: "Env | None" = None, bindings: dict | None = None) -> None:
        self.parent = parent
        self.bindings: dict[str, "Thunk"] = bindings or {}

    def lookup(self, name: str) -> "Thunk | None":
        if name in self.bindings:
            return self.bindings[name]
        if self.parent is not None:
            return self.parent.lookup(name)
        return None


# ---------------------------------------------------------------- 轨迹

@dataclass
class Event:
    kind: str                 # enter / hit / leave / blackhole / tick
    name: str
    depth: int
    at: Span | None = None
    value: Any = None
    error: BaseException | None = None
    chain: list[ChainEntry] | None = None
    tick_no: int = 0
    thunk: "Thunk | None" = None


@dataclass
class Tracer:
    """记录强制求值链。默认实现即收集事件，测试可据此断言。"""

    events: list[Event] = field(default_factory=list)
    sourcemap: SourceMap | None = None
    tick_count: int = 0
    # 当前正在求值的 thunk 栈：(thunk, 触发强制的源码位置)
    stack: list[tuple["Thunk", Span | None]] = field(default_factory=list)

    def _location(self, at: Span | None) -> str:
        if at is None or self.sourcemap is None:
            return "<内部>"
        preview = self.sourcemap.slice(at)
        first_line = preview.splitlines()[0] if preview else ""
        if len(first_line) > 24:
            first_line = first_line[:24] + "…"
        return f"{self.sourcemap.describe(at.start)}: {first_line!r}"

    # ---- 由 Thunk 调用 ----
    def on_enter(self, thunk: "Thunk", at: Span | None) -> None:
        self.events.append(
            Event("enter", thunk.name, len(self.stack), at=at, thunk=thunk)
        )
        self.stack.append((thunk, at))

    def on_hit(self, thunk: "Thunk", at: Span | None,
               value: Any, error: BaseException | None) -> None:
        self.events.append(
            Event("hit", thunk.name, len(self.stack), at=at,
                  value=value, error=error)
        )

    def on_leave(self, thunk: "Thunk", value: Any,
                 error: BaseException | None) -> None:
        self.stack.pop()
        self.events.append(
            Event("leave", thunk.name, len(self.stack), value=value, error=error)
        )

    def on_blackhole(self, thunk: "Thunk", at: Span | None,
                     error: BlackHoleError) -> None:
        self.events.append(
            Event("blackhole", thunk.name, len(self.stack),
                  at=at, error=error, chain=list(error.chain))
        )

    def note_tick(self, at: Span | None) -> int:
        """``tick()`` 被真正执行一次，返回这是第几次（从 1 开始）。"""
        self.tick_count += 1
        owner = self.stack[-1][0].name if self.stack else "<根表达式>"
        self.events.append(
            Event("tick", owner, max(0, len(self.stack) - 1),
                  at=at, tick_no=self.tick_count)
        )
        return self.tick_count

    def build_blackhole_chain(self, thunk: "Thunk",
                              at: Span | None) -> list[ChainEntry]:
        """从强制栈中切出从第一次进入该 thunk 到现在的环。"""
        start = next(
            i for i, (current, _) in enumerate(self.stack) if current is thunk
        )
        chain = [
            ChainEntry(current.name, current.binding_span, forced_at)
            for current, forced_at in self.stack[start:]
        ]
        chain.append(ChainEntry(thunk.name, thunk.binding_span, at))
        return chain

    # ---- 展示 ----
    def render_trace(self) -> str:
        lines: list[str] = []
        for event in self.events:
            indent = "    " * event.depth
            location = f"  @ {self._location(event.at)}" if event.at is not None else ""
            if event.kind == "enter":
                lines.append(f"{indent}+-> 强制 thunk {event.name!r}{location}")
            elif event.kind == "hit":
                if event.error is not None:
                    lines.append(
                        f"{indent}|== 命中缓存异常 (thunk {event.name!r}): "
                        f"{event.error.message}{location}"
                    )
                else:
                    lines.append(
                        f"{indent}|== 命中缓存值 (thunk {event.name!r}) = "
                        f"{self._format_value(event.value)}{location}"
                    )
            elif event.kind == "leave":
                if event.error is not None:
                    lines.append(
                        f"{indent}<-x thunk {event.name!r} 缓存异常: "
                        f"{event.error.message}"
                    )
                else:
                    lines.append(
                        f"{indent}<-- thunk {event.name!r} 求值完成 = "
                        f"{self._format_value(event.value)}"
                    )
            elif event.kind == "tick":
                lines.append(
                    f"{indent}* tick() 第 {event.tick_no} 次真正执行"
                    f"（位于 thunk {event.name!r}）"
                )
            elif event.kind == "blackhole":
                chain = " -> ".join(entry.name for entry in (event.chain or []))
                lines.append(
                    f"{indent}!! 检测到自引用黑洞 (thunk {event.name!r})"
                    f"{location}，环: {chain}"
                )
        return "\n".join(lines)

    @staticmethod
    def _format_value(value: Any) -> str:
        if isinstance(value, bool):
            return "true" if value else "false"
        return repr(value)


# ---------------------------------------------------------------- thunk

class Thunk:
    SUSPENDED = "suspended"
    RUNNING = "running"
    DONE = "done"

    def __init__(self, name: str, expr: ast.Expr, env: Env,
                 binding_span: Span | None, evaluator: "Evaluator") -> None:
        self.name = name
        self.expr = expr
        self.env = env
        self.binding_span = binding_span
        self.evaluator = evaluator
        self.status = self.SUSPENDED
        self.value: Any = None
        self.error: EvalError | None = None

    @property
    def forced(self) -> bool:
        """是否至少被强制过（已结束求值，无论成功/失败）。"""
        return self.status == self.DONE

    def force(self, at: Span | None = None) -> Any:
        tracer = self.evaluator.tracer
        if self.status == self.DONE:
            tracer.on_hit(self, at, self.value, self.error)
            if self.error is not None:
                raise self.error
            return self.value
        if self.status == self.RUNNING:
            error = BlackHoleError(
                self.name, tracer.build_blackhole_chain(self, at)
            )
            tracer.on_blackhole(self, at, error)
            raise error

        self.status = self.RUNNING
        tracer.on_enter(self, at)
        try:
            result = self.evaluator.eval(self.expr, self.env)
        except EvalError as error:
            # 异常也是结果：缓存起来，后续引用直接重抛。
            self.status = self.DONE
            self.error = error
            tracer.on_leave(self, None, error)
            raise
        self.status = self.DONE
        self.value = result
        tracer.on_leave(self, result, None)
        return result


# ---------------------------------------------------------------- 求值器

class Evaluator:
    def __init__(self, tracer: Tracer, sourcemap: SourceMap) -> None:
        self.tracer = tracer
        self.sourcemap = sourcemap

    def eval(self, expr: ast.Expr, env: Env) -> Any:
        if isinstance(expr, ast.IntLit):
            return expr.value
        if isinstance(expr, ast.BoolLit):
            return expr.value
        if isinstance(expr, ast.Tick):
            return self.tracer.note_tick(at=expr.span)
        if isinstance(expr, ast.Var):
            thunk = env.lookup(expr.name)
            if thunk is None:
                raise UnboundVariableError(
                    f"未绑定的变量 {expr.name!r}", expr.span
                )
            return thunk.force(at=expr.span)
        if isinstance(expr, ast.Neg):
            return -self._require_int(self.eval(expr.operand, env), expr.span)
        if isinstance(expr, ast.BinOp):
            return self._eval_binop(expr, env)
        if isinstance(expr, ast.If):
            cond = self.eval(expr.cond, env)
            chosen = expr.then_expr if self._truthy(cond) else expr.else_expr
            return self.eval(chosen, env)
        if isinstance(expr, ast.Let):
            return self._eval_let(expr, env)
        raise EvalError(f"未知表达式节点 {type(expr).__name__}", getattr(expr, "span", None))

    # ---- let ----
    def _eval_let(self, expr: ast.Let, env: Env) -> Any:
        if not expr.rec:
            # 顺序建链：后一个绑定能看见前面的，但看不见自己。
            local = env
            for binding in expr.bindings:
                thunk = self._make_thunk(binding, local)
                local = Env(local, {binding.name: thunk})
            return self.eval(expr.body, local)

        # rec：所有 thunk 共享同一个递归环境（自引用在此被黑洞捕获）。
        recursive_env = Env(env)
        for binding in expr.bindings:
            recursive_env.bindings[binding.name] = self._make_thunk(
                binding, recursive_env
            )
        return self.eval(expr.body, recursive_env)

    def _make_thunk(self, binding: ast.Binding, env: Env) -> Thunk:
        return Thunk(binding.name, binding.value, env, binding.name_span, self)

    # ---- 二元运算 ----
    def _eval_binop(self, expr: ast.BinOp, env: Env) -> Any:
        # 短路：被跳过的一侧连强制都不会发生。
        if expr.op == "&&":
            return self._truthy(self.eval(expr.left, env)) and \
                self._truthy(self.eval(expr.right, env))
        if expr.op == "||":
            return self._truthy(self.eval(expr.left, env)) or \
                self._truthy(self.eval(expr.right, env))

        left = self.eval(expr.left, env)
        right = self.eval(expr.right, env)
        op = expr.op
        if op in ("==", "!=", "<", ">", "<=", ">="):
            compare = {
                "==": left == right, "!=": left != right,
                "<": left < right, ">": left > right,
                "<=": left <= right, ">=": left >= right,
            }[op]
            return bool(compare)

        left = self._require_int(left, expr.span)
        right = self._require_int(right, expr.span)
        if op == "+":
            return left + right
        if op == "-":
            return left - right
        if op == "*":
            return left * right
        if op == "/":
            if right == 0:
                raise DivByZeroError("除数为 0（整数除法）", expr.span)
            return left // right
        if op == "%":
            if right == 0:
                raise DivByZeroError("除数为 0（取模）", expr.span)
            return left % right
        raise EvalError(f"未知运算符 {op!r}", expr.span)

    @staticmethod
    def _truthy(value: Any) -> bool:
        if isinstance(value, bool):
            return value
        return value != 0

    def _require_int(self, value: Any, span: Span | None) -> int:
        if isinstance(value, bool) or not isinstance(value, int):
            raise EvalError(
                f"这里需要整数，但得到 {type(value).__name__}", span
            )
        return value


# ---------------------------------------------------------------- 解释器

class Interpreter:
    """编译源码并管理顶层 thunk 图。"""

    def __init__(self, source: str | bytes, tracer: Tracer | None = None) -> None:
        self.sourcemap = SourceMap(source)
        self.program = parse(self.sourcemap.text_bytes)
        self.tracer = tracer or Tracer()
        if self.tracer.sourcemap is None:
            self.tracer.sourcemap = self.sourcemap
        self.evaluator = Evaluator(self.tracer, self.sourcemap)

        # 顶层非递归 let：顺序扩展环境。
        local = Env()
        self.top_thunks: dict[str, Thunk] = {}
        for binding in self.program.bindings:
            thunk = Thunk(
                binding.name, binding.value, local,
                binding.name_span, self.evaluator,
            )
            self.top_thunks[binding.name] = thunk
            local = Env(local, {binding.name: thunk})

        self.result_thunk = Thunk(
            "<结果>", self.program.result, local,
            None, self.evaluator,
        )

    def run(self) -> int:
        """强制求值根表达式。"""
        return self.result_thunk.force(at=self.program.result.span)
