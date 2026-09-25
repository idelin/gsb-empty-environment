from .ast_nodes import (
    Import, ConstDecl, FnDecl, IfStmt, WhileStmt, ReturnStmt, PrintStmt,
    ExprStmt, Num, Str, Bool, Nil, Var, Assign, Binary, Unary, Call,
)
from .errors import LangError

# Sentinel for "declared but not yet initialized" (temporal dead zone).
UNINITIALIZED = object()


class ReturnSignal(Exception):
    def __init__(self, value):
        self.value = value


class ImportRef:
    """Lazy binding to another module's export.

    Created when the target module is still initializing (import cycle).
    Resolved on every read, so hoisted functions become visible as soon
    as the target module hoists them, while reads of not-yet-initialized
    constants raise an error carrying the read chain.
    """

    __slots__ = ("module", "name")

    def __init__(self, module, name):
        self.module = module
        self.name = name


class Function:
    def __init__(self, decl, env, module):
        self.decl = decl
        self.env = env          # closure environment
        self.module = module    # defining module

    def call(self, interp, args, node):
        params = self.decl.params
        if len(args) != len(params):
            raise LangError(
                f"'{self.decl.name}' expects {len(params)} argument(s), "
                f"got {len(args)}", node.start, node.end)
        env = Env(self.env)
        for param, arg in zip(params, args):
            env.define(param, arg)
        prev = interp.current_module
        interp.current_module = self.module
        try:
            try:
                interp.exec_block(self.decl.body, env)
            except ReturnSignal as ret:
                return ret.value
            return None
        finally:
            interp.current_module = prev


class Env:
    def __init__(self, parent=None):
        self.vars = {}
        self.consts = set()
        self.parent = parent

    def define(self, name, value, is_const=False):
        self.vars[name] = value
        if is_const:
            self.consts.add(name)
        else:
            self.consts.discard(name)

    def lookup(self, name, start, end):
        env = self
        while env is not None:
            if name in env.vars:
                return env.vars[name]
            env = env.parent
        raise LangError(f"undefined name '{name}'", start, end)

    def assign(self, name, value, start, end):
        env = self
        while env is not None:
            if name in env.vars:
                if env.vars[name] is UNINITIALIZED:
                    raise LangError(
                        f"cannot assign to '{name}' before it is "
                        f"initialized", start, end)
                if name in env.consts:
                    raise LangError(
                        f"cannot assign to const '{name}'", start, end)
                env.vars[name] = value
                return
            env = env.parent
        raise LangError(f"undefined name '{name}'", start, end)


def fmt(value):
    if value is True:
        return "true"
    if value is False:
        return "false"
    if value is None:
        return "nil"
    if isinstance(value, Function):
        return f"<fn {value.decl.name}>"
    return str(value)


def is_num(value):
    return isinstance(value, (int, float)) and not isinstance(value, bool)


class Interpreter:
    def __init__(self, loader):
        self.loader = loader
        self.current_module = None
        # Stack of "module.name" labels for constants currently being
        # initialized; used to report the read chain on cycles.
        self.init_stack = []

    # ---- module initialization ----

    def init_module(self, module):
        env = module.env
        prev = self.current_module
        self.current_module = module
        try:
            # 1. Hoist function declarations: mutual recursion works,
            #    even across modules involved in an import cycle.
            for stmt in module.program.stmts:
                if isinstance(stmt, FnDecl):
                    fn = Function(stmt, env, module)
                    env.define(stmt.name, fn)
                    if stmt.exported:
                        module.exports[stmt.name] = fn
            # 2. Resolve imports. Modules in a cycle that are still
            #    initializing yield lazy ImportRef bindings.
            for stmt in module.program.stmts:
                if isinstance(stmt, Import):
                    target = self.loader.load_import(module, stmt)
                    self.loader.ensure_init(target)
                    for name in stmt.names:
                        if target.state == "initialized":
                            if name not in target.exports:
                                raise LangError(
                                    f"module '{target.name}' does not "
                                    f"export '{name}'", stmt.start, stmt.end)
                            env.define(name, target.exports[name])
                        else:
                            env.define(name, ImportRef(target, name))
            # 3. Pre-declare top-level const/var names as UNINITIALIZED
            #    so early reads are caught (temporal dead zone).
            for stmt in module.program.stmts:
                if isinstance(stmt, ConstDecl):
                    env.define(stmt.name, UNINITIALIZED)
            # 4. Run the remaining top-level statements in order.
            for stmt in module.program.stmts:
                if isinstance(stmt, (FnDecl, Import)):
                    continue
                self.exec_stmt(stmt, env)
        finally:
            self.current_module = prev

    # ---- statements ----

    def exec_block(self, stmts, env):
        for stmt in stmts:
            self.exec_stmt(stmt, env)

    def exec_stmt(self, stmt, env):
        if isinstance(stmt, ConstDecl):
            label = f"{self.current_module.name}.{stmt.name}"
            self.init_stack.append(label)
            try:
                value = self.eval(stmt.init, env)
            finally:
                self.init_stack.pop()
            env.define(stmt.name, value, is_const=not stmt.mutable)
            if stmt.exported:
                self.current_module.exports[stmt.name] = value
            return
        if isinstance(stmt, FnDecl):
            env.define(stmt.name, Function(stmt, env, self.current_module))
            return
        if isinstance(stmt, Import):
            raise LangError("import is only allowed at module top level",
                            stmt.start, stmt.end)
        if isinstance(stmt, IfStmt):
            if self.truthy(self.eval(stmt.cond, env)):
                self.exec_block(stmt.then, Env(env))
            else:
                self.exec_block(stmt.other, Env(env))
            return
        if isinstance(stmt, WhileStmt):
            while self.truthy(self.eval(stmt.cond, env)):
                self.exec_block(stmt.body, Env(env))
            return
        if isinstance(stmt, ReturnStmt):
            value = self.eval(stmt.value, env) if stmt.value is not None else None
            raise ReturnSignal(value)
        if isinstance(stmt, PrintStmt):
            text = " ".join(fmt(self.eval(arg, env)) for arg in stmt.args)
            self.loader.out(text)
            return
        if isinstance(stmt, ExprStmt):
            self.eval(stmt.expr, env)
            return
        raise AssertionError(f"unknown statement {stmt!r}")

    # ---- expressions ----

    def truthy(self, value):
        return bool(value)

    def deref(self, raw, node):
        if raw is UNINITIALIZED:
            raise LangError(
                f"cannot read '{node.name}' before it is initialized",
                node.start, node.end)
        if isinstance(raw, ImportRef):
            module = raw.module
            if raw.name in module.exports:
                return module.exports[raw.name]
            chain = " -> ".join(
                self.init_stack + [f"{module.name}.{raw.name}"])
            raise LangError(
                f"cannot read '{module.name}.{raw.name}': module "
                f"'{module.name}' is still initializing; "
                f"read chain: {chain}", node.start, node.end)
        return raw

    def eval(self, expr, env):
        if isinstance(expr, Num):
            return expr.value
        if isinstance(expr, Str):
            return expr.value
        if isinstance(expr, Bool):
            return expr.value
        if isinstance(expr, Nil):
            return None
        if isinstance(expr, Var):
            return self.deref(env.lookup(expr.name, expr.start, expr.end),
                              expr)
        if isinstance(expr, Assign):
            value = self.eval(expr.value, env)
            env.assign(expr.name, value, expr.start, expr.end)
            return value
        if isinstance(expr, Unary):
            return self.eval_unary(expr, env)
        if isinstance(expr, Binary):
            return self.eval_binary(expr, env)
        if isinstance(expr, Call):
            callee = self.eval(expr.callee, env)
            args = [self.eval(arg, env) for arg in expr.args]
            if not isinstance(callee, Function):
                raise LangError("attempt to call a non-function",
                                expr.start, expr.end)
            return callee.call(self, args, expr)
        raise AssertionError(f"unknown expression {expr!r}")

    def eval_unary(self, expr, env):
        operand = self.eval(expr.operand, env)
        if expr.op == "not":
            return not self.truthy(operand)
        if expr.op == "-":
            if not is_num(operand):
                raise LangError("unary '-' requires a number",
                                expr.start, expr.end)
            return -operand
        raise AssertionError(f"unknown unary op {expr.op!r}")

    def eval_binary(self, expr, env):
        if expr.op == "and":
            return (self.truthy(self.eval(expr.left, env))
                    and self.truthy(self.eval(expr.right, env)))
        if expr.op == "or":
            return (self.truthy(self.eval(expr.left, env))
                    or self.truthy(self.eval(expr.right, env)))
        left = self.eval(expr.left, env)
        right = self.eval(expr.right, env)
        op = expr.op
        if op == "==":
            return left == right
        if op == "!=":
            return left != right
        if op == "+":
            if isinstance(left, str) and isinstance(right, str):
                return left + right
            if is_num(left) and is_num(right):
                return left + right
            raise LangError("'+' requires two numbers or two strings",
                            expr.start, expr.end)
        if op in ("-", "*", "/", "%"):
            if not (is_num(left) and is_num(right)):
                raise LangError(f"'{op}' requires numbers",
                                expr.start, expr.end)
            if op == "-":
                return left - right
            if op == "*":
                return left * right
            if right == 0:
                raise LangError("division by zero", expr.start, expr.end)
            if op == "/":
                if isinstance(left, int) and isinstance(right, int) \
                        and left % right == 0:
                    return left // right
                return left / right
            return left % right
        if op in ("<", "<=", ">", ">="):
            both_num = is_num(left) and is_num(right)
            both_str = isinstance(left, str) and isinstance(right, str)
            if not (both_num or both_str):
                raise LangError(f"'{op}' requires two numbers or two "
                                f"strings", expr.start, expr.end)
            if op == "<":
                return left < right
            if op == "<=":
                return left <= right
            if op == ">":
                return left > right
            return left >= right
        raise AssertionError(f"unknown binary op {op!r}")
