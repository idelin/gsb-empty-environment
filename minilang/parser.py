"""Recursive-descent parser producing the AST defined in ``ast_nodes``."""

from __future__ import annotations

from . import ast_nodes as ast
from .errors import LangError
from .lexer import Token


class Parser:
    def __init__(self, tokens: list[Token]):
        self.tokens = tokens
        self.pos = 0

    # ---------- token helpers ----------

    def peek(self, ahead: int = 0) -> Token:
        return self.tokens[min(self.pos + ahead, len(self.tokens) - 1)]

    def advance(self) -> Token:
        tok = self.tokens[self.pos]
        if tok.kind != "EOF":
            self.pos += 1
        return tok

    def check(self, kind: str) -> bool:
        return self.peek().kind == kind

    def expect(self, kind: str, what: str) -> Token:
        tok = self.peek()
        if tok.kind != kind:
            raise LangError(f"expected {what}, found {tok.text!r}",
                            (tok.start, tok.end))
        return self.advance()

    # ---------- entry point ----------

    def parse_program(self) -> ast.Program:
        body = []
        while not self.check("EOF"):
            body.append(self.parse_statement())
        end = self.peek().start
        start = body[0].start if body else end
        return ast.Program(start, end, body)

    # ---------- statements ----------

    def parse_statement(self) -> ast.Node:
        kind = self.peek().kind
        if kind == "let":
            return self.parse_let()
        if kind == "fn":
            return self.parse_func_def()
        if kind == "if":
            return self.parse_if()
        if kind == "while":
            return self.parse_while()
        if kind == "return":
            return self.parse_return()
        if kind == "break":
            tok = self.advance()
            semi = self.expect("SEMI", "';'")
            return ast.Break(tok.start, semi.end)
        if kind == "continue":
            tok = self.advance()
            semi = self.expect("SEMI", "';'")
            return ast.Continue(tok.start, semi.end)
        if kind == "throw":
            return self.parse_throw()
        if kind == "try":
            return self.parse_try()
        if kind == "LBRACE":
            return self.parse_block()
        if kind == "IDENT" and self.peek(1).kind == "ASSIGN":
            name = self.advance()
            self.advance()  # '='
            value = self.parse_expression()
            semi = self.expect("SEMI", "';'")
            return ast.Assign(name.start, semi.end, name.text, value)
        expr = self.parse_expression()
        semi = self.expect("SEMI", "';'")
        return ast.ExprStmt(expr.start, semi.end, expr)

    def parse_block(self) -> ast.Block:
        open_tok = self.expect("LBRACE", "'{'")
        body = []
        while not self.check("RBRACE"):
            if self.check("EOF"):
                tok = self.peek()
                raise LangError("unterminated block", (tok.start, tok.end))
            body.append(self.parse_statement())
        close = self.advance()
        return ast.Block(open_tok.start, close.end, body)

    def parse_let(self) -> ast.Let:
        start = self.advance()  # 'let'
        name = self.expect("IDENT", "variable name")
        self.expect("ASSIGN", "'='")
        init = self.parse_expression()
        semi = self.expect("SEMI", "';'")
        return ast.Let(start.start, semi.end, name.text, init)

    def parse_func_def(self) -> ast.FuncDef:
        start = self.advance()  # 'fn'
        name = self.expect("IDENT", "function name")
        self.expect("LPAREN", "'('")
        params = []
        if not self.check("RPAREN"):
            params.append(self.expect("IDENT", "parameter name").text)
            while self.check("COMMA"):
                self.advance()
                params.append(self.expect("IDENT", "parameter name").text)
        self.expect("RPAREN", "')'")
        body = self.parse_block()
        return ast.FuncDef(start.start, body.end, name.text, params, body)

    def parse_if(self) -> ast.If:
        start = self.advance()  # 'if'
        self.expect("LPAREN", "'('")
        cond = self.parse_expression()
        self.expect("RPAREN", "')'")
        then = self.parse_block()
        otherwise = None
        end = then.end
        if self.check("else"):
            self.advance()
            if self.check("if"):
                otherwise = self.parse_if()
            else:
                otherwise = self.parse_block()
            end = otherwise.end
        return ast.If(start.start, end, cond, then, otherwise)

    def parse_while(self) -> ast.While:
        start = self.advance()  # 'while'
        self.expect("LPAREN", "'('")
        cond = self.parse_expression()
        self.expect("RPAREN", "')'")
        body = self.parse_block()
        return ast.While(start.start, body.end, cond, body)

    def parse_return(self) -> ast.Return:
        start = self.advance()  # 'return'
        if self.check("SEMI"):
            semi = self.advance()
            return ast.Return(start.start, semi.end, None)
        value = self.parse_expression()
        semi = self.expect("SEMI", "';'")
        return ast.Return(start.start, semi.end, value)

    def parse_throw(self) -> ast.Throw:
        start = self.advance()  # 'throw'
        value = self.parse_expression()
        semi = self.expect("SEMI", "';'")
        return ast.Throw(start.start, semi.end, value)

    def parse_try(self) -> ast.Try:
        start = self.advance()  # 'try'
        body = self.parse_block()
        catch_name = None
        catch_body = None
        finally_body = None
        end = body.end
        if self.check("catch"):
            self.advance()
            self.expect("LPAREN", "'('")
            catch_name = self.expect("IDENT", "catch variable").text
            self.expect("RPAREN", "')'")
            catch_body = self.parse_block()
            end = catch_body.end
        if self.check("finally"):
            self.advance()
            finally_body = self.parse_block()
            end = finally_body.end
        if catch_body is None and finally_body is None:
            raise LangError("try requires catch and/or finally",
                            (start.start, end))
        return ast.Try(start.start, end, body, catch_name, catch_body,
                       finally_body)

    # ---------- expressions ----------

    def parse_expression(self) -> ast.Node:
        return self.parse_or()

    def parse_or(self) -> ast.Node:
        left = self.parse_and()
        while self.check("OR"):
            self.advance()
            right = self.parse_and()
            left = ast.Logical(left.start, right.end, "||", left, right)
        return left

    def parse_and(self) -> ast.Node:
        left = self.parse_equality()
        while self.check("AND"):
            self.advance()
            right = self.parse_equality()
            left = ast.Logical(left.start, right.end, "&&", left, right)
        return left

    def parse_equality(self) -> ast.Node:
        left = self.parse_comparison()
        while self.peek().kind in ("EQ", "NE"):
            op = self.advance()
            right = self.parse_comparison()
            left = ast.Binary(left.start, right.end, op.text, left, right)
        return left

    def parse_comparison(self) -> ast.Node:
        left = self.parse_term()
        while self.peek().kind in ("LT", "LE", "GT", "GE"):
            op = self.advance()
            right = self.parse_term()
            left = ast.Binary(left.start, right.end, op.text, left, right)
        return left

    def parse_term(self) -> ast.Node:
        left = self.parse_factor()
        while self.peek().kind in ("PLUS", "MINUS"):
            op = self.advance()
            right = self.parse_factor()
            left = ast.Binary(left.start, right.end, op.text, left, right)
        return left

    def parse_factor(self) -> ast.Node:
        left = self.parse_unary()
        while self.peek().kind in ("STAR", "SLASH", "PERCENT"):
            op = self.advance()
            right = self.parse_unary()
            left = ast.Binary(left.start, right.end, op.text, left, right)
        return left

    def parse_unary(self) -> ast.Node:
        if self.peek().kind in ("BANG", "MINUS"):
            op = self.advance()
            operand = self.parse_unary()
            return ast.Unary(op.start, operand.end, op.text, operand)
        return self.parse_call()

    def parse_call(self) -> ast.Node:
        expr = self.parse_primary()
        while self.check("LPAREN"):
            self.advance()
            args = []
            if not self.check("RPAREN"):
                args.append(self.parse_expression())
                while self.check("COMMA"):
                    self.advance()
                    args.append(self.parse_expression())
            close = self.expect("RPAREN", "')'")
            expr = ast.Call(expr.start, close.end, expr, args)
        return expr

    def parse_primary(self) -> ast.Node:
        tok = self.peek()
        if tok.kind == "NUMBER":
            self.advance()
            return ast.Literal(tok.start, tok.end, tok.value)
        if tok.kind == "STRING":
            self.advance()
            return ast.Literal(tok.start, tok.end, tok.value)
        if tok.kind == "true":
            self.advance()
            return ast.Literal(tok.start, tok.end, True)
        if tok.kind == "false":
            self.advance()
            return ast.Literal(tok.start, tok.end, False)
        if tok.kind == "null":
            self.advance()
            return ast.Literal(tok.start, tok.end, None)
        if tok.kind == "IDENT":
            self.advance()
            return ast.Var(tok.start, tok.end, tok.text)
        if tok.kind == "LPAREN":
            self.advance()
            expr = self.parse_expression()
            self.expect("RPAREN", "')'")
            return expr
        raise LangError(f"expected expression, found {tok.text!r}",
                        (tok.start, tok.end))
