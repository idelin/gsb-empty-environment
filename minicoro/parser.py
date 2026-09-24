"""递归下降语法分析器，产出的 AST 节点携带 UTF-8 字节区间。"""

from .ast_nodes import (
    Assign, BinOp, BoolLit, Break, Call, Continue, ExprStmt, FnDef, If, Index,
    IntLit, Let, ListLit, Name, NoneLit, Program, Return, StrLit, TryFinally,
    UnaryOp, While, YieldExpr,
)
from .errors import LangError
from .lexer import tokenize

_EXPR_START_KW = {"true", "false", "none", "not", "yield"}
_COMPARE_OPS = ("==", "!=", "<", "<=", ">", ">=")


def parse(source):
    """源码字符串 -> Program。"""
    return Parser(tokenize(source)).parse_program()


class Parser:
    def __init__(self, tokens):
        self.tokens = tokens
        self.pos = 0

    # ---- 基础工具 ----

    def peek(self, k=0):
        idx = min(self.pos + k, len(self.tokens) - 1)
        return self.tokens[idx]

    def advance(self):
        tok = self.tokens[self.pos]
        if tok.kind != "eof":
            self.pos += 1
        return tok

    def at_kw(self, text):
        tok = self.peek()
        return tok.kind == "kw" and tok.text == text

    def at_op(self, text):
        tok = self.peek()
        return tok.kind == "op" and tok.text == text

    def expect_kw(self, text):
        tok = self.peek()
        if tok.kind != "kw" or tok.text != text:
            raise LangError(f"expected '{text}', got {tok.text!r}", tok.start, tok.end)
        return self.advance()

    def expect_op(self, text):
        tok = self.peek()
        if tok.kind != "op" or tok.text != text:
            raise LangError(f"expected '{text}', got {tok.text!r}", tok.start, tok.end)
        return self.advance()

    def expect_name(self):
        tok = self.peek()
        if tok.kind != "name":
            raise LangError(f"expected name, got {tok.text!r}", tok.start, tok.end)
        return self.advance()

    def starts_expr(self):
        tok = self.peek()
        if tok.kind in ("num", "str", "name"):
            return True
        if tok.kind == "op" and tok.text in ("(", "[", "-"):
            return True
        return tok.kind == "kw" and tok.text in _EXPR_START_KW

    # ---- 程序与语句 ----

    def parse_program(self):
        stmts = []
        while self.peek().kind != "eof":
            stmts.append(self.parse_stmt())
        return Program(stmts)

    def parse_stmt(self):
        tok = self.peek()
        if tok.kind == "kw":
            if tok.text == "fn":
                return self.parse_fn()
            if tok.text == "let":
                return self.parse_let()
            if tok.text == "if":
                return self.parse_if()
            if tok.text == "while":
                return self.parse_while()
            if tok.text == "try":
                return self.parse_try()
            if tok.text == "return":
                return self.parse_return()
            if tok.text == "break":
                self.advance()
                return Break(tok.start, tok.end)
            if tok.text == "continue":
                self.advance()
                return Continue(tok.start, tok.end)
        if tok.kind == "name" and self.peek(1).kind == "op" and self.peek(1).text == "=":
            self.advance()
            self.advance()
            expr = self.parse_expr()
            return Assign(tok.text, expr, tok.start, expr.end)
        expr = self.parse_expr()
        return ExprStmt(expr, expr.start, expr.end)

    def parse_fn(self):
        start = self.expect_kw("fn").start
        name = self.expect_name().text
        self.expect_op("(")
        params = []
        if not self.at_op(")"):
            params.append(self.expect_name().text)
            while self.at_op(","):
                self.advance()
                params.append(self.expect_name().text)
        self.expect_op(")")
        body, end = self.parse_block()
        return FnDef(name, params, body, start, end)

    def parse_let(self):
        start = self.expect_kw("let").start
        name = self.expect_name().text
        self.expect_op("=")
        expr = self.parse_expr()
        return Let(name, expr, start, expr.end)

    def parse_if(self):
        start = self.expect_kw("if").start
        cond = self.parse_expr()
        body, end = self.parse_block()
        arms = [(cond, body)]
        else_body = None
        while self.at_kw("elif"):
            self.advance()
            arm_cond = self.parse_expr()
            arm_body, end = self.parse_block()
            arms.append((arm_cond, arm_body))
        if self.at_kw("else"):
            self.advance()
            else_body, end = self.parse_block()
        return If(arms, else_body, start, end)

    def parse_while(self):
        start = self.expect_kw("while").start
        cond = self.parse_expr()
        body, end = self.parse_block()
        return While(cond, body, start, end)

    def parse_try(self):
        start = self.expect_kw("try").start
        body, _ = self.parse_block()
        self.expect_kw("finally")
        final, end = self.parse_block()
        return TryFinally(body, final, start, end)

    def parse_return(self):
        tok = self.expect_kw("return")
        if self.starts_expr():
            expr = self.parse_expr()
            return Return(expr, tok.start, expr.end)
        return Return(None, tok.start, tok.end)

    def parse_block(self):
        self.expect_op("{")
        stmts = []
        while not self.at_op("}"):
            if self.peek().kind == "eof":
                tok = self.peek()
                raise LangError("unterminated block", tok.start, tok.end)
            stmts.append(self.parse_stmt())
        end = self.expect_op("}").end
        return stmts, end

    # ---- 表达式（优先级从低到高） ----

    def parse_expr(self):
        return self.parse_or()

    def parse_or(self):
        left = self.parse_and()
        while self.at_kw("or"):
            self.advance()
            right = self.parse_and()
            left = BinOp("or", left, right, left.start, right.end)
        return left

    def parse_and(self):
        left = self.parse_not()
        while self.at_kw("and"):
            self.advance()
            right = self.parse_not()
            left = BinOp("and", left, right, left.start, right.end)
        return left

    def parse_not(self):
        if self.at_kw("not"):
            tok = self.advance()
            operand = self.parse_not()
            return UnaryOp("not", operand, tok.start, operand.end)
        return self.parse_comparison()

    def parse_comparison(self):
        left = self.parse_additive()
        tok = self.peek()
        if tok.kind == "op" and tok.text in _COMPARE_OPS:
            self.advance()
            right = self.parse_additive()
            return BinOp(tok.text, left, right, left.start, right.end)
        return left

    def parse_additive(self):
        left = self.parse_multiplicative()
        while self.at_op("+") or self.at_op("-"):
            op = self.advance().text
            right = self.parse_multiplicative()
            left = BinOp(op, left, right, left.start, right.end)
        return left

    def parse_multiplicative(self):
        left = self.parse_unary()
        while self.at_op("*") or self.at_op("/") or self.at_op("%"):
            op = self.advance().text
            right = self.parse_unary()
            left = BinOp(op, left, right, left.start, right.end)
        return left

    def parse_unary(self):
        if self.at_op("-"):
            tok = self.advance()
            operand = self.parse_unary()
            return UnaryOp("-", operand, tok.start, operand.end)
        return self.parse_postfix()

    def parse_postfix(self):
        expr = self.parse_primary()
        while True:
            if self.at_op("("):
                self.advance()
                args = []
                if not self.at_op(")"):
                    args.append(self.parse_expr())
                    while self.at_op(","):
                        self.advance()
                        args.append(self.parse_expr())
                end = self.expect_op(")").end
                expr = Call(expr, args, expr.start, end)
            elif self.at_op("["):
                self.advance()
                index = self.parse_expr()
                end = self.expect_op("]").end
                expr = Index(expr, index, expr.start, end)
            else:
                return expr

    def parse_primary(self):
        tok = self.peek()
        if tok.kind == "num":
            self.advance()
            return IntLit(tok.value, tok.start, tok.end)
        if tok.kind == "str":
            self.advance()
            return StrLit(tok.value, tok.start, tok.end)
        if tok.kind == "name":
            self.advance()
            return Name(tok.text, tok.start, tok.end)
        if tok.kind == "kw":
            if tok.text == "true":
                self.advance()
                return BoolLit(True, tok.start, tok.end)
            if tok.text == "false":
                self.advance()
                return BoolLit(False, tok.start, tok.end)
            if tok.text == "none":
                self.advance()
                return NoneLit(tok.start, tok.end)
            if tok.text == "yield":
                self.advance()
                expr = self.parse_expr() if self.starts_expr() else None
                end = expr.end if expr is not None else tok.end
                return YieldExpr(expr, tok.start, end)
        if self.at_op("("):
            self.advance()
            expr = self.parse_expr()
            self.expect_op(")")
            return expr
        if self.at_op("["):
            start = self.advance().start
            items = []
            if not self.at_op("]"):
                items.append(self.parse_expr())
                while self.at_op(","):
                    self.advance()
                    items.append(self.parse_expr())
            end = self.expect_op("]").end
            return ListLit(items, start, end)
        raise LangError(f"unexpected token {tok.text!r}", tok.start, tok.end)
