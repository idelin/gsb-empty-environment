"""递归下降语法分析器：从词法单元流构建 AST（自行实现，不用现成编译器）。

文法（优先级从低到高）：
    program  := def* expr EOF
    def      := "def" NAME "(" [NAME ("," NAME)*] ")" "=" expr
    expr     := "let" NAME "=" expr "in" expr
              | "if" expr "then" expr "else" expr
              | "try" expr "finally" expr
              | or_expr
    or_expr  := and_expr ("or" and_expr)*
    and_expr := not_expr ("and" not_expr)*
    not_expr := "not" not_expr | cmp_expr
    cmp_expr := add_expr (("=="|"!="|"<"|"<="|">"|">=") add_expr)?   # 非结合
    add_expr := mul_expr (("+"|"-") mul_expr)*
    mul_expr := unary (("*"|"/"|"%") unary)*
    unary    := "-" unary | postfix
    postfix  := primary ("(" [expr ("," expr)*] ")")*
    primary  := INT | STR | "true" | "false" | NAME | "(" expr ")"
"""

from .ast_nodes import (BinOp, BoolLit, Call, FunctionDef, If, IntLit, Let,
                        Program, StrLit, TryFinally, UnaryOp, Var)
from .errors import LangError
from .lexer import tokenize

_CMP_OPS = ("==", "!=", "<", "<=", ">", ">=")


class Parser:
    def __init__(self, source):
        self.tokens = tokenize(source)
        self.pos = 0

    # ---- 基本工具 ----
    def _peek(self):
        return self.tokens[self.pos]

    def _advance(self):
        tok = self.tokens[self.pos]
        self.pos += 1
        return tok

    def _check(self, kind, text=None):
        tok = self.tokens[self.pos]
        return tok.kind == kind and (text is None or tok.text == text)

    def _expect(self, kind, text=None):
        if not self._check(kind, text):
            tok = self._peek()
            want = text if text is not None else kind
            got = tok.text or tok.kind
            raise LangError(
                "语法错误：期望 {!r}，实际遇到 {!r}".format(want, got),
                (tok.start, tok.end))
        return self._advance()

    # ---- 程序与函数定义 ----
    def parse_program(self):
        defs = []
        while self._check("KW", "def"):
            defs.append(self._parse_def())
        main = self.parse_expr()
        self._expect("EOF")
        return Program(defs=defs, main=main)

    def _parse_def(self):
        start = self._expect("KW", "def").start
        name = self._expect("NAME").text
        self._expect("OP", "(")
        params = []
        if not self._check("OP", ")"):
            params.append(self._expect("NAME").text)
            while self._check("OP", ","):
                self._advance()
                params.append(self._expect("NAME").text)
        self._expect("OP", ")")
        self._expect("OP", "=")
        body = self.parse_expr()
        return FunctionDef(name=name, params=params, body=body,
                           span=(start, body.span[1]))

    # ---- 表达式 ----
    def parse_expr(self):
        tok = self._peek()
        if tok.kind == "KW":
            if tok.text == "let":
                return self._parse_let()
            if tok.text == "if":
                return self._parse_if()
            if tok.text == "try":
                return self._parse_try()
        return self._parse_or()

    def _parse_let(self):
        start = self._expect("KW", "let").start
        name = self._expect("NAME").text
        self._expect("OP", "=")
        value = self.parse_expr()
        self._expect("KW", "in")
        body = self.parse_expr()
        return Let(span=(start, body.span[1]), name=name, value=value, body=body)

    def _parse_if(self):
        start = self._expect("KW", "if").start
        cond = self.parse_expr()
        self._expect("KW", "then")
        then = self.parse_expr()
        self._expect("KW", "else")
        otherwise = self.parse_expr()
        return If(span=(start, otherwise.span[1]), cond=cond, then=then,
                  otherwise=otherwise)

    def _parse_try(self):
        start = self._expect("KW", "try").start
        body = self.parse_expr()
        self._expect("KW", "finally")
        finalizer = self.parse_expr()
        return TryFinally(span=(start, finalizer.span[1]), body=body,
                          finalizer=finalizer)

    def _parse_or(self):
        left = self._parse_and()
        while self._check("KW", "or"):
            self._advance()
            right = self._parse_and()
            left = BinOp(span=(left.span[0], right.span[1]), op="or",
                         left=left, right=right)
        return left

    def _parse_and(self):
        left = self._parse_not()
        while self._check("KW", "and"):
            self._advance()
            right = self._parse_not()
            left = BinOp(span=(left.span[0], right.span[1]), op="and",
                         left=left, right=right)
        return left

    def _parse_not(self):
        tok = self._peek()
        if tok.kind == "KW" and tok.text == "not":
            self._advance()
            operand = self._parse_not()
            return UnaryOp(span=(tok.start, operand.span[1]), op="not",
                           operand=operand)
        return self._parse_cmp()

    def _parse_cmp(self):
        left = self._parse_add()
        tok = self._peek()
        if tok.kind == "OP" and tok.text in _CMP_OPS:
            self._advance()
            right = self._parse_add()
            return BinOp(span=(left.span[0], right.span[1]), op=tok.text,
                         left=left, right=right)
        return left

    def _parse_add(self):
        left = self._parse_mul()
        while self._check("OP", "+") or self._check("OP", "-"):
            op = self._advance().text
            right = self._parse_mul()
            left = BinOp(span=(left.span[0], right.span[1]), op=op,
                         left=left, right=right)
        return left

    def _parse_mul(self):
        left = self._parse_unary()
        while (self._check("OP", "*") or self._check("OP", "/")
               or self._check("OP", "%")):
            op = self._advance().text
            right = self._parse_unary()
            left = BinOp(span=(left.span[0], right.span[1]), op=op,
                         left=left, right=right)
        return left

    def _parse_unary(self):
        tok = self._peek()
        if tok.kind == "OP" and tok.text == "-":
            self._advance()
            operand = self._parse_unary()
            return UnaryOp(span=(tok.start, operand.span[1]), op="-",
                           operand=operand)
        return self._parse_postfix()

    def _parse_postfix(self):
        expr = self._parse_primary()
        while self._check("OP", "("):
            self._advance()
            args = []
            if not self._check("OP", ")"):
                args.append(self.parse_expr())
                while self._check("OP", ","):
                    self._advance()
                    args.append(self.parse_expr())
            end = self._expect("OP", ")").end
            expr = Call(span=(expr.span[0], end), callee=expr, args=args)
        return expr

    def _parse_primary(self):
        tok = self._peek()
        if tok.kind == "INT":
            self._advance()
            return IntLit(span=(tok.start, tok.end), value=int(tok.text))
        if tok.kind == "STR":
            self._advance()
            return StrLit(span=(tok.start, tok.end), value=tok.text)
        if tok.kind == "KW" and tok.text in ("true", "false"):
            self._advance()
            return BoolLit(span=(tok.start, tok.end), value=tok.text == "true")
        if tok.kind == "NAME":
            self._advance()
            return Var(span=(tok.start, tok.end), name=tok.text)
        if tok.kind == "OP" and tok.text == "(":
            self._advance()
            expr = self.parse_expr()
            self._expect("OP", ")")
            return expr
        got = tok.text or tok.kind
        raise LangError("语法错误：此处不应出现 {!r}".format(got),
                        (tok.start, tok.end))


def parse(source):
    """把源码解析为 Program；语法错误抛出 LangError（带 UTF-8 字节区间）。"""
    return Parser(source).parse_program()
