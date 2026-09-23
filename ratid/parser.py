"""有理多项式表达式的词法与语法分析（递归下降，仅标准库）。

支持的语法（支持域之外一律 Unsupported，绝不猜测）：
  - 数字：非负整数与小数（如 12, 3.5），一元负号 -
  - 唯一变量：x
  - 二元运算：+ - * / ，幂：^ 与 **（右结合，指数必须是整数字面量）
  - 括号 ( )；空白任意
  - 禁止隐式乘法（2x）、禁止多变量、禁止函数符号

文法：
  expr   := term (('+' | '-') term)*
  term   := factor (('*' | '/') factor)*
  factor := atom ('^' | '**') factor | atom     # 右结合
  atom   := NUMBER | VARIABLE | '(' expr ')' | '-' atom
"""

from __future__ import annotations

from dataclasses import dataclass
from fractions import Fraction

from .errors import Unsupported

_TOKEN_PATTERN = None  # 不引入 re 以外的东西；这里手写扫描，零依赖


@dataclass(frozen=True)
class Node:
    kind: str               # num | var | neg | add | sub | mul | div | pow
    value: object = None    # num: Fraction；pow: int 指数
    left: "Node | None" = None
    right: "Node | None" = None


# ---------------------------------------------------------------------
# 词法
# ---------------------------------------------------------------------


@dataclass
class Token:
    kind: str
    text: str
    pos: int


def tokenize(text: str) -> list[Token]:
    tokens: list[Token] = []
    i, n = 0, len(text)
    while i < n:
        ch = text[i]
        if ch.isspace():
            i += 1
            continue
        if ch.isdigit() or (ch == "." and i + 1 < n and text[i + 1].isdigit()):
            j = i
            dot = False
            while j < n and (text[j].isdigit() or text[j] == "."):
                if text[j] == ".":
                    if dot:
                        raise Unsupported(f"位置 {j}：小数点重复")
                    dot = True
                j += 1
            word = text[i:j]
            if word == ".":
                raise Unsupported(f"位置 {i}：非法数字")
            tokens.append(Token("num", word, i))
            i = j
            continue
        if ch.isalpha() or ch == "_":
            j = i
            while j < n and (text[j].isalnum() or text[j] == "_"):
                j += 1
            tokens.append(Token("name", text[i:j], i))
            i = j
            continue
        if text.startswith("**", i):
            tokens.append(Token("op", "**", i))
            i += 2
            continue
        if ch == "^":
            tokens.append(Token("op", "^", i))
            i += 1
            continue
        if ch in "+-*/()":
            tokens.append(Token("op", ch, i))
            i += 1
            continue
        raise Unsupported(f"位置 {i}：无法识别的字符 {ch!r}")
    tokens.append(Token("eof", "", n))
    return tokens


# ---------------------------------------------------------------------
# 语法
# ---------------------------------------------------------------------


class Parser:
    def __init__(self, text: str):
        self.text = text
        self.tokens = tokenize(text)
        self.pos = 0

    def peek(self) -> Token:
        return self.tokens[self.pos]

    def next(self) -> Token:
        tok = self.tokens[self.pos]
        self.pos += 1
        return tok

    def expect(self, text: str) -> Token:
        tok = self.next()
        if tok.text != text:
            raise Unsupported(f"位置 {tok.pos}：期望 {text!r}，得到 {tok.text!r}")
        return tok

    def parse(self) -> Node:
        node = self.expr()
        if self.peek().kind != "eof":
            tok = self.peek()
            raise Unsupported(f"位置 {tok.pos}：表达式末尾有多余内容 {tok.text!r}")
        return node

    def expr(self) -> Node:
        node = self.term()
        while self.peek().text in ("+", "-"):
            op = self.next().text
            rhs = self.term()
            node = Node("add" if op == "+" else "sub", left=node, right=rhs)
        return node

    def term(self) -> Node:
        node = self.factor()
        while self.peek().text in ("*", "/"):
            op = self.next().text
            rhs = self.factor()
            node = Node("mul" if op == "*" else "div", left=node, right=rhs)
        return node

    def factor(self) -> Node:
        base = self.atom()
        if self.peek().text in ("^", "**"):
            op = self.next().text
            exponent_node = self.factor()  # 右结合
            exp_value = self._literal_int(exponent_node)
            return Node("pow", value=exp_value, left=base)
        return base

    def _literal_int(self, node: Node) -> int:
        """接受整数字面量、一元负号整数（如 -2）；底数必须是 x 本身。

        允许像 2^3 这样的整数常量折叠在指数位置（结果仍是整数指数），
        但不允许含变量的表达式作为指数。
        """
        if node.kind == "num" and node.value.denominator == 1:
            return int(node.value)
        if node.kind == "neg" and node.left is not None:
            return -self._literal_int(node.left)
        if node.kind == "pow" and node.left is not None and node.value is not None:
            base = self._literal_int(node.left)
            exp = int(node.value)
            if exp < 0:
                raise Unsupported("指数链中不允许负指数")
            return base ** exp
        raise Unsupported("幂指数必须是（可带负号的）整数字面量")

    def atom(self) -> Node:
        tok = self.peek()
        if tok.text == "-":
            self.next()
            return Node("neg", left=self.atom())
        if tok.text == "+":
            self.next()
            return self.atom()
        if tok.kind == "num":
            self.next()
            frac = Fraction(tok.text)
            return Node("num", value=frac)
        if tok.kind == "name":
            self.next()
            if tok.text != "x":
                raise Unsupported(f"位置 {tok.pos}：只支持单变量 x，发现 {tok.text!r}")
            return Node("var")
        if tok.text == "(":
            self.next()
            node = self.expr()
            self.expect(")")
            return node
        if tok.kind == "eof":
            raise Unsupported("表达式不完整：意外结束")
        raise Unsupported(f"位置 {tok.pos}：意外的符号 {tok.text!r}")


def parse(text: str) -> Node:
    """把表达式字符串解析成 AST。"""
    if not text or not text.strip():
        raise Unsupported("空表达式")
    return Parser(text).parse()
