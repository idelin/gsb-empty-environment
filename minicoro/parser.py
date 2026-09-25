"""Recursive-descent parser producing the AST defined in nodes.py."""

from .errors import LangError
from .nodes import (
    Assign, Binary, Call, ExprStmt, FnDef, If, Let, Literal, Program,
    Return, Throw, Try, Unary, Var, While, YieldExpr,
)


class Parser:
    def __init__(self, tokens):
        self.tokens = tokens
        self.i = 0

    # -- token helpers ----------------------------------------------------

    def _peek(self):
        return self.tokens[self.i]

    def _advance(self):
        tok = self.tokens[self.i]
        self.i += 1
        return tok

    def _check(self, kind, value=None):
        tok = self.tokens[self.i]
        if tok.kind != kind:
            return False
        return value is None or tok.value == value

    def _match(self, kind, value=None):
        if self._check(kind, value):
            return self._advance()
        return None

    def _expect(self, kind, value=None):
        tok = self._peek()
        if not self._check(kind, value):
            want = value if value is not None else kind
            got = tok.value if tok.value is not None else tok.kind
            raise LangError("expected %s, got %r" % (want, got), (tok.start, tok.end))
        return self._advance()

    # -- grammar ----------------------------------------------------------

    def parse_program(self):
        defs = []
        while not self._check("eof"):
            defs.append(self._parse_fn_def())
        return Program(defs, (0, self._peek().start))

    def _parse_fn_def(self):
        kw = self._expect("keyword", "fn")
        name = self._expect("ident").value
        self._expect("punct", "(")
        params = []
        if not self._check("punct", ")"):
            while True:
                params.append(self._expect("ident").value)
                if not self._match("punct", ","):
                    break
        self._expect("punct", ")")
        body, end = self._parse_block()
        return FnDef(name, params, body, (kw.start, end))

    def _parse_block(self):
        open_tok = self._expect("punct", "{")
        stmts = []
        while not self._check("punct", "}"):
            if self._check("eof"):
                raise LangError("unterminated block", (open_tok.start, open_tok.end))
            stmts.append(self._parse_stmt())
        close_tok = self._advance()
        return stmts, close_tok.end

    def _parse_stmt(self):
        tok = self._peek()
        if tok.kind == "keyword":
            if tok.value == "let":
                return self._parse_let()
            if tok.value == "if":
                return self._parse_if()
            if tok.value == "while":
                return self._parse_while()
            if tok.value == "return":
                return self._parse_return()
            if tok.value == "throw":
                return self._parse_throw()
            if tok.value == "try":
                return self._parse_try()
        expr = self._parse_expr()
        if self._match("op", "="):
            if not isinstance(expr, Var):
                raise LangError("invalid assignment target", expr.span)
            value = self._parse_expr()
            semi = self._expect("punct", ";")
            return Assign(expr.name, value, (expr.span[0], semi.end))
        semi = self._expect("punct", ";")
        return ExprStmt(expr, (expr.span[0], semi.end))

    def _parse_let(self):
        kw = self._expect("keyword", "let")
        name = self._expect("ident").value
        self._expect("op", "=")
        expr = self._parse_expr()
        semi = self._expect("punct", ";")
        return Let(name, expr, (kw.start, semi.end))

    def _parse_if(self):
        kw = self._expect("keyword", "if")
        cond = self._parse_expr()
        then_body, end = self._parse_block()
        else_body = None
        if self._match("keyword", "else"):
            if self._check("keyword", "if"):
                nested = self._parse_if()
                else_body = [nested]
                end = nested.span[1]
            else:
                else_body, end = self._parse_block()
        return If(cond, then_body, else_body, (kw.start, end))

    def _parse_while(self):
        kw = self._expect("keyword", "while")
        cond = self._parse_expr()
        body, end = self._parse_block()
        return While(cond, body, (kw.start, end))

    def _parse_return(self):
        kw = self._expect("keyword", "return")
        expr = None
        if not self._check("punct", ";"):
            expr = self._parse_expr()
        semi = self._expect("punct", ";")
        return Return(expr, (kw.start, semi.end))

    def _parse_throw(self):
        kw = self._expect("keyword", "throw")
        expr = self._parse_expr()
        semi = self._expect("punct", ";")
        return Throw(expr, (kw.start, semi.end))

    def _parse_try(self):
        kw = self._expect("keyword", "try")
        body, end = self._parse_block()
        catch_name = None
        catch_body = None
        finally_body = None
        if self._match("keyword", "catch"):
            catch_name = self._expect("ident").value
            catch_body, end = self._parse_block()
        if self._match("keyword", "finally"):
            finally_body, end = self._parse_block()
        if catch_body is None and finally_body is None:
            raise LangError("try requires catch and/or finally", (kw.start, end))
        return Try(body, catch_name, catch_body, finally_body, (kw.start, end))

    # -- expressions ------------------------------------------------------

    def _parse_expr(self):
        return self._parse_or()

    def _parse_or(self):
        left = self._parse_and()
        while self._check("keyword", "or"):
            self._advance()
            right = self._parse_and()
            left = Binary("or", left, right, (left.span[0], right.span[1]))
        return left

    def _parse_and(self):
        left = self._parse_comparison()
        while self._check("keyword", "and"):
            self._advance()
            right = self._parse_comparison()
            left = Binary("and", left, right, (left.span[0], right.span[1]))
        return left

    def _parse_comparison(self):
        left = self._parse_additive()
        while self._check("op") and self._peek().value in ("==", "!=", "<", "<=", ">", ">="):
            op = self._advance().value
            right = self._parse_additive()
            left = Binary(op, left, right, (left.span[0], right.span[1]))
        return left

    def _parse_additive(self):
        left = self._parse_multiplicative()
        while self._check("op") and self._peek().value in ("+", "-"):
            op = self._advance().value
            right = self._parse_multiplicative()
            left = Binary(op, left, right, (left.span[0], right.span[1]))
        return left

    def _parse_multiplicative(self):
        left = self._parse_unary()
        while self._check("op") and self._peek().value in ("*", "/", "%"):
            op = self._advance().value
            right = self._parse_unary()
            left = Binary(op, left, right, (left.span[0], right.span[1]))
        return left

    def _parse_unary(self):
        if self._check("op", "-") or self._check("keyword", "not"):
            tok = self._advance()
            operand = self._parse_unary()
            return Unary(tok.value, operand, (tok.start, operand.span[1]))
        return self._parse_postfix()

    def _parse_postfix(self):
        expr = self._parse_primary()
        while self._check("punct", "("):
            self._advance()
            args = []
            if not self._check("punct", ")"):
                while True:
                    args.append(self._parse_expr())
                    if not self._match("punct", ","):
                        break
            close = self._expect("punct", ")")
            expr = Call(expr, args, (expr.span[0], close.end))
        return expr

    def _parse_primary(self):
        tok = self._peek()
        if tok.kind == "int":
            self._advance()
            return Literal(tok.value, (tok.start, tok.end))
        if tok.kind == "string":
            self._advance()
            return Literal(tok.value, (tok.start, tok.end))
        if tok.kind == "keyword":
            if tok.value == "true":
                self._advance()
                return Literal(True, (tok.start, tok.end))
            if tok.value == "false":
                self._advance()
                return Literal(False, (tok.start, tok.end))
            if tok.value == "null":
                self._advance()
                return Literal(None, (tok.start, tok.end))
            if tok.value == "yield":
                self._advance()
                operand = self._parse_expr()
                return YieldExpr(operand, (tok.start, operand.span[1]))
        if tok.kind == "ident":
            self._advance()
            return Var(tok.value, (tok.start, tok.end))
        if self._match("punct", "("):
            expr = self._parse_expr()
            self._expect("punct", ")")
            return expr
        got = tok.value if tok.value is not None else tok.kind
        raise LangError("unexpected token %r" % got, (tok.start, tok.end))
