"""尾调用安全的解释器。

设计要点：
- 求值器是显式状态机：控制项 + 续体栈（待完成运算）+ 堆上帧栈，
  因此物理栈（Python 调用栈）恒为 O(1)，与递归深度无关。
- 尾位置调用不压新帧，直接复用当前帧；逻辑调用轨迹仍然完整保留，
  帧复用时在逻辑栈条目上累计“尾复用”次数。
- try/finally 的清理动作属于待完成运算：try 体内的调用一律按非尾调用
  处理，保证清理顺序；异常展开时也会先执行沿途的 finally。

尾位置的判定（tail=True 向下传递）：
- 函数体、主表达式是尾位置；
- if 的两个分支、let 的体、and/or 的右操作数继承尾位置；
- try 的体不是尾位置（finally 必须先运行）；finally 的体是尾位置
  （此时清理动作已确定，帧复用安全）；
- 其余位置（运算操作数、调用参数、let 的绑定值等）都不是尾位置。
"""

from dataclasses import dataclass

from .ast_nodes import (BinOp, BoolLit, Call, If, IntLit, Let, StrLit,
                        TryFinally, UnaryOp, Var)
from .errors import LangError


class UserFunction:
    __slots__ = ("defn",)

    def __init__(self, defn):
        self.defn = defn

    def __repr__(self):
        return "<函数 {}>".format(self.defn.name)


class BuiltinFunction:
    __slots__ = ("name", "arity", "fn")

    def __init__(self, name, arity, fn):
        self.name = name
        self.arity = arity
        self.fn = fn

    def __repr__(self):
        return "<内建函数 {}>".format(self.name)


class Frame:
    """物理帧：尾调用时被原地复用，帧数不随尾递归深度增长。"""

    __slots__ = ("name", "env", "call_id", "depth")

    def __init__(self, name, env, call_id, depth):
        self.name = name
        self.env = env
        self.call_id = call_id
        self.depth = depth


class LogicalEntry:
    """逻辑调用栈条目：帧被尾调用复用时，逻辑轨迹依然保留。"""

    __slots__ = ("label", "first", "last", "reuses")

    def __init__(self, label=None, first=None):
        self.label = label      # 帧的创建者标签（如 <主程序>）
        self.first = first      # 帧内第一个调用的描述
        self.last = first
        self.reuses = 0

    def reuse(self, desc):
        if self.first is None:
            self.first = desc
            self.last = desc
        else:
            self.last = desc
            self.reuses += 1

    def __str__(self):
        if self.first is None:
            return self.label or "<帧>"
        if self.reuses:
            body = "{} ↻ {}（尾复用 ×{}）".format(self.first, self.last,
                                                 self.reuses)
        else:
            body = self.first
        if self.label:
            return "{}: {}".format(self.label, body)
        return body


# ---- 续体：求值过程中“待完成的运算” ----

@dataclass
class KUnary:
    op: str
    span: tuple


@dataclass
class KBinRight:
    op: str
    right: object
    env: dict
    span: tuple


@dataclass
class KBinLeft:
    op: str
    left: object
    span: tuple


@dataclass
class KLogic:
    op: str
    right: object
    env: dict
    tail: bool


@dataclass
class KIf:
    then: object
    otherwise: object
    env: dict
    tail: bool


@dataclass
class KLet:
    name: str
    body: object
    env: dict
    tail: bool


@dataclass
class KCallee:
    args: list
    env: dict
    span: tuple
    tail: bool


@dataclass
class KArgs:
    func: object
    rest: list
    collected: list
    env: dict
    span: tuple
    tail: bool


@dataclass
class KPopFrame:
    pass


@dataclass
class KFinally:
    finalizer: object
    env: dict


@dataclass
class KFinallyValue:
    value: object


@dataclass
class KFinallyRaise:
    exc: object


def _truthy(value):
    return bool(value)


class Machine:
    """显式状态机解释器。运行结束后可检查以下公开状态：

    result / error    运行结果或未捕获的 LangError
    output            emit() 的输出序列
    max_frames        物理帧数峰值（尾递归时保持为常数）
    tail_reuse_count  尾调用帧复用次数
    trace_log         可读的调用轨迹（trace=True 时记录）
    logical_stack     逻辑调用栈（帧复用时逻辑轨迹不丢失）
    """

    def __init__(self, program, trace=False):
        self.functions = {d.name: d for d in program.defs}
        self.builtins = {
            "emit": BuiltinFunction("emit", 1, lambda m, args: m._emit(args[0])),
        }
        self.trace_enabled = trace
        self.trace_log = []
        self.output = []
        self.frames = []
        self.kont = []
        self.logical_stack = []
        self.max_frames = 0
        self.call_count = 0
        self.tail_reuse_count = 0
        self.result = None
        self.error = None

    # ---- 主循环：物理栈恒为 O(1) ----
    def execute(self, main):
        self.frames.append(Frame("<主程序>", {}, 0, 0))
        self.logical_stack.append(LogicalEntry(label="<主程序>"))
        self.max_frames = 1
        control = ("eval", main, {}, True)
        while True:
            tag = control[0]
            if tag == "eval":
                try:
                    control = self._step_eval(control[1], control[2], control[3])
                except LangError as exc:
                    control = ("unwind", exc)
            elif tag == "value":
                if not self.kont:
                    return control[1]
                kont = self.kont.pop()
                try:
                    control = self._deliver(kont, control[1])
                except LangError as exc:
                    control = ("unwind", exc)
            else:
                control = self._unwind(control[1])

    # ---- 表达式求值 ----
    def _step_eval(self, expr, env, tail):
        if isinstance(expr, (IntLit, BoolLit, StrLit)):
            return ("value", expr.value)
        if isinstance(expr, Var):
            if expr.name in env:
                return ("value", env[expr.name])
            if expr.name in self.functions:
                return ("value", UserFunction(self.functions[expr.name]))
            if expr.name in self.builtins:
                return ("value", self.builtins[expr.name])
            raise LangError("未定义的名字：{}".format(expr.name), expr.span)
        if isinstance(expr, UnaryOp):
            self.kont.append(KUnary(expr.op, expr.span))
            return ("eval", expr.operand, env, False)
        if isinstance(expr, BinOp):
            if expr.op in ("and", "or"):
                # and/or 的右操作数继承尾位置
                self.kont.append(KLogic(expr.op, expr.right, env, tail))
            else:
                self.kont.append(KBinRight(expr.op, expr.right, env, expr.span))
            return ("eval", expr.left, env, False)
        if isinstance(expr, If):
            self.kont.append(KIf(expr.then, expr.otherwise, env, tail))
            return ("eval", expr.cond, env, False)
        if isinstance(expr, Let):
            self.kont.append(KLet(expr.name, expr.body, env, tail))
            return ("eval", expr.value, env, False)
        if isinstance(expr, Call):
            self.kont.append(KCallee(expr.args, env, expr.span, tail))
            return ("eval", expr.callee, env, False)
        if isinstance(expr, TryFinally):
            # finally 是待完成的清理动作：try 体内的调用不能复用帧
            self.kont.append(KFinally(expr.finalizer, env))
            return ("eval", expr.body, env, False)
        raise LangError("未知的语法节点：{}".format(type(expr).__name__),
                        expr.span)

    # ---- 把值交给续体 ----
    def _deliver(self, kont, value):
        if isinstance(kont, KUnary):
            return ("value", self._unary(kont.op, value, kont.span))
        if isinstance(kont, KBinRight):
            self.kont.append(KBinLeft(kont.op, value, kont.span))
            return ("eval", kont.right, kont.env, False)
        if isinstance(kont, KBinLeft):
            return ("value", self._binop(kont.op, kont.left, value, kont.span))
        if isinstance(kont, KLogic):
            if kont.op == "and":
                if not _truthy(value):
                    return ("value", value)
                return ("eval", kont.right, kont.env, kont.tail)
            if _truthy(value):
                return ("value", value)
            return ("eval", kont.right, kont.env, kont.tail)
        if isinstance(kont, KIf):
            branch = kont.then if _truthy(value) else kont.otherwise
            return ("eval", branch, kont.env, kont.tail)
        if isinstance(kont, KLet):
            new_env = dict(kont.env)
            new_env[kont.name] = value
            return ("eval", kont.body, new_env, kont.tail)
        if isinstance(kont, KCallee):
            if not kont.args:
                return self._call(value, [], kont.span, kont.tail)
            self.kont.append(KArgs(value, kont.args[1:], [], kont.env,
                                   kont.span, kont.tail))
            return ("eval", kont.args[0], kont.env, False)
        if isinstance(kont, KArgs):
            collected = kont.collected + [value]
            if kont.rest:
                self.kont.append(KArgs(kont.func, kont.rest[1:], collected,
                                       kont.env, kont.span, kont.tail))
                return ("eval", kont.rest[0], kont.env, False)
            return self._call(kont.func, collected, kont.span, kont.tail)
        if isinstance(kont, KPopFrame):
            frame = self.frames.pop()
            self.logical_stack.pop()
            self._trace(frame.depth, "← #{} 返回 {!r}".format(frame.call_id,
                                                             value))
            return ("value", value)
        if isinstance(kont, KFinally):
            # 正常离开 try：先执行 finally，再递交原值
            self.kont.append(KFinallyValue(value))
            return ("eval", kont.finalizer, kont.env, True)
        if isinstance(kont, KFinallyValue):
            return ("value", kont.value)
        if isinstance(kont, KFinallyRaise):
            return ("unwind", kont.exc)
        raise AssertionError("未知续体：{!r}".format(kont))

    # ---- 调用 ----
    def _call(self, func, args, span, tail):
        if isinstance(func, BuiltinFunction):
            if len(args) != func.arity:
                raise LangError(
                    "内建函数 {} 需要 {} 个参数，实际传入 {} 个".format(
                        func.name, func.arity, len(args)), span)
            return ("value", func.fn(self, args))
        if not isinstance(func, UserFunction):
            raise LangError("试图调用不可调用的值：{!r}".format(func), span)
        defn = func.defn
        if len(args) != len(defn.params):
            raise LangError(
                "函数 {} 需要 {} 个参数，实际传入 {} 个".format(
                    defn.name, len(defn.params), len(args)), span)
        self.call_count += 1
        new_env = dict(zip(defn.params, args))
        desc = "{}({})".format(defn.name, ", ".join(map(repr, args)))
        if tail:
            # 尾位置调用：复用当前帧，物理帧数不增长
            frame = self.frames[-1]
            frame.name = defn.name
            frame.env = new_env
            frame.call_id = self.call_count
            self.logical_stack[-1].reuse(desc)
            self.tail_reuse_count += 1
            self._trace(frame.depth,
                        "↻ #{} {}（复用帧）".format(self.call_count, desc))
        else:
            depth = self.frames[-1].depth + 1
            self.kont.append(KPopFrame())
            self.frames.append(Frame(defn.name, new_env, self.call_count,
                                     depth))
            self.logical_stack.append(LogicalEntry(first=desc))
            if len(self.frames) > self.max_frames:
                self.max_frames = len(self.frames)
            self._trace(depth, "→ #{} {}（新帧）".format(self.call_count, desc))
        return ("eval", defn.body, new_env, True)

    # ---- 异常展开：沿途执行 finally，保持清理顺序 ----
    def _unwind(self, exc):
        if not hasattr(exc, "trace_entries"):
            exc.trace_entries = []
        while self.kont:
            kont = self.kont.pop()
            if isinstance(kont, KFinally):
                self.kont.append(KFinallyRaise(exc))
                return ("eval", kont.finalizer, kont.env, True)
            if isinstance(kont, KPopFrame):
                self.frames.pop()
                # 展开时逐帧记录逻辑轨迹，保证出错现场不丢失
                exc.trace_entries.append(str(self.logical_stack.pop()))
        remaining = [str(entry) for entry in reversed(self.logical_stack)]
        exc.traceback_lines = (
            ["逻辑调用轨迹（最近的调用在最前）："]
            + ["  " + s for s in exc.trace_entries + remaining])
        raise exc

    # ---- 运算 ----
    def _binop(self, op, left, right, span):
        if op in ("==", "!="):
            equal = type(left) is type(right) and left == right
            return equal if op == "==" else not equal
        if type(left) is not int or type(right) is not int:
            raise LangError("运算符 {} 需要整数操作数".format(op), span)
        if op == "+":
            return left + right
        if op == "-":
            return left - right
        if op == "*":
            return left * right
        if op == "/":
            if right == 0:
                raise LangError("除数为零", span)
            return left // right
        if op == "%":
            if right == 0:
                raise LangError("除数为零", span)
            return left % right
        if op == "<":
            return left < right
        if op == "<=":
            return left <= right
        if op == ">":
            return left > right
        if op == ">=":
            return left >= right
        raise LangError("未知运算符：{}".format(op), span)

    def _unary(self, op, value, span):
        if op == "-":
            if type(value) is not int:
                raise LangError("取负运算需要整数", span)
            return -value
        if op == "not":
            return not _truthy(value)
        raise LangError("未知一元运算符：{}".format(op), span)

    # ---- 辅助 ----
    def _emit(self, value):
        self.output.append(value)
        return value

    def _trace(self, depth, message):
        if self.trace_enabled:
            self.trace_log.append("{}{}".format("  " * depth, message))


def run(program, trace=False):
    """运行程序并返回 Machine；结果在 machine.result，错误在 machine.error。"""
    machine = Machine(program, trace=trace)
    try:
        machine.result = machine.execute(program.main)
    except LangError as exc:
        machine.error = exc
    return machine
