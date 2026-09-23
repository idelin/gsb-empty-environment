"""词法分析器。

产生的 token 种类：

- ``("rat", (分子, 分母))``：有理字面量。整数是 ``("int", n)``；
  分数使用 Unicode 分数斜线 ``⁄``（U+2044）写成 ``p⁄q``，与二元
  除法 ``/`` 彻底分开，避免 ``1/2`` 到底是常数还是除法的歧义。
- ``("id", 名字)``：变量 / 函数名（字母或下划线开头）。
- ``("op", 符号)``：二元算符及一元符号。
- ``("?", ...) (":", ...) ("(", ...) (")", ...) (",", ...)``。

不支持浮点：出现 ``1.5`` 这类 ``数字 . 数字`` 一律 :class:`LexError`，
从源头杜绝浮点损失。
"""

from __future__ import annotations

from .errors import LexError
from .grammar import Grammar

FRAC_SLASH = "\u2044"
_INT_START = frozenset("0123456789")
_ID_START = frozenset("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ_")
_ID_PART = _ID_START | _INT_START


def tokenize(text: str, grammar: Grammar):
    tokens = []
    src = text
    i = 0
    n = len(src)
    op_symbols = tuple(op.symbol for op in grammar.operators) + (grammar.unary_symbol,)

    while i < n:
        ch = src[i]
        if ch in " \t\r\n":
            i += 1
            continue
        start = i

        if ch in _INT_START:
            j = i + 1
            while j < n and src[j] in _INT_START:
                j += 1
            if j < n and src[j] == ".":
                raise LexError(f"位置 {j}：不支持浮点字面量，只接受整数或 p{FRAC_SLASH}q")
            if j + 1 < n and src[j] == FRAC_SLASH and src[j + 1] in _INT_START:
                k = j + 2
                while k < n and src[k] in _INT_START:
                    k += 1
                if k < n and src[k] == ".":
                    raise LexError(f"位置 {k}：分母不能为浮点")
                num = int(src[i:j])
                den = int(src[j + 1 : k])
                if den == 0:
                    raise LexError(f"位置 {j}：有理字面量分母不能为 0")
                tokens.append(("rat", (num, den)))
                i = k
                continue
            tokens.append(("int", int(src[i:j])))
            i = j
            continue

        if ch in _ID_START:
            j = i + 1
            while j < n and src[j] in _ID_PART:
                j += 1
            tokens.append(("id", src[i:j]))
            i = j
            continue

        matched = None
        for sym in op_symbols:
            if src.startswith(sym, i):
                if matched is None or len(sym) > len(matched):
                    matched = sym
        if matched is not None:
            tokens.append(("op", matched))
            i += len(matched)
            continue

        if ch in "?():,":
            tokens.append((ch, ch))
            i += 1
            continue

        raise LexError(f"位置 {start}：无法识别的字符 {ch!r}")

    tokens.append(("eof", None))
    return tokens
