from __future__ import annotations

from . import ast
from .errors import CompileError, Span, render_diagnostic
from .lexer import Lexer, Token


class Parser:
    def __init__(self, source: bytes, filename: str = "<input>"):
        self.source = source
        self.filename = filename
        self.tokens = Lexer(source, filename).tokenize()
        self.pos = 0

    def error(self, token: Token, message: str) -> CompileError:
        return CompileError([render_diagnostic(self.source, token.span, message, self.filename)])

    def peek(self, offset: int = 0) -> Token:
        return self.tokens[min(self.pos + offset, len(self.tokens) - 1)]

    def advance(self) -> Token:
        token = self.tokens[self.pos]
        if token.kind != "EOF":
            self.pos += 1
        return token

    def accept(self, kind: str) -> Token | None:
        if self.peek().kind == kind:
            return self.advance()
        return None

    def expect(self, kind: str, description: str) -> Token:
        token = self.peek()
        if token.kind != kind:
            raise self.error(token, f"expected {description}")
        return self.advance()

    def parse_program(self) -> ast.Program:
        statements: list[ast.Stmt] = []
        while self.peek().kind != "EOF":
            statements.append(self.parse_statement())
            self.accept(";")
        return ast.Program(statements)

    def parse_statement(self) -> ast.Stmt:
        token = self.peek()
        if token.kind == "KEYWORD" and token.text == "union":
            return self.parse_union()
        if token.kind == "KEYWORD" and token.text == "let":
            return self.parse_let()
        return self.parse_expression()

    def parse_union(self) -> ast.UnionDecl:
        start = self.expect("KEYWORD", "'union'").start
        name = self.expect("IDENT", "union name")
        self.expect("{", "'{'")
        constructors: list[ast.Constructor] = []
        if self.peek().kind != "}":
            while True:
                ctor = self.expect("IDENT", "constructor name")
                payload: list[ast.PType] = []
                if self.accept("("):
                    if self.peek().kind != ")":
                        payload.append(self.parse_type())
                        while self.accept(","):
                            payload.append(self.parse_type())
                    self.expect(")", "')'")
                constructors.append(ast.Constructor(ctor.text, payload, ctor.span))
                if not self.accept(","):
                    break
        end = self.expect("}", "'}'").end
        return ast.UnionDecl(name.text, constructors, Span(start, end))

    def parse_let(self) -> ast.LetStmt:
        start = self.expect("KEYWORD", "'let'").start
        name = self.expect("IDENT", "binding name")
        annotation = None
        if self.accept(":"):
            annotation = self.parse_type()
        self.expect("=", "'='")
        value = self.parse_expression()
        return ast.LetStmt(name.text, annotation, value, Span(start, value.span.end))

    def parse_type(self) -> ast.PType:
        token = self.peek()
        if token.kind == "(":
            start = token.start
            self.advance()
            elements: list[ast.PType] = []
            if self.peek().kind != ")":
                elements.append(self.parse_type())
                while self.accept(","):
                    elements.append(self.parse_type())
            end = self.expect(")", "')'").end
            if len(elements) == 1:
                return elements[0]
            return ast.TupleType(elements, Span(start, end))
        builtins = {"int", "string", "bool"}
        if token.kind == "IDENT" and token.text in builtins:
            self.advance()
            return ast.BuiltinType(token.text, token.span)
        if token.kind == "IDENT":
            self.advance()
            return ast.UserType(token.text, token.span)
        raise self.error(token, "expected a type")

    def parse_expression(self) -> ast.Expr:
        return self.parse_binary(0)

    def parse_binary(self, minimum_precedence: int) -> ast.Expr:
        left = self.parse_unary()
        while True:
            token = self.peek()
            precedence = binary_precedence(token.kind)
            if precedence < minimum_precedence:
                return left
            self.advance()
            right = self.parse_binary(precedence + 1)
            left = ast.BinaryExpr(token.kind, left, right, Span(left.span.start, right.span.end))

    def parse_unary(self) -> ast.Expr:
        token = self.peek()
        if token.kind in {"-", "!"}:
            self.advance()
            operand = self.parse_unary()
            return ast.UnaryExpr(token.kind, operand, Span(token.start, operand.span.end))
        return self.parse_postfix()

    def parse_postfix(self) -> ast.Expr:
        expr = self.parse_primary()
        return expr

    def parse_primary(self) -> ast.Expr:
        token = self.peek()
        if token.kind == "INT":
            self.advance()
            return ast.IntLiteral(int(token.text), token.span)
        if token.kind == "STRING":
            self.advance()
            return ast.StringLiteral(token.text, token.span)
        if token.kind == "KEYWORD" and token.text in {"true", "false"}:
            self.advance()
            return ast.BoolLiteral(token.text == "true", token.span)
        if token.kind == "KEYWORD" and token.text == "if":
            return self.parse_if()
        if token.kind == "KEYWORD" and token.text == "match":
            return self.parse_match()
        if token.kind == "IDENT":
            self.advance()
            if self.peek().kind == "(":
                return self.parse_tag_payload(token)
            if token.text[:1].isupper():
                return ast.TagExpr(token.text, [], token.span)
            return ast.Variable(token.text, token.span)
        if token.kind == "(":
            return self.parse_tuple_expression()
        raise self.error(token, "expected an expression")

    def parse_tag_payload(self, name: Token) -> ast.TagExpr:
        self.expect("(", "'('")
        payload: list[ast.Expr] = []
        if self.peek().kind != ")":
            payload.append(self.parse_expression())
            while self.accept(","):
                payload.append(self.parse_expression())
        end = self.expect(")", "')'").end
        return ast.TagExpr(name.text, payload, Span(name.start, end))

    def parse_tuple_expression(self) -> ast.Expr:
        start = self.expect("(", "'('").start
        first = self.parse_expression()
        if self.accept(","):
            elements = [first]
            if self.peek().kind != ")":
                elements.append(self.parse_expression())
                while self.accept(","):
                    elements.append(self.parse_expression())
            end = self.expect(")", "')'").end
            return ast.TupleExpr(elements, Span(start, end))
        end = self.expect(")", "')'").end
        if end == first.span.end:
            return first
        return first

    def parse_if(self) -> ast.Expr:
        start = self.expect("KEYWORD", "'if'").start
        condition = self.parse_expression()
        self.expect("KEYWORD", "'then'")
        consequent = self.parse_expression()
        self.expect("KEYWORD", "'else'")
        alternate = self.parse_expression()
        return ast.IfExpr(condition, consequent, alternate, Span(start, alternate.span.end))

    def parse_match(self) -> ast.MatchExpr:
        start = self.expect("KEYWORD", "'match'").start
        scrutinee = self.parse_expression()
        self.expect(":", "':'")
        scrutinee_type = self.parse_type()
        self.expect("{", "'{'")
        clauses: list[ast.Clause] = []
        if self.peek().kind != "}":
            while True:
                clauses.append(self.parse_clause())
                self.accept(",")
                if self.peek().kind == "}":
                    break
        end = self.expect("}", "'}'").end
        return ast.MatchExpr(scrutinee, scrutinee_type, clauses, Span(start, end))

    def parse_clause(self) -> ast.Clause:
        pattern = self.parse_pattern()
        guard = None
        if self.peek().kind == "|":
            self.advance()
            guard = self.parse_expression()
        self.expect("->", "'->'")
        body = self.parse_expression()
        return ast.Clause(pattern, guard, body, Span(pattern.span.start, body.span.end))

    def parse_pattern(self) -> ast.Pattern:
        token = self.peek()
        if token.kind == "-":
            self.advance()
            number = self.peek()
            if number.kind != "INT":
                raise self.error(number, "expected an integer literal after '-'")
            self.advance()
            return ast.LiteralPattern(-int(number.text), Span(token.start, number.end))
        if token.kind == "INT":
            self.advance()
            return ast.LiteralPattern(int(token.text), token.span)
        if token.kind == "STRING":
            self.advance()
            return ast.LiteralPattern(token.text, token.span)
        if token.kind == "KEYWORD" and token.text in {"true", "false"}:
            self.advance()
            return ast.LiteralPattern(token.text == "true", token.span)
        if token.kind == ".":
            self.advance()
            return ast.WildcardPattern(token.span)
        if token.kind == "IDENT":
            self.advance()
            if self.peek().kind == "(":
                return self.parse_tag_pattern(token)
            if token.text[:1].isupper():
                return ast.TagPattern(token.text, [], token.span)
            return ast.VariablePattern(token.text, token.span)
        if token.kind == "(":
            return self.parse_tuple_pattern()
        raise self.error(token, "expected a pattern")

    def parse_tag_pattern(self, name: Token) -> ast.TagPattern:
        self.expect("(", "'('")
        payload: list[ast.Pattern] = []
        if self.peek().kind != ")":
            payload.append(self.parse_pattern())
            while self.accept(","):
                payload.append(self.parse_pattern())
        end = self.expect(")", "')'").end
        return ast.TagPattern(name.text, payload, Span(name.start, end))

    def parse_tuple_pattern(self) -> ast.Pattern:
        start = self.expect("(", "'('").start
        first = self.parse_pattern()
        if self.accept(","):
            elements = [first]
            if self.peek().kind != ")":
                elements.append(self.parse_pattern())
                while self.accept(","):
                    elements.append(self.parse_pattern())
            end = self.expect(")", "')'").end
            return ast.TuplePattern(elements, Span(start, end))
        self.expect(")", "')'")
        return first


def binary_precedence(kind: str) -> int:
    if kind == "||":
        return 1
    if kind == "&&":
        return 2
    if kind in {"==", "!=", "<", "<=", ">", ">="}:
        return 3
    if kind in {"+", "-"}:
        return 4
    if kind in {"*", "/"}:
        return 5
    return -1


def parse(source: bytes, filename: str = "<input>") -> ast.Program:
    return Parser(source, filename).parse_program()
