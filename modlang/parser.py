"""Recursive-descent parser producing AST nodes with UTF-8 byte spans."""

from __future__ import annotations

from .ast_nodes import (
    Bin, Bool, Call, ConstDecl, ExprStmt, FnDecl, IfStmt, Import, Module,
    Name, Num, ReturnStmt, Str, Unary,
)
from .lexer import Token


class ParseError(Exception):
    def __init__(self, message: str, start: int, end: int):
        super().__init__(message)
        self.message = message
        self.start = start
        self.end = end


class Parser:
    def __init__(self, tokens: list[Token]):
        self.toks = tokens
        self.pos = 0

    def peek(self) -> Token:
        return self.toks[self.pos]

    def advance(self) -> Token:
        tok = self.toks[self.pos]
        if tok.kind != "eof":
            self.pos += 1
        return tok

    def check(self, text: str) -> bool:
        return self.peek().text == text

    def expect(self, text: str) -> Token:
        tok = self.peek()
        if tok.text != text:
            raise ParseError(f"expected '{text}', found {tok.text!r}", tok.start, tok.end)
        return self.advance()

    def expect_kind(self, kind: str) -> Token:
        tok = self.peek()
        if tok.kind != kind:
            raise ParseError(f"expected {kind}, found {tok.text!r}", tok.start, tok.end)
        return self.advance()

    def parse_module(self) -> Module:
        start = self.peek().start
        items = []
        while self.peek().kind != "eof":
            items.append(self.parse_item())
        return Module(items, (start, self.peek().end))

    def parse_item(self):
        exported = False
        start = None
        if self.check("export"):
            start = self.advance().start
            exported = True
        tok = self.peek()
        if tok.text == "import":
            if exported:
                raise ParseError("'import' cannot be exported", tok.start, tok.end)
            return self.parse_import()
        if tok.text == "const":
            return self.parse_const(exported, start)
        if tok.text == "fn":
            return self.parse_fn(exported, start)
        if exported:
            raise ParseError("expected 'const' or 'fn' after 'export'", tok.start, tok.end)
        return self.parse_stmt()

    def parse_import(self) -> Import:
        start = self.expect("import").start
        self.expect("{")
        names = [self.expect_kind("ident").text]
        while self.check(","):
            self.advance()
            names.append(self.expect_kind("ident").text)
        self.expect("}")
        self.expect("from")
        mod_tok = self.expect_kind("string")
        return Import(names, mod_tok.text, (start, mod_tok.end))

    def parse_const(self, exported: bool, start=None) -> ConstDecl:
        const_tok = self.expect("const")
        if start is None:
            start = const_tok.start
        name_tok = self.expect_kind("ident")
        self.expect("=")
        expr = self.parse_expr()
        return ConstDecl(name_tok.text, expr, exported, (start, expr.span[1]))

    def parse_fn(self, exported: bool, start=None) -> FnDecl:
        fn_tok = self.expect("fn")
        if start is None:
            start = fn_tok.start
        name_tok = self.expect_kind("ident")
        self.expect("(")
        params: list[str] = []
        if not self.check(")"):
            params.append(self.expect_kind("ident").text)
            while self.check(","):
                self.advance()
                params.append(self.expect_kind("ident").text)
        self.expect(")")
        body, end = self.parse_block()
        return FnDecl(name_tok.text, params, body, exported, (start, end))

    def parse_block(self):
        self.expect("{")
        stmts = []
        while not self.check("}"):
            if self.peek().kind == "eof":
                tok = self.peek()
                raise ParseError("unterminated block", tok.start, tok.end)
            stmts.append(self.parse_stmt())
        close = self.expect("}")
        return stmts, close.end

    def parse_stmt(self):
        tok = self.peek()
        if tok.text == "return":
            self.advance()
            expr = None if self.check("}") else self.parse_expr()
            end = expr.span[1] if expr is not None else tok.end
            return ReturnStmt(expr, (tok.start, end))
        if tok.text == "if":
            return self.parse_if()
        if tok.text == "const":
            return self.parse_const(False)
        expr = self.parse_expr()
        return ExprStmt(expr, expr.span)

    def parse_if(self) -> IfStmt:
        start = self.expect("if").start
        cond = self.parse_expr()
        then, end = self.parse_block()
        orelse = None
        if self.check("else"):
            self.advance()
            if self.check("if"):
                nested = self.parse_if()
                orelse = [nested]
                end = nested.span[1]
            else:
                orelse, end = self.parse_block()
        return IfStmt(cond, then, orelse, (start, end))

    def parse_expr(self):
        return self.parse_or()

    def _parse_binary(self, sub, ops):
        left = sub()
        while self.peek().text in ops:
            op = self.advance()
            right = sub()
            left = Bin(op.text, left, right, (left.span[0], right.span[1]))
        return left

    def parse_or(self):
        return self._parse_binary(self.parse_and, {"or"})

    def parse_and(self):
        return self._parse_binary(self.parse_equality, {"and"})

    def parse_equality(self):
        return self._parse_binary(self.parse_comparison, {"==", "!="})

    def parse_comparison(self):
        return self._parse_binary(self.parse_additive, {"<", "<=", ">", ">="})

    def parse_additive(self):
        return self._parse_binary(self.parse_multiplicative, {"+", "-"})

    def parse_multiplicative(self):
        return self._parse_binary(self.parse_unary, {"*", "/", "%"})

    def parse_unary(self):
        tok = self.peek()
        if tok.text in ("-", "not", "!"):
            self.advance()
            operand = self.parse_unary()
            return Unary(tok.text, operand, (tok.start, operand.span[1]))
        return self.parse_call()

    def parse_call(self):
        expr = self.parse_primary()
        while self.check("("):
            self.advance()
            args = []
            if not self.check(")"):
                args.append(self.parse_expr())
                while self.check(","):
                    self.advance()
                    args.append(self.parse_expr())
            close = self.expect(")")
            expr = Call(expr, args, (expr.span[0], close.end))
        return expr

    def parse_primary(self):
        tok = self.peek()
        if tok.kind == "number":
            self.advance()
            value = float(tok.text) if "." in tok.text else int(tok.text)
            return Num(value, (tok.start, tok.end))
        if tok.kind == "string":
            self.advance()
            return Str(tok.text, (tok.start, tok.end))
        if tok.text in ("true", "false"):
            self.advance()
            return Bool(tok.text == "true", (tok.start, tok.end))
        if tok.kind == "ident":
            self.advance()
            return Name(tok.text, (tok.start, tok.end))
        if tok.text == "(":
            self.advance()
            expr = self.parse_expr()
            self.expect(")")
            return expr
        raise ParseError(f"unexpected {tok.text!r}", tok.start, tok.end)
