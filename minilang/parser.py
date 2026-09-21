"""递归下降解析器。

文法:
    program  := decl*
    decl     := "def" IDENT "(" params ")" "=" expr ";"   # 函数声明
              | "let" IDENT "=" expr ";"                  # 变量声明
    params   := IDENT ("," IDENT)*
    expr     := additive
    additive := multiplicative (("+"|"-") multiplicative)*
    multiplicative := unary (("*"|"/") unary)*
    unary    := "-" unary | primary
    primary  := NUMBER | IDENT | IDENT "(" args ")" | "(" expr ")"
    args     := expr ("," expr)*

增量复用:``reusable`` 形如 {新字节起点: (旧声明节点, 占用的 token 数)}。
解析到顶层声明时,若当前 token 起点命中表项,直接复用旧节点并跳过
对应数量的 token,不重建该声明的任何子节点。

错误恢复:未闭合括号、缺少分隔符、顶层意外内容都会产生明确的
ErrorNode,并同步到 ';' / 'def' / 'let' 后继续解析后续声明。

节点 offset 约定:解析过程中节点先携带绝对起点,父节点组装时把
直接子节点的 offset 减去自身起点,转为相对偏移(见 nodes.py)。
"""

from .lexer import lex
from .nodes import (Binary, Call, ErrorNode, FuncDecl, Ident, LetDecl, Number,
                    Program, Unary)

SYNC_KINDS = ("EOF", "SEMI", "DEF", "LET")


class Parser:
    def __init__(self, tokens, reusable=None):
        self.tokens = tokens
        self.i = 0
        self.errors = []
        self.reusable = reusable or {}

    def peek(self):
        return self.tokens[self.i]

    def at(self, kind):
        return self.tokens[self.i].kind == kind

    def advance(self):
        tok = self.tokens[self.i]
        if tok.kind != "EOF":
            self.i += 1
        return tok

    def last_end(self):
        if self.i == 0:
            return 0
        return self.tokens[self.i - 1].end

    def error(self, message, start, end):
        self.errors.append((message, start, end))

    # ---------- 顶层 ----------

    def parse_program(self):
        decls = []
        gaps = []
        prev_end = 0
        while not self.at("EOF"):
            tok = self.peek()
            gaps.append(tok.start - prev_end)
            decl = self.try_reuse()
            if decl is None:
                decl = self.parse_decl()
            decls.append(decl)
            prev_end = tok.start + decl.length
        return Program(decls, gaps, self.tokens[-1].start)

    def try_reuse(self):
        entry = self.reusable.get(self.peek().start)
        if entry is None:
            return None
        decl, token_count = entry
        if token_count <= 0 or self.i + token_count > len(self.tokens) - 1:
            return None
        for _ in range(token_count):
            self.advance()
        return decl

    def parse_decl(self):
        if self.at("DEF"):
            return self.parse_func()
        if self.at("LET"):
            return self.parse_let()
        return self.recover_decl("顶层出现意外的内容")

    def recover_decl(self, message):
        """从当前位置同步到 ';' / 'def' / 'let' / EOF,产出错误节点。"""
        start = self.peek().start
        while self.peek().kind not in SYNC_KINDS:
            self.advance()
        if self.at("SEMI"):
            self.advance()
        end = self.last_end()
        self.error(message, start, end)
        return ErrorNode(start, end - start, message, ())

    def recover_decl_from(self, start, message):
        """声明中途出错:丢弃已解析部分,同步后整体记为错误节点。"""
        while self.peek().kind not in SYNC_KINDS:
            self.advance()
        if self.at("SEMI"):
            self.advance()
        end = self.last_end()
        self.error(message, start, end)
        return ErrorNode(start, end - start, message, ())

    # ---------- 声明 ----------

    def parse_func(self):
        start = self.advance().start  # 'def'
        extras = []
        name = ""
        if self.at("IDENT"):
            name = self.advance().text
        else:
            extras.append(self.zero_error("函数缺少名称"))
        if self.at("LPAREN"):
            self.advance()
            params, ok = self.parse_params()
            if not ok:
                return self.recover_decl_from(start, "参数列表缺少右括号 ')'")
        else:
            params = ()
            extras.append(self.zero_error("函数缺少参数列表 '('"))
        if self.at("EQ"):
            self.advance()
        else:
            extras.append(self.zero_error("函数缺少 '='"))
        body = self.parse_expr()
        body.offset -= start
        if self.at("SEMI"):
            end = self.advance().end
        else:
            extras.append(self.zero_error("缺少 ';'"))
            end = self.last_end()
        for extra in extras:
            extra.offset -= start
        return FuncDecl(start, end - start, name, params, body, tuple(extras))

    def parse_params(self):
        params = []
        if not self.at("RPAREN"):
            if not self.at("IDENT"):
                return (), False
            params.append(self.advance().text)
            while self.at("COMMA"):
                self.advance()
                if not self.at("IDENT"):
                    return (), False
                params.append(self.advance().text)
        if self.at("RPAREN"):
            self.advance()
            return tuple(params), True
        return (), False

    def parse_let(self):
        start = self.advance().start  # 'let'
        extras = []
        name = ""
        if self.at("IDENT"):
            name = self.advance().text
        else:
            extras.append(self.zero_error("let 缺少变量名"))
        if self.at("EQ"):
            self.advance()
        else:
            extras.append(self.zero_error("let 缺少 '='"))
        value = self.parse_expr()
        value.offset -= start
        if self.at("SEMI"):
            end = self.advance().end
        else:
            extras.append(self.zero_error("缺少 ';'"))
            end = self.last_end()
        for extra in extras:
            extra.offset -= start
        return LetDecl(start, end - start, name, value, tuple(extras))

    def zero_error(self, message):
        here = self.peek().start
        self.error(message, here, here)
        return ErrorNode(here, 0, message, ())

    # ---------- 表达式 ----------

    def parse_expr(self):
        return self.parse_additive()

    def parse_additive(self):
        left = self.parse_multiplicative()
        while self.peek().kind in ("PLUS", "MINUS"):
            op = self.advance().text
            right = self.parse_multiplicative()
            left = self.make_binary(op, left, right)
        return left

    def parse_multiplicative(self):
        left = self.parse_unary()
        while self.peek().kind in ("STAR", "SLASH"):
            op = self.advance().text
            right = self.parse_unary()
            left = self.make_binary(op, left, right)
        return left

    def make_binary(self, op, left, right):
        start = left.offset
        end = right.offset + right.length
        left.offset -= start
        right.offset -= start
        return Binary(start, end - start, op, left, right)

    def parse_unary(self):
        if self.at("MINUS"):
            op = self.advance()
            operand = self.parse_unary()
            operand.offset -= op.start
            return Unary(op.start, operand.offset + operand.length, "-", operand)
        return self.parse_primary()

    def parse_primary(self):
        tok = self.peek()
        if tok.kind == "NUMBER":
            self.advance()
            value = float(tok.text) if "." in tok.text else int(tok.text)
            return Number(tok.start, tok.end - tok.start, value)
        if tok.kind == "IDENT":
            self.advance()
            ident = Ident(tok.start, tok.end - tok.start, tok.text)
            if self.at("LPAREN"):
                return self.finish_call(ident)
            return ident
        if tok.kind == "LPAREN":
            lp = self.advance()
            inner = self.parse_expr()
            if self.at("RPAREN"):
                self.advance()
                return inner
            end = self.last_end()
            message = "缺少右括号 ')'"
            self.error(message, lp.start, end)
            inner.offset -= lp.start
            return ErrorNode(lp.start, end - lp.start, message, (inner,))
        if tok.kind == "BAD":
            self.advance()
            message = f"无法识别的字符 {tok.text!r}"
            self.error(message, tok.start, tok.end)
            return ErrorNode(tok.start, tok.end - tok.start, message, ())
        message = "表达式意外结束" if tok.kind == "EOF" else f"此处不应出现 {tok.text!r}"
        self.error(message, tok.start, tok.end)
        if tok.kind in ("EOF", "RPAREN", "SEMI", "COMMA"):
            return ErrorNode(tok.start, 0, message, ())
        self.advance()
        return ErrorNode(tok.start, tok.end - tok.start, message, ())

    def finish_call(self, ident):
        self.advance()  # '('
        args = []
        if not self.at("RPAREN"):
            args.append(self.parse_expr())
            while self.at("COMMA"):
                self.advance()
                args.append(self.parse_expr())
        start = ident.offset
        if self.at("RPAREN"):
            end = self.advance().end
            return self.make_call(ident, args, start, end)
        end = self.last_end()
        message = "调用缺少右括号 ')'"
        self.error(message, start, end)
        call = self.make_call(ident, args, start, end)
        call.offset -= start
        return ErrorNode(start, end - start, message, (call,))

    def make_call(self, ident, args, start, end):
        ident.offset -= start
        for arg in args:
            arg.offset -= start
        return Call(start, end - start, ident, tuple(args))


def parse_text(text, reusable=None):
    """解析源码,返回 (program, tokens, errors)。"""
    tokens, lex_errors = lex(text)
    parser = Parser(tokens, reusable)
    program = parser.parse_program()
    return program, tokens, [*lex_errors, *parser.errors]


def parse(text):
    """全量解析,返回 (program, errors)。"""
    program, _, errors = parse_text(text)
    return program, errors
