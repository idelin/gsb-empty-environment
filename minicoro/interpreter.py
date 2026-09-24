"""树遍历解释器。

协程通过 Python 生成器驱动：语言层的 yield 对应生成器的挂起点，
挂起时整个解释器调用链（即协程的栈帧）由生成器自然保留；
close() 向挂起点抛入 GeneratorExit，解释器在展开过程中执行 finally 清理。
不使用 eval / compile 及任何现成编译器。
"""

from collections import namedtuple

from .ast_nodes import (
    Assign, BinOp, BoolLit, Break, Call, Continue, ExprStmt, FnDef, If, Index,
    IntLit, Let, ListLit, Name, NoneLit, Return, StrLit, TryFinally, UnaryOp,
    While, YieldExpr, contains_yield,
)
from .errors import LangError, ReentrancyError
from .parser import parse

# resume 的结果：done=False 表示协程挂出于 value；done=True 为明确终态。
Step = namedtuple("Step", ["done", "value"])


# ---- 控制流信号（解释器内部使用） ----

class _Signal(BaseException):
    pass


class ReturnSignal(_Signal):
    def __init__(self, value):
        self.value = value


class BreakSignal(_Signal):
    pass


class ContinueSignal(_Signal):
    pass


# ---- 运行时值 ----

class Env:
    """作用域链：每个协程/函数调用拥有独立的帧，局部变量互不污染。"""

    def __init__(self, parent=None):
        self.vars = {}
        self.parent = parent

    def declare(self, name, value):
        self.vars[name] = value

    def set(self, name, value):
        env = self
        while env is not None:
            if name in env.vars:
                env.vars[name] = value
                return
            env = env.parent
        self.vars[name] = value

    def get(self, name):
        env = self
        while env is not None:
            if name in env.vars:
                return env.vars[name]
            env = env.parent
        raise KeyError(name)


class Function:
    def __init__(self, node, closure):
        self.name = node.name
        self.params = node.params
        self.body = node.body
        self.closure = closure
        # 函数体（不含嵌套 fn）中出现 yield 即为协程函数
        self.is_coroutine = contains_yield(node.body)


class Builtin:
    def __init__(self, name, func):
        self.name = name
        self.func = func


def truthy(value):
    if value is None or value is False:
        return False
    if isinstance(value, int):
        return value != 0
    if isinstance(value, (str, list)):
        return len(value) > 0
    return True


def display(value, nested=False):
    if value is None:
        return "none"
    if value is True:
        return "true"
    if value is False:
        return "false"
    if isinstance(value, str):
        return f'"{value}"' if nested else value
    if isinstance(value, list):
        return "[" + ", ".join(display(item, True) for item in value) + "]"
    if isinstance(value, Function):
        return f"<fn {value.name}>"
    if isinstance(value, Coroutine):
        return f"<coroutine {value.func.name} state={value.state}>"
    return str(value)


# ---- 协程 ----

class Coroutine:
    """状态机：created -> running -> suspended / done / closed。

    - resume 返回 Step(done, value)；完成后再 resume 仍返回明确终态 Step(True, 返回值)。
    - running 状态下 resume/close 抛出 ReentrancyError，禁止运行中重入。
    - close 在挂起点展开清理栈，finally 恰好执行一次。
    """

    def __init__(self, interp, func, args):
        self.func = func
        self._gen = interp._function_main(func, args)
        self.state = "created"
        self.return_value = None
        self._started = False

    def resume(self, value=None):
        if self.state == "running":
            raise ReentrancyError(
                f"coroutine '{self.func.name}' is already running: re-entry forbidden"
            )
        if self.state in ("done", "closed"):
            return Step(True, self.return_value)
        self.state = "running"
        try:
            if not self._started:
                self._started = True
                yielded = next(self._gen)
            else:
                yielded = self._gen.send(value)
        except StopIteration as stop:
            self.state = "done"
            self.return_value = stop.value
            return Step(True, stop.value)
        except BaseException:
            # 语言异常（含 finally 清理后）向上传播，协程终结
            self.state = "done"
            raise
        self.state = "suspended"
        return Step(False, yielded)

    def close(self):
        if self.state == "running":
            raise ReentrancyError(
                f"coroutine '{self.func.name}' is already running: cannot close"
            )
        if self.state in ("done", "closed"):
            return
        try:
            self._gen.close()
        except LangError:
            self.state = "closed"
            raise
        except RuntimeError as exc:
            self.state = "closed"
            raise LangError(f"error during close: {exc}") from exc
        self.state = "closed"
        self.return_value = None


# ---- 解释器 ----

class Interpreter:
    def __init__(self):
        self.globals = Env()
        self._install_builtins()

    # -- 对外接口 --

    def run(self, source):
        """编译并执行顶层代码，返回最后一条语句的值。"""
        program = parse(source)
        gen = self.exec_block(program.stmts, self.globals)
        try:
            next(gen)
        except StopIteration as stop:
            return stop.value
        except ReturnSignal:
            raise LangError("return outside function") from None
        except (BreakSignal, ContinueSignal):
            raise LangError("break/continue outside loop") from None
        gen.close()
        raise LangError("yield outside coroutine")

    def get(self, name):
        try:
            return self.globals.get(name)
        except KeyError:
            raise LangError(f"undefined variable '{name}'") from None

    def set_global(self, name, value):
        self.globals.declare(name, value)

    def invoke(self, func, args):
        """调用函数值：协程函数返回 Coroutine，普通函数同步执行到底。"""
        if isinstance(func, Builtin):
            return func.func(args)
        if isinstance(func, Function):
            if len(args) != len(func.params):
                raise LangError(
                    f"{func.name} expects {len(func.params)} args, got {len(args)}"
                )
            if func.is_coroutine:
                return Coroutine(self, func, args)
            gen = self._function_main(func, args)
            try:
                next(gen)
            except StopIteration as stop:
                return stop.value
            gen.close()
            raise LangError(f"yield escaped non-coroutine function '{func.name}'")
        raise LangError(f"value is not callable: {display(func)}")

    # -- 函数调用帧 --

    def _function_main(self, func, args):
        if False:
            yield None  # 使其成为生成器
        frame = Env(func.closure)
        for param, arg in zip(func.params, args):
            frame.declare(param, arg)
        try:
            yield from self.exec_block(func.body, frame)
        except ReturnSignal as ret:
            return ret.value
        except (BreakSignal, ContinueSignal):
            raise LangError("break/continue outside loop") from None
        return None

    # -- 语句执行（生成器：语言层 yield 在此挂起） --

    def exec_block(self, stmts, env):
        if False:
            yield None
        result = None
        for stmt in stmts:
            result = yield from self.exec_stmt(stmt, env)
        return result

    def exec_stmt(self, node, env):
        if False:
            yield None
        if isinstance(node, Let):
            env.declare(node.name, (yield from self.eval_expr(node.expr, env)))
        elif isinstance(node, Assign):
            env.set(node.name, (yield from self.eval_expr(node.expr, env)))
        elif isinstance(node, ExprStmt):
            return (yield from self.eval_expr(node.expr, env))
        elif isinstance(node, FnDef):
            env.declare(node.name, Function(node, env))
        elif isinstance(node, If):
            for cond, body in node.arms:
                if truthy((yield from self.eval_expr(cond, env))):
                    return (yield from self.exec_block(body, Env(env)))
            if node.else_body is not None:
                return (yield from self.exec_block(node.else_body, Env(env)))
        elif isinstance(node, While):
            while truthy((yield from self.eval_expr(node.cond, env))):
                try:
                    yield from self.exec_block(node.body, Env(env))
                except BreakSignal:
                    break
                except ContinueSignal:
                    pass
        elif isinstance(node, TryFinally):
            try:
                yield from self.exec_block(node.body, Env(env))
            except BaseException:
                # 异常 / return / break / close(GeneratorExit) 均先执行 finally 再展开
                yield from self.exec_block(node.final, Env(env))
                raise
            yield from self.exec_block(node.final, Env(env))
        elif isinstance(node, Return):
            value = None
            if node.expr is not None:
                value = yield from self.eval_expr(node.expr, env)
            raise ReturnSignal(value)
        elif isinstance(node, Break):
            raise BreakSignal()
        elif isinstance(node, Continue):
            raise ContinueSignal()
        else:
            raise LangError(f"unknown statement {node!r}")
        return None

    # -- 表达式求值 --

    def eval_expr(self, node, env):
        if False:
            yield None
        if isinstance(node, IntLit) or isinstance(node, StrLit) or isinstance(node, BoolLit):
            return node.value
        if isinstance(node, NoneLit):
            return None
        if isinstance(node, Name):
            try:
                return env.get(node.name)
            except KeyError:
                raise LangError(
                    f"undefined variable '{node.name}'", node.start, node.end
                ) from None
        if isinstance(node, ListLit):
            items = []
            for item in node.items:
                items.append((yield from self.eval_expr(item, env)))
            return items
        if isinstance(node, UnaryOp):
            operand = yield from self.eval_expr(node.operand, env)
            if node.op == "not":
                return not truthy(operand)
            if type(operand) is not int:
                raise LangError("unary '-' expects int", node.start, node.end)
            return -operand
        if isinstance(node, BinOp):
            if node.op == "and":
                left = yield from self.eval_expr(node.left, env)
                if not truthy(left):
                    return False
                return truthy((yield from self.eval_expr(node.right, env)))
            if node.op == "or":
                left = yield from self.eval_expr(node.left, env)
                if truthy(left):
                    return True
                return truthy((yield from self.eval_expr(node.right, env)))
            left = yield from self.eval_expr(node.left, env)
            right = yield from self.eval_expr(node.right, env)
            return self._binop(node, left, right)
        if isinstance(node, YieldExpr):
            value = None
            if node.expr is not None:
                value = yield from self.eval_expr(node.expr, env)
            sent = yield value  # 协程在此挂起，栈帧由生成器保留
            return sent
        if isinstance(node, Call):
            func = yield from self.eval_expr(node.func, env)
            args = []
            for arg in node.args:
                args.append((yield from self.eval_expr(arg, env)))
            return self.invoke(func, args)
        if isinstance(node, Index):
            obj = yield from self.eval_expr(node.obj, env)
            index = yield from self.eval_expr(node.index, env)
            try:
                return obj[index]
            except (TypeError, IndexError, KeyError):
                raise LangError(
                    f"cannot index {display(obj)} with {display(index)}",
                    node.start, node.end,
                ) from None
        raise LangError(f"unknown expression {node!r}")

    def _binop(self, node, left, right):
        op = node.op
        if op == "==":
            return left == right
        if op == "!=":
            return left != right
        if op == "+" and isinstance(left, str) and isinstance(right, str):
            return left + right
        if op == "+" and isinstance(left, list) and isinstance(right, list):
            return left + right
        if type(left) is int and type(right) is int:
            if op == "+":
                return left + right
            if op == "-":
                return left - right
            if op == "*":
                return left * right
            if op == "/":
                if right == 0:
                    raise LangError("division by zero", node.start, node.end)
                return left // right
            if op == "%":
                if right == 0:
                    raise LangError("division by zero", node.start, node.end)
                return left % right
            if op == "<":
                return left < right
            if op == "<=":
                return left <= right
            if op == ">":
                return left > right
            if op == ">=":
                return left >= right
        if isinstance(left, str) and isinstance(right, str) and op in ("<", "<=", ">", ">="):
            return {"<": left < right, "<=": left <= right,
                    ">": left > right, ">=": left >= right}[op]
        raise LangError(
            f"bad operands for '{op}': {display(left)} and {display(right)}",
            node.start, node.end,
        )

    # -- 内建函数 --

    def _install_builtins(self):
        def b_print(args):
            print(" ".join(display(arg) for arg in args))
            return None

        def b_resume(args):
            if not args or not isinstance(args[0], Coroutine):
                raise LangError("resume expects a coroutine")
            value = args[1] if len(args) > 1 else None
            step = args[0].resume(value)
            return [step.done, step.value]

        def b_close(args):
            if not args or not isinstance(args[0], Coroutine):
                raise LangError("close expects a coroutine")
            args[0].close()
            return None

        def b_state(args):
            if not args or not isinstance(args[0], Coroutine):
                raise LangError("state expects a coroutine")
            return args[0].state

        def b_len(args):
            return len(args[0])

        def b_push(args):
            args[0].append(args[1])
            return None

        def b_str(args):
            return display(args[0]) if args else ""

        for name, func in [
            ("print", b_print), ("resume", b_resume), ("close", b_close),
            ("state", b_state), ("len", b_len), ("push", b_push), ("str", b_str),
        ]:
            self.globals.declare(name, Builtin(name, func))
