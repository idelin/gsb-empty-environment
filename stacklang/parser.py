from .errors import ParseError, Span
from .lexer import tokenize
from . import ast_nodes as ast


class Parser:
    def __init__(self, tokens):
        self.tokens = tokens
        self.i = 0

    def peek(self, offset=0):
        return self.tokens[min(self.i + offset, len(self.tokens) - 1)]

    def take(self):
        tok = self.tokens[self.i]
        if self.i < len(self.tokens) - 1:
            self.i += 1
        return tok

    def expect(self, kind):
        tok = self.peek()
        if tok.kind != kind:
            raise ParseError(f"expected {kind} but got {tok.kind}",
                             Span(tok.start, tok.end))
        return self.take()

    def parse_program(self):
        start = self.peek().start
        stmts = []
        while self.peek().kind != "EOF":
            stmts.append(self.statement())
        end = self.peek().end
        return ast.Block(stmts, start, end)

    def statement(self):
        tok = self.peek()
        if tok.kind == "LET":
            return self.let_statement()
        if tok.kind == "IF":
            return self.if_statement()
        if tok.kind == "WHILE":
            return self.while_statement()
        if tok.kind == "BREAK":
            self.take()
            end = self.terminate_statement(tok.start)
            return ast.Break(tok.start, end)
        if tok.kind == "LBRACE":
            return self.block()
        return self.assignment_or_expr()

    def terminate_statement(self, start):
        tok = self.peek()
        if tok.kind == "SEMI":
            semi = self.take()
            return semi.end
        return start

    def let_statement(self):
        let = self.expect("LET")
        name = self.expect("NAME")
        self.expect("ASSIGN")
        value = self.expression()
        end = self.terminate_statement(value.end)
        return ast.Let(name.value, value, let.start, end)

    def assignment_or_expr(self):
        expr = self.expression()
        if self.peek().kind == "ASSIGN":
            if not isinstance(expr, ast.Name):
                eq = self.peek()
                raise ParseError("assignment target must be a name",
                                 Span(eq.start, eq.end))
            eq = self.take()
            value = self.expression()
            end = self.terminate_statement(value.end)
            return ast.Assign(expr.name, value, expr.start, end)
        end = self.terminate_statement(expr.end)
        return ast.ExprStmt(expr, expr.start, end)

    def block(self):
        lbrace = self.expect("LBRACE")
        stmts = []
        while self.peek().kind not in ("RBRACE", "EOF"):
            stmts.append(self.statement())
        self.expect("RBRACE")
        return ast.Block(stmts, lbrace.start, self.tokens[self.i - 1].end)

    def if_statement(self):
        if_tok = self.expect("IF")
        self.expect("LPAREN")
        cond = self.expression()
        self.expect("RPAREN")
        then_body = self.statement()
        otherwise = None
        if self.peek().kind == "ELSE":
            self.take()
            otherwise = self.statement()
        end = otherwise.end if otherwise is not None else then_body.end
        return ast.If(cond, then_body, otherwise, if_tok.start, end)

    def while_statement(self):
        while_tok = self.expect("WHILE")
        self.expect("LPAREN")
        cond = self.expression()
        self.expect("RPAREN")
        body = self.statement()
        return ast.While(cond, body, while_tok.start, body.end)

    def expression(self):
        return self.parse_or()

    def parse_or(self):
        node = self.parse_and()
        while self.peek().kind == "OR":
            tok = self.take()
            right = self.parse_and()
            node = ast.Binary("or", node, right, node.start, right.end)
        return node

    def parse_and(self):
        node = self.parse_equality()
        while self.peek().kind == "AND":
            tok = self.take()
            right = self.parse_equality()
            node = ast.Binary("and", node, right, node.start, right.end)
        return node

    def parse_equality(self):
        node = self.parse_comparison()
        while self.peek().kind in ("EQ", "NE"):
            tok = self.take()
            right = self.parse_comparison()
            op = "==" if tok.kind == "EQ" else "!="
            node = ast.Binary(op, node, right, node.start, right.end)
        return node

    def parse_comparison(self):
        node = self.parse_additive()
        while self.peek().kind in ("LT", "GT", "LE", "GE"):
            tok = self.take()
            right = self.parse_additive()
            op = {"LT": "<", "GT": ">", "LE": "<=", "GE": ">="}[tok.kind]
            node = ast.Binary(op, node, right, node.start, right.end)
        return node

    def parse_additive(self):
        node = self.parse_multiplicative()
        while self.peek().kind in ("PLUS", "MINUS"):
            tok = self.take()
            right = self.parse_multiplicative()
            op = "+" if tok.kind == "PLUS" else "-"
            node = ast.Binary(op, node, right, node.start, right.end)
        return node

    def parse_multiplicative(self):
        node = self.parse_unary()
        while self.peek().kind in ("STAR", "SLASH", "PERCENT"):
            tok = self.take()
            right = self.parse_unary()
            op = {"STAR": "*", "SLASH": "/", "PERCENT": "%"}[tok.kind]
            node = ast.Binary(op, node, right, node.start, right.end)
        return node

    def parse_unary(self):
        tok = self.peek()
        if tok.kind in ("MINUS", "BANG", "NOT"):
            self.take()
            operand = self.parse_unary()
            op = "!" if tok.kind in ("BANG", "NOT") else "-"
            return ast.Unary(op, operand, tok.start, operand.end)
        return self.parse_primary()

    def parse_primary(self):
        tok = self.peek()
        if tok.kind == "INT":
            self.take()
            return ast.IntLit(tok.value, tok.start, tok.end)
        if tok.kind in ("TRUE", "FALSE"):
            self.take()
            return ast.BoolLit(tok.kind == "TRUE", tok.start, tok.end)
        if tok.kind == "NAME":
            self.take()
            return ast.Name(tok.value, tok.start, tok.end)
        if tok.kind == "LPAREN":
            self.take()
            expr = self.expression()
            self.expect("RPAREN")
            end = self.tokens[self.i - 1].end
            expr.start = tok.start
            expr.end = end
            return expr
        raise ParseError(f"unexpected token {tok.kind}", Span(tok.start, tok.end))


def parse(source):
    return Parser(tokenize(source)).parse_program()
