"""递归下降解析器。

文法（优先级从低到高）::

    program := ('let' name '=' expr)* expr
    expr    := 'let' ['rec'] binding {'and' binding} 'in' expr
             | 'if' expr 'then' expr 'else' expr
             | orExpr
    binding := name '=' expr
    orExpr  := andExpr {'||' andExpr}
    andExpr := cmpExpr {'&&' cmpExpr}
    cmpExpr := addExpr {('==' | '!=' | '<' | '>' | '<=' | '>=') addExpr}
    addExpr := mulExpr {('+' | '-') mulExpr}
    mulExpr := unary {('*' | '/' | '%') unary}
    unary   := '-' unary | atom
    atom    := INT | 'true' | 'false' | 'tick' '(' ')' | name | '(' expr ')'

顶层 ``let`` 与作为最终表达式的 ``let ... in`` 通过绑定值后面是否
紧跟 ``in`` 关键字消歧。
"""
from __future__ import annotations

from . import ast
from .errors import ParseError
from .lexer import Token, tokenize
from .sourcemap import Span

# (允许的运算符集合)；下标越大优先级越高。
_BINARY_LEVELS: tuple[tuple[str, ...], ...] = (
    ("||",),
    ("&&",),
    ("==", "!=", "<", ">", "<=", ">="),
    ("+", "-"),
    ("*", "/", "%"),
)


class Parser:
    def __init__(self, tokens: list[Token]) -> None:
        self.tokens = tokens
        self.index = 0

    # ---- 基础工具 ----
    def _peek(self, offset: int = 0) -> Token:
        return self.tokens[min(self.index + offset, len(self.tokens) - 1)]

    def _advance(self) -> Token:
        token = self.tokens[self.index]
        if token.kind != "EOF":
            self.index += 1
        return token

    def _check_keyword(self, value: str) -> bool:
        token = self._peek()
        return token.kind == "KEYWORD" and token.value == value

    def _expect_op(self, value: str) -> Token:
        token = self._peek()
        if token.kind != "OP" or token.value != value:
            raise ParseError(f"期望 '{value}'，但遇到 {self._describe(token)}", token.span)
        return self._advance()

    def _expect_keyword(self, value: str) -> Token:
        token = self._peek()
        if token.kind != "KEYWORD" or token.value != value:
            raise ParseError(
                f"期望关键字 '{value}'，但遇到 {self._describe(token)}", token.span
            )
        return self._advance()

    @staticmethod
    def _describe(token: Token) -> str:
        if token.kind == "EOF":
            return "输入结束"
        return f"'{token.value}'"

    # ---- 程序 ----
    def parse_program(self) -> ast.Program:
        start_span = self._peek().span
        bindings: list[ast.Binding] = []

        while self._check_keyword("let"):
            checkpoint = self.index
            self._advance()  # 'let'
            rec = False
            if self._check_keyword("rec"):
                rec = True
                self._advance()
            binding = self._parse_rest_of_binding()
            if rec or self._check_keyword("and") or self._check_keyword("in"):
                # 这是一个 let 表达式（可能是 rec/多绑定/普通 let-in）。
                self.index = checkpoint
                result = self._parse_let_expr()
                self._expect_end()
                return ast.Program(bindings, result, Span(start_span.start, result.span.end))
            # 普通顶层绑定
            bindings.append(binding)

        result = self.parse_expr()
        self._expect_end()
        return ast.Program(bindings, result, Span(start_span.start, result.span.end))

    def _expect_end(self) -> None:
        token = self._peek()
        if token.kind != "EOF":
            raise ParseError(f"表达式之后出现多余符号 {self._describe(token)}", token.span)

    # ---- 表达式 ----
    def parse_expr(self) -> ast.Expr:
        if self._check_keyword("let"):
            return self._parse_let_expr()
        if self._check_keyword("if"):
            return self._parse_if()
        return self._parse_binary(0)

    def _parse_let_expr(self) -> ast.Let:
        let_token = self._expect_keyword("let")
        rec = False
        if self._check_keyword("rec"):
            rec = True
            self._advance()

        bindings = [self._parse_rest_of_binding()]
        while self._check_keyword("and"):
            self._advance()
            bindings.append(self._parse_rest_of_binding())

        self._expect_keyword("in")
        body = self.parse_expr()
        return ast.Let(rec, bindings, body, Span(let_token.span.start, body.span.end))

    def _parse_rest_of_binding(self) -> ast.Binding:
        """在 'let' 已消费的前提下解析 ``name '=' expr``。"""
        start = self.tokens[max(0, self.index - 1)].span.start
        name_token = self._peek()
        if name_token.kind != "IDENT":
            raise ParseError(
                f"let 后需要绑定名，但遇到 {self._describe(name_token)}", name_token.span
            )
        self._advance()
        self._expect_op("=")
        value = self.parse_expr()
        return ast.Binding(
            name_token.value, value, name_token.span, Span(start, value.span.end)
        )

    def _parse_if(self) -> ast.If:
        if_token = self._expect_keyword("if")
        cond = self.parse_expr()
        self._expect_keyword("then")
        then_expr = self.parse_expr()
        self._expect_keyword("else")
        else_expr = self.parse_expr()
        return ast.If(
            cond, then_expr, else_expr,
            Span(if_token.span.start, else_expr.span.end),
        )

    def _parse_binary(self, level: int) -> ast.Expr:
        if level >= len(_BINARY_LEVELS):
            return self._parse_unary()
        left = self._parse_binary(level + 1)
        ops = _BINARY_LEVELS[level]
        while True:
            token = self._peek()
            if token.kind != "OP" or token.value not in ops:
                return left
            self._advance()
            right = self._parse_binary(level + 1)
            left = ast.BinOp(
                token.value, left, right, Span(left.span.start, right.span.end)
            )

    def _parse_unary(self) -> ast.Expr:
        token = self._peek()
        if token.kind == "OP" and token.value == "-":
            self._advance()
            operand = self._parse_unary()
            return ast.Neg(operand, Span(token.span.start, operand.span.end))
        return self._parse_atom()

    def _parse_atom(self) -> ast.Expr:
        token = self._peek()
        if token.kind == "INT":
            self._advance()
            return ast.IntLit(int(token.value), token.span)
        if token.kind == "KEYWORD" and token.value in ("true", "false"):
            self._advance()
            return ast.BoolLit(token.value == "true", token.span)
        if token.kind == "KEYWORD" and token.value == "tick":
            self._advance()
            self._expect_op("(")
            close = self._expect_op(")")
            return ast.Tick(Span(token.span.start, close.span.end))
        if token.kind == "IDENT":
            self._advance()
            return ast.Var(token.value, token.span)
        if token.kind == "OP" and token.value == "(":
            self._advance()
            expr = self.parse_expr()
            self._expect_op(")")
            return expr
        if token.kind == "EOF":
            raise ParseError("表达式意外结束", token.span)
        raise ParseError(f"这里不能出现 {self._describe(token)}，期望一个表达式", token.span)


def parse(source: str | bytes) -> ast.Program:
    return Parser(tokenize(source)).parse_program()
