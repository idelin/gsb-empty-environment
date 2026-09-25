"""Semantic analysis: width subtyping, read/write variance, missing-field check.

Variance policy
---------------
* A ``readonly`` parameter occupies a *read* position: it is covariant in
  width, so ``{name: Str, age: Int}`` is acceptable where ``{name: Str}`` is
  expected (and nested records widen covariantly as well).  Writes through a
  read-only parameter are rejected.
* A ``writable`` parameter occupies a *mutation* position: the argument type
  must match the parameter type exactly (invariant).  A narrow record cannot be
  smuggled through an alias that later gets a field written which the original
  object lacks.
* Field access on a record whose static type lacks the field is rejected at
  compile time.  Adding a field requires the explicit ``extend`` statement,
  which flow-sensitively widens the local variable's record type.
"""

from __future__ import annotations

from dataclasses import dataclass

from . import ast_nodes as ast
from .errors import CompileError
from .types import BOOL, INT, STR, UNIT, Type, is_subtype, same_type


@dataclass
class Binding:
    name: str
    type: Type
    mutable: bool


class Scope:
    def __init__(self, parent: "Scope | None" = None, isolated: bool = False):
        self.parent = parent
        self.bindings: dict[str, Binding] = {}
        self.isolated = isolated  # branch scope: widenings must not escape

    def define(self, binding: Binding) -> None:
        self.bindings[binding.name] = binding

    def lookup(self, name: str) -> Binding | None:
        scope: Scope | None = self
        while scope is not None:
            if name in scope.bindings:
                return scope.bindings[name]
            scope = scope.parent
        return None

    def holder(self, name: str) -> "tuple[Scope, Binding] | None":
        scope: Scope | None = self
        while scope is not None:
            if name in scope.bindings:
                return scope, scope.bindings[name]
            scope = scope.parent
        return None

    def isolated_child(self) -> "Scope":
        # Copy every visible binding so branch-time widening stays local.
        child = Scope(self.parent, isolated=True)
        scope: Scope | None = self
        while scope is not None:
            for name, binding in scope.bindings.items():
                if name not in child.bindings:
                    child.bindings[name] = binding
            scope = scope.parent
        return child


class Checker:
    def __init__(self, program: ast.Program):
        self.program = program
        self.signatures: dict[str, ast.Function] = {}
        self.current_return = UNIT

    # -- entry points -------------------------------------------------------

    def check(self) -> None:
        for name in self.program.order:
            fn = self.program.functions[name]
            self.signatures[name] = fn
        for name in self.program.order:
            self.check_function(self.program.functions[name])
        top = Scope()
        self.check_block(self.program.main, top)

    def check_function(self, fn: ast.Function) -> None:
        scope = Scope()
        for param in fn.params:
            scope.define(Binding(param.name, param.type, mutable=param.mode == "writable"))
        self.current_return = fn.return_type
        self.check_block(fn.body, scope)
        self.current_return = UNIT

    # -- statements ---------------------------------------------------------

    def check_block(self, block: ast.Block, scope: Scope) -> Scope:
        inner = Scope(scope)
        for stmt in block.stmts:
            self.check_stmt(stmt, inner)
        return inner

    def check_stmt(self, stmt: ast.Stmt, scope: Scope) -> None:
        if isinstance(stmt, ast.Let):
            value_t = self.type_expr(stmt.value, scope)
            declared = stmt.declared if stmt.declared is not None else value_t
            if stmt.declared is not None and not is_subtype(value_t, declared, readonly=True):
                raise CompileError(
                    f"cannot initialise {stmt.name!r}: {value_t} is not {declared} "
                    f"(read position)", *stmt.value.span)
            scope.define(Binding(stmt.name, declared, mutable=stmt.mutable))
        elif isinstance(stmt, ast.Assign):
            self.check_assign(stmt, scope)
        elif isinstance(stmt, ast.Extend):
            self.check_extend(stmt, scope)
        elif isinstance(stmt, ast.If):
            cond_t = self.type_expr(stmt.cond, scope)
            if cond_t != BOOL:
                raise CompileError(f"if condition must be Bool, got {cond_t}", *stmt.cond.span)
            self.check_block(stmt.then_block, scope.isolated_child())
            if stmt.else_block is not None:
                self.check_block(stmt.else_block, scope.isolated_child())
        elif isinstance(stmt, ast.While):
            cond_t = self.type_expr(stmt.cond, scope)
            if cond_t != BOOL:
                raise CompileError(f"while condition must be Bool, got {cond_t}", *stmt.cond.span)
            self.check_block(stmt.body, scope.isolated_child())
        elif isinstance(stmt, ast.Return):
            if stmt.value is None:
                if self.current_return != UNIT:
                    raise CompileError(
                        f"function must return {self.current_return}", *stmt.span)
            else:
                value_t = self.type_expr(stmt.value, scope)
                if not is_subtype(value_t, self.current_return, readonly=True):
                    raise CompileError(
                        f"returning {value_t}, expected {self.current_return}",
                        *stmt.value.span)
        elif isinstance(stmt, ast.ExprStmt):
            self.type_expr(stmt.expr, scope)
        elif isinstance(stmt, ast.Block):
            self.check_block(stmt, scope)
        else:
            raise AssertionError(f"unhandled statement {type(stmt)}")

    def path_binding(self, target: ast.Expr, scope: Scope) -> Binding:
        root = target
        while isinstance(root, ast.Field):
            root = root.target
        if not isinstance(root, ast.Var):
            raise CompileError("invalid assignment path", *target.span)
        binding = scope.lookup(root.name)
        if binding is None or root.name in self.signatures:
            raise CompileError(f"unknown variable {root.name!r}", *root.span)
        return binding

    def path_writable(self, target: ast.Expr, scope: Scope) -> bool:
        # A field path is writable only if every link leads back to a mutable
        # root; a readonly binding freezes the whole reachable record.
        if isinstance(target, ast.Var):
            binding = scope.lookup(target.name)
            return binding is not None and binding.mutable
        if isinstance(target, ast.Field):
            return self.path_writable(target.target, scope)
        return False

    def check_assign(self, stmt: ast.Assign, scope: Scope) -> None:
        if not isinstance(stmt.target, (ast.Var, ast.Field)):
            raise CompileError("assignment target must be a variable or field path",
                               *stmt.target.span)
        target_t = self.type_expr(stmt.target, scope)
        if not self.path_writable(stmt.target, scope):
            raise CompileError(
                "cannot assign through a readonly binding; use a writable location",
                *stmt.target.span)
        value_t = self.type_expr(stmt.value, scope)
        if not is_subtype(value_t, target_t, readonly=True):
            raise CompileError(
                f"cannot assign {value_t} to {target_t}", *stmt.value.span)

    def check_extend(self, stmt: ast.Extend, scope: Scope) -> None:
        if not isinstance(stmt.target, (ast.Var, ast.Field)):
            raise CompileError("extend target must be a variable or field path",
                               *stmt.target.span)
        if not self.path_writable(stmt.target, scope):
            raise CompileError(
                "cannot extend a record through a readonly binding", *stmt.target.span)
        target_t = self.type_expr(stmt.target, scope)
        if not target_t.is_record:
            raise CompileError(f"can only extend records, got {target_t}", *stmt.target.span)
        if target_t.get(stmt.field) is not None:
            raise CompileError(
                f"field {stmt.field!r} already exists; assign it instead of extending",
                stmt.span[0], stmt.span[1])
        value_t = self.type_expr(stmt.value, scope)
        self.widen_path(stmt.target, stmt.field, value_t, scope)

    def widen_path(self, target: ast.Expr, field: str, ftype: Type, scope: Scope) -> None:
        # Flow-sensitively widen the root variable's record type.
        root = target
        chain: list[str] = []
        while isinstance(root, ast.Field):
            chain.append(root.name)
            root = root.target
        assert isinstance(root, ast.Var)
        found = scope.holder(root.name)
        assert found is not None
        holder, binding = found
        widened = self.add_field(binding.type, list(reversed(chain)), field, ftype)
        updated = Binding(binding.name, widened, binding.mutable)
        # If the path is used inside an isolated branch scope, install a shadow
        # copy there so the extra field is visible only within that branch.
        shadow: Scope | None = None
        cur: Scope | None = scope
        while cur is not None and cur is not holder.parent:
            if cur is not holder and cur.isolated:
                shadow = cur
            cur = cur.parent
        if shadow is not None:
            shadow.define(updated)
        else:
            holder.define(updated)

    def add_field(self, root_t: Type, path: list[str], field: str, ftype: Type) -> Type:
        if not path:
            fields = dict(root_t.field_map())
            fields[field] = ftype
            return Type.record(fields)
        head, *rest = path
        nested = root_t.get(head)
        if nested is None or not nested.is_record:
            raise CompileError(f"cannot extend through missing field {head!r}", 0, 0)
        new_nested = self.add_field(nested, rest, field, ftype)
        fields = dict(root_t.field_map())
        fields[head] = new_nested
        return Type.record(fields)

    # -- expressions --------------------------------------------------------

    def type_expr(self, expr: ast.Expr, scope: Scope) -> Type:
        if isinstance(expr, ast.IntLit):
            return INT
        if isinstance(expr, ast.StrLit):
            return STR
        if isinstance(expr, ast.BoolLit):
            return BOOL
        if isinstance(expr, ast.Var):
            if expr.name in self.signatures:
                raise CompileError("functions are not first-class values", *expr.span)
            binding = scope.lookup(expr.name)
            if binding is None:
                raise CompileError(f"unknown variable {expr.name!r}", *expr.span)
            return binding.type
        if isinstance(expr, ast.RecordLit):
            fields: list[tuple[str, Type]] = []
            for fname, fexpr in expr.fields:
                fields.append((fname, self.type_expr(fexpr, scope)))
            return Type.record(fields)
        if isinstance(expr, ast.Field):
            return self.check_field(expr, scope)
        if isinstance(expr, ast.Unary):
            inner = self.type_expr(expr.operand, scope)
            if expr.op == "!":
                if inner != BOOL:
                    raise CompileError(f"operator '!' needs Bool, got {inner}", *expr.span)
                return BOOL
            if inner != INT:
                raise CompileError(f"unary '-' needs Int, got {inner}", *expr.span)
            return INT
        if isinstance(expr, ast.Binary):
            return self.check_binary(expr, scope)
        if isinstance(expr, ast.Call):
            return self.check_call(expr, scope)
        raise AssertionError(f"unhandled expression {type(expr)}")

    def check_field(self, expr: ast.Field, scope: Scope) -> Type:
        target_t = self.type_expr(expr.target, scope)
        if not target_t.is_record:
            raise CompileError(f"value of type {target_t} has no fields", *expr.target.span)
        ftype = target_t.get(expr.name)
        if ftype is None:
            available = ", ".join(sorted(target_t.field_map())) or "<none>"
            raise CompileError(
                f"record has no field {expr.name!r} (known fields: {available}); "
                f"use 'extend' to add it", *expr.name_span)
        return ftype

    def check_binary(self, expr: ast.Binary, scope: Scope) -> Type:
        lt = self.type_expr(expr.left, scope)
        rt = self.type_expr(expr.right, scope)
        op = expr.op
        if op in ("+",):
            if lt == INT and rt == INT:
                return INT
            if lt == STR and rt == STR:
                return STR
            raise CompileError(f"operator '+' needs two Int or two Str, got {lt} and {rt}",
                               *expr.span)
        if op in ("-", "*", "/", "%"):
            if not (lt == INT and rt == INT):
                raise CompileError(f"operator {op!r} needs two Int, got {lt} and {rt}",
                                   *expr.span)
            return INT
        if op in ("<", ">", "<=", ">="):
            if not (lt == INT and rt == INT):
                raise CompileError(f"operator {op!r} needs two Int, got {lt} and {rt}",
                                   *expr.span)
            return BOOL
        if op in ("==", "!="):
            if not (same_type(lt, rt) or is_subtype(lt, rt, True) or is_subtype(rt, lt, True)):
                raise CompileError(f"cannot compare {lt} and {rt}", *expr.span)
            return BOOL
        if op in ("&&", "||"):
            if not (lt == BOOL and rt == BOOL):
                raise CompileError(f"operator {op!r} needs two Bool, got {lt} and {rt}",
                                   *expr.span)
            return BOOL
        raise AssertionError(op)

    def check_call(self, expr: ast.Call, scope: Scope) -> Type:
        if expr.callee == "print":
            if len(expr.args) != 1:
                raise CompileError("print expects exactly one argument", *expr.span)
            self.type_expr(expr.args[0], scope)
            return UNIT
        fn = self.signatures.get(expr.callee)
        if fn is None:
            raise CompileError(f"unknown function {expr.callee!r}()", *expr.span)
        if len(expr.args) != len(fn.params):
            raise CompileError(
                f"{fn.name}() expects {len(fn.params)} arguments, got {len(expr.args)}",
                *expr.span)
        for arg, param in zip(expr.args, fn.params):
            arg_t = self.type_expr(arg, scope)
            if param.mode == "readonly":
                if not is_subtype(arg_t, param.type, readonly=True):
                    raise CompileError(
                        f"readonly parameter {param.name!r} expects {param.type}, "
                        f"got {arg_t}", *arg.span)
            else:
                if not isinstance(arg, (ast.Var, ast.Field)):
                    raise CompileError(
                        f"writable parameter {param.name!r} needs a variable or field path",
                        *arg.span)
                if not self.path_writable(arg, scope):
                    raise CompileError(
                        f"writable parameter {param.name!r} needs a writable location; "
                        f"{param.name} is mutable but the argument is readonly",
                        *arg.span)
                if not same_type(arg_t, param.type):
                    raise CompileError(
                        f"writable parameter {param.name!r} must match {param.type} exactly "
                        f"(invariant), got {arg_t}; width widening is unsafe for mutation",
                        *arg.span)
        return fn.return_type
