"""Recursive-descent parser for Rill."""

from __future__ import annotations

from . import ast_nodes as ast
from .errors import CompileError
from .lexer import Token
from .types import Type

PRIMITIVES = {"Int", "Str", "Bool", "Unit"}


class Parser:
    def __init__(self, tokens: list[Token]):
        self.toks = tokens
        self.i = 0

    # -- token helpers ------------------------------------------------------

    @property
    def tok(self) -> Token:
        return self.toks[self.i]

    def peek(self, offset: int = 1) -> Token:
        j = min(self.i + offset, len(self.toks) - 1)
        return self.toks[j]

    def advance(self) -> Token:
        t = self.tok
        if self.i < len(self.toks) - 1:
            self.i += 1
        return t

    def accept(self, kind: str) -> Token | None:
        if self.tok.kind == kind:
            return self.advance()
        return None

    def expect(self, kind: str) -> Token:
        if self.tok.kind != kind:
            got = self.tok.value or self.tok.kind
            raise CompileError(f"expected {kind!r} but found {got!r}",
                               self.tok.start, self.tok.end)
        return self.advance()

    # -- grammar ------------------------------------------------------------

    def parse_program(self) -> ast.Program:
        prog = ast.Program()
        while self.tok.kind != "EOF":
            if self.tok.kind == "fn":
                fn = self.parse_function()
                if fn.name in prog.functions:
                    raise CompileError(f"function {fn.name!r} declared more than once",
                                       *fn.span)
                prog.functions[fn.name] = fn
                prog.order.append(fn.name)
            else:
                prog.main.stmts.append(self.parse_stmt())
        prog.main.span = (prog.main.stmts[0].span[0] if prog.main.stmts else 0,
                          prog.main.stmts[-1].span[1] if prog.main.stmts else 0)
        return prog

    def parse_function(self) -> ast.Function:
        start = self.expect("fn").start
        name = self.expect("NAME").value
        self.expect("(")
        params: list[ast.Param] = []
        seen: set[str] = set()
        while self.tok.kind != ")":
            mode_tok = self.expect_any("readonly", "writable")
            pname = self.expect("NAME").value
            if pname in seen:
                raise CompileError(f"duplicate parameter {pname!r}",
                                   mode_tok.start, mode_tok.end)
            seen.add(pname)
            self.expect(":")
            ptype = self.parse_type()
            params.append(ast.Param(pname, ptype, mode_tok.kind))
            if not self.accept(","):
                break
        self.expect(")")
        ret = Type("Unit")
        if self.accept(":"):
            ret = self.parse_type()
        body = self.parse_block()
        return ast.Function(name, params, ret, body, (start, body.span[1]))

    def expect_any(self, *kinds: str) -> Token:
        if self.tok.kind not in kinds:
            found = self.tok.value or self.tok.kind
            label = " or ".join(repr(k) for k in kinds)
            raise CompileError(f"parameter access mode must be {label}, found {found!r}",
                               self.tok.start, self.tok.end)
        return self.advance()

    def parse_type(self) -> Type:
        if self.tok.kind == "{":
            return self.parse_record_type()
        name_tok = self.expect("NAME")
        if name_tok.value not in PRIMITIVES:
            raise CompileError(f"unknown type {name_tok.value!r}",
                               name_tok.start, name_tok.end)
        return Type(name_tok.value)

    def parse_record_type(self) -> Type:
        start = self.expect("{").start
        fields: list[tuple[str, Type]] = []
        seen: set[str] = set()
        while self.tok.kind != "}":
            fname = self.expect("NAME").value
            if fname in seen:
                raise CompileError(f"duplicate field {fname!r} in record type",
                                   self.tok.start, self.tok.end)
            seen.add(fname)
            self.expect(":")
            ftype = self.parse_type()
            fields.append((fname, ftype))
            if not self.accept(","):
                break
        end = self.expect("}").end
        return Type.record(fields) if fields else Type("")

    def parse_block(self) -> ast.Block:
        start = self.expect("{").start
        stmts: list[ast.Stmt] = []
        while self.tok.kind != "}":
            if self.tok.kind == "EOF":
                raise CompileError("unterminated block, expected '}'",
                                   self.tok.start, self.tok.end)
            stmts.append(self.parse_stmt())
        end = self.expect("}").end
        return ast.Block((start, end), stmts)

    def parse_stmt(self) -> ast.Stmt:
        k = self.tok.kind
        if k == "let" or k == "var":
            return self.parse_let()
        if k == "if":
            return self.parse_if()
        if k == "while":
            return self.parse_while()
        if k == "return":
            return self.parse_return()
        if k == "{":
            return self.parse_block()
        if k == "extend":
            return self.parse_extend()
        return self.parse_expr_stmt()

    def parse_let(self) -> ast.Let:
        kw = self.advance()
        name = self.expect("NAME").value
        declared: Type | None = None
        if self.accept(":"):
            declared = self.parse_type()
        self.expect("=")
        value = self.parse_expr()
        self.opt_semi()
        return ast.Let((kw.start, value.span[1]), name, declared, value,
                       mutable=(kw.kind == "var"))

    def parse_if(self) -> ast.If:
        start = self.expect("if").start
        cond = self.parse_expr()
        then_block = self.parse_block()
        else_block = None
        if self.accept("else"):
            else_block = self.parse_block() if self.tok.kind == "{" else self.parse_if()
        return ast.If((start, (else_block or then_block).span[1]), cond,
                      then_block, else_block)

    def parse_while(self) -> ast.While:
        start = self.expect("while").start
        cond = self.parse_expr()
        body = self.parse_block()
        return ast.While((start, body.span[1]), cond, body)

    def parse_return(self) -> ast.Return:
        kw = self.expect("return")
        value = None
        if self.tok.kind != ";" and self.tok.kind != "}":
            value = self.parse_expr()
        end = value.span[1] if value else kw.end
        self.opt_semi()
        return ast.Return((kw.start, end), value)

    def parse_extend(self) -> ast.Extend:
        kw = self.expect("extend")
        target = self.parse_primary()
        # Consume dotted segments up to (but not including) the final one:
        # `extend a.b.c = v` means "extend a.b with field c".
        while self.tok.kind == "." and self.peek(1).kind == "NAME" \
                and self.peek(2).kind == ".":
            self.expect(".")
            name = self.expect("NAME")
            target = ast.Field((target.span[0], name.end), target, name.value,
                               (name.start, name.end))
        self.expect(".")
        field_tok = self.expect("NAME")
        self.expect("=")
        value = self.parse_expr()
        self.opt_semi()
        return ast.Extend((kw.start, value.span[1]), target, field_tok.value, value)

    def parse_expr_stmt(self) -> ast.Stmt:
        start = self.tok.start
        # assignment: lvalue "=" expr
        if self.tok.kind == "NAME" and self.peek().kind == "=":
            target_tok = self.advance()
            self.advance()  # '='
            value = self.parse_expr()
            self.opt_semi()
            return ast.Assign((start, value.span[1]),
                              ast.Var((target_tok.start, target_tok.end), target_tok.value),
                              value)
        expr = self.parse_expr()
        if self.accept("="):
            if not isinstance(expr, (ast.Var, ast.Field)):
                raise CompileError("assignment target must be a variable or field path",
                                   *expr.span)
            value = self.parse_expr()
            self.opt_semi()
            return ast.Assign((start, value.span[1]), expr, value)
        self.opt_semi()
        return ast.ExprStmt((start, expr.span[1]), expr)

    def opt_semi(self) -> None:
        self.accept(";")

    # -- expressions --------------------------------------------------------

    def parse_expr(self) -> ast.Expr:
        return self.parse_binary(0)

    PRECEDENCE = {
        "||": 1, "&&": 2,
        "==": 3, "!=": 3, "<": 4, ">": 4, "<=": 4, ">=": 4,
        "+": 5, "-": 5, "*": 6, "/": 6, "%": 6,
    }

    def parse_binary(self, min_prec: int) -> ast.Expr:
        left = self.parse_unary()
        while self.tok.kind in self.PRECEDENCE and self.PRECEDENCE[self.tok.kind] >= min_prec:
            op = self.advance()
            right = self.parse_binary(self.PRECEDENCE[op.kind] + 1)
            left = ast.Binary((left.span[0], right.span[1]), op.kind, left, right)
        return left

    def parse_unary(self) -> ast.Expr:
        if self.tok.kind in ("!", "-"):
            op = self.advance()
            operand = self.parse_unary()
            return ast.Unary((op.start, operand.span[1]), op.kind, operand)
        return self.parse_postfix(self.parse_primary())

    def parse_postfix(self, expr: ast.Expr) -> ast.Expr:
        while True:
            if self.accept("."):
                name = self.expect("NAME")
                expr = ast.Field((expr.span[0], name.end), expr, name.value,
                                 (name.start, name.end))
            elif self.accept("("):
                if not isinstance(expr, ast.Var):
                    raise CompileError("only named functions can be called",
                                       *expr.span)
                args: list[ast.Expr] = []
                while self.tok.kind != ")":
                    args.append(self.parse_expr())
                    if not self.accept(","):
                        break
                end = self.expect(")").end
                expr = ast.Call((expr.span[0], end), expr.name, args)
            else:
                return expr

    def parse_primary(self) -> ast.Expr:
        t = self.tok
        if t.kind == "INT":
            self.advance()
            return ast.IntLit((t.start, t.end), int(t.value))
        if t.kind == "STR":
            self.advance()
            return ast.StrLit((t.start, t.end), t.value)
        if t.kind in ("true", "false"):
            self.advance()
            return ast.BoolLit((t.start, t.end), t.kind == "true")
        if t.kind == "NAME":
            self.advance()
            return ast.Var((t.start, t.end), t.value)
        if t.kind == "{":
            return self.parse_record_lit()
        if t.kind == "(":
            self.advance()
            expr = self.parse_expr()
            self.expect(")")
            return expr
        raise CompileError(f"expected an expression but found {t.value or t.kind!r}",
                           t.start, t.end)

    def parse_record_lit(self) -> ast.Expr:
        start = self.expect("{").start
        fields: list[tuple[str, ast.Expr]] = []
        seen: set[str] = set()
        while self.tok.kind != "}":
            fname = self.expect("NAME").value
            if fname in seen:
                raise CompileError(f"duplicate field {fname!r} in record literal",
                                   self.tok.start, self.tok.end)
            seen.add(fname)
            self.expect(":")
            fexpr = self.parse_expr()
            fields.append((fname, fexpr))
            if not self.accept(","):
                break
        end = self.expect("}").end
        return ast.RecordLit((start, end), fields)
