"""A tiny self-contained language: lexer, parser, macro expander, bytecode
compiler and a stack VM.  No eval, no compile(), no ast module.

All positions are UTF-8 byte intervals ``[start, end)``.

Grammar::

    program  := stmt*
    stmt     := "let" NAME "=" expr ";"
              | "print" expr ";"
              | "macro" NAME "(" params ")" "{" expr "}"
              | expr ";"
    expr     := add
    add      := mul (("+" | "-") mul)*
    mul      := primary (("*" | "/") primary)*
    primary  := NUM | NAME | NAME "(" args ")" | "(" expr ")"
"""
from __future__ import annotations

from dataclasses import dataclass

from .intervals import Interval


# ------------------------------------------------------------------ lexer

@dataclass(frozen=True)
class Token:
    kind: str        # 'num' | 'name' | 'punct'
    text: str
    span: Interval   # UTF-8 byte interval in the lexed buffer


class LexError(Exception):
    pass


def _byte_positions(s: str) -> list[int]:
    """char index -> UTF-8 byte offset."""

    pos = [0] * (len(s) + 1)
    off = 0
    for i, ch in enumerate(s):
        pos[i] = off
        off += len(ch.encode("utf-8"))
    pos[len(s)] = off
    return pos


_PUNCT = set("(){};=+-*/,")


def lex(text: str) -> list[Token]:
    bpos = _byte_positions(text)
    toks: list[Token] = []
    i, n = 0, len(text)
    while i < n:
        ch = text[i]
        if ch.isspace():
            i += 1
            continue
        if ch == "#":  # line comment (may contain arbitrary Unicode)
            while i < n and text[i] != "\n":
                i += 1
            continue
        start = i
        if ch.isdigit():
            while i < n and text[i].isdigit():
                i += 1
            kind = "num"
        elif ch.isalpha() or ch == "_":
            while i < n and (text[i].isalnum() or text[i] == "_"):
                i += 1
            kind = "name"
        elif ch in _PUNCT:
            i += 1
            kind = "punct"
        else:
            raise LexError(f"unexpected character {ch!r} at byte {bpos[start]}")
        toks.append(Token(kind, text[start:i], Interval(bpos[start], bpos[i])))
    return toks


# ------------------------------------------------------------------ AST

@dataclass
class Num:
    text: str
    value: int
    span: Interval


@dataclass
class Var:
    name: str
    span: Interval


@dataclass
class Bin:
    op: str
    left: object
    right: object
    op_span: Interval
    span: Interval


@dataclass
class Call:
    name: str
    args: list
    span: Interval


@dataclass
class Let:
    span: Interval
    name: str
    name_span: Interval
    expr: object


@dataclass
class Print:
    span: Interval
    expr: object


@dataclass
class ExprStmt:
    span: Interval
    expr: object


@dataclass
class MacroDef:
    span: Interval        # whole `macro ... { ... }` statement
    name: str
    params: list
    body: object          # expression AST, spans in the user source


class ParseError(Exception):
    pass


class _Parser:
    def __init__(self, toks):
        self.toks = toks
        self.i = 0

    def at_end(self):
        return self.i >= len(self.toks)

    def peek(self, text=None):
        if self.i < len(self.toks):
            tok = self.toks[self.i]
            if text is None or tok.text == text:
                return tok
        return None

    def next(self, text=None):
        tok = self.peek(text)
        if tok is None:
            got = self.toks[self.i].text if self.i < len(self.toks) else "<end>"
            raise ParseError(f"expected {text!r}, got {got!r}")
        self.i += 1
        return tok

    def next_name(self):
        tok = self.next()
        if tok.kind != "name":
            raise ParseError(f"expected a name, got {tok.text!r}")
        return tok


def parse_program(toks: list[Token]) -> list:
    p = _Parser(toks)
    stmts = []
    while not p.at_end():
        stmts.append(_parse_stmt(p))
    return stmts


def _parse_stmt(p: _Parser):
    tok = p.peek()
    if tok is None:
        raise ParseError("unexpected end of input")
    if tok.text == "let":
        kw = p.next()
        name = p.next_name()
        p.next("=")
        expr = _parse_expr(p)
        semi = p.next(";")
        return Let(Interval(kw.span.start, semi.span.end), name.text, name.span, expr)
    if tok.text == "print":
        kw = p.next()
        expr = _parse_expr(p)
        semi = p.next(";")
        return Print(Interval(kw.span.start, semi.span.end), expr)
    if tok.text == "macro":
        kw = p.next()
        name = p.next_name()
        p.next("(")
        params = []
        if not p.peek(")"):
            params.append(p.next_name().text)
            while p.peek(","):
                p.next(",")
                params.append(p.next_name().text)
        p.next(")")
        p.next("{")
        body = _parse_expr(p)
        close = p.next("}")
        return MacroDef(Interval(kw.span.start, close.span.end), name.text, params, body)
    expr = _parse_expr(p)
    semi = p.next(";")
    return ExprStmt(Interval(expr.span.start, semi.span.end), expr)


def _parse_expr(p: _Parser):
    return _parse_add(p)


def _parse_add(p: _Parser):
    left = _parse_mul(p)
    while p.peek("+") or p.peek("-"):
        op = p.next()
        right = _parse_mul(p)
        left = Bin(op.text, left, right, op.span, Interval(left.span.start, right.span.end))
    return left


def _parse_mul(p: _Parser):
    left = _parse_primary(p)
    while p.peek("*") or p.peek("/"):
        op = p.next()
        right = _parse_primary(p)
        left = Bin(op.text, left, right, op.span, Interval(left.span.start, right.span.end))
    return left


def _parse_primary(p: _Parser):
    tok = p.peek()
    if tok is None:
        raise ParseError("unexpected end of input in expression")
    if tok.kind == "num":
        p.next()
        return Num(tok.text, int(tok.text), tok.span)
    if tok.kind == "name":
        p.next()
        if p.peek("("):
            p.next("(")
            args = []
            if not p.peek(")"):
                args.append(_parse_expr(p))
                while p.peek(","):
                    p.next(",")
                    args.append(_parse_expr(p))
            close = p.next(")")
            return Call(tok.text, args, Interval(tok.span.start, close.span.end))
        return Var(tok.text, tok.span)
    if p.peek("("):
        p.next("(")
        expr = _parse_expr(p)
        p.next(")")
        return expr
    raise ParseError(f"unexpected {tok.text!r} at byte {tok.span.start}")


# ------------------------------------------------------------------ macro expansion

@dataclass(frozen=True)
class Prov:
    """Provenance of one expanded token.

    * ``source``: copied from user code; ``src_span`` is the user span.
    * ``macro_body``: copied from a macro definition; ``src_span`` is the
      span inside the definition, ``call_span`` the outermost user-level
      call site, ``def_span`` the whole definition statement.
    * ``generated``: synthesized by the expander; no source at all.
    """

    kind: str
    src_span: Interval | None
    call_span: Interval | None = None
    def_span: Interval | None = None
    macro_name: str | None = None


_GENERATED = Prov("generated", None)


# Expanded AST (XAST): tokens carry Prov instead of plain spans.
@dataclass
class XNum:
    text: str
    prov: Prov


@dataclass
class XVar:
    name: str
    prov: Prov


@dataclass
class XBin:
    op: str
    left: object
    right: object
    op_prov: Prov


@dataclass
class XCall:   # macro call inside a macro body, not yet expanded
    name: str
    args: list
    span: Interval


@dataclass
class XSub:    # substituted argument; emitted wrapped in generated parens
    inner: object


class ExpandError(Exception):
    pass


MAX_DEPTH = 100


def expand_expr(node, macros: dict, used: set, depth: int = 0):
    """Expand a user-source expression AST into an XAST."""

    if depth > MAX_DEPTH:
        raise ExpandError("macro expansion too deep (recursive macro?)")
    if isinstance(node, Num):
        return XNum(node.text, Prov("source", node.span))
    if isinstance(node, Var):
        return XVar(node.name, Prov("source", node.span))
    if isinstance(node, Bin):
        return XBin(
            node.op,
            expand_expr(node.left, macros, used, depth),
            expand_expr(node.right, macros, used, depth),
            Prov("source", node.op_span),
        )
    if isinstance(node, Call):
        args = [expand_expr(a, macros, used, depth) for a in node.args]
        return _expand_call(node.name, args, node.span, macros, used, depth)
    raise ExpandError(f"cannot expand {node!r}")


def _expand_call(name, arg_xnodes, user_call_span, macros, used, depth):
    if name not in macros:
        raise ExpandError(f"unknown macro '{name}'")
    d = macros[name]
    if len(arg_xnodes) != len(d.params):
        raise ExpandError(
            f"macro '{name}' takes {len(d.params)} argument(s), got {len(arg_xnodes)}"
        )
    used.add(name)
    mapping = dict(zip(d.params, arg_xnodes))
    sub = _substitute(d.body, mapping, d, user_call_span)
    return _expand_x(sub, macros, used, depth + 1, user_call_span)


def _substitute(node, mapping, d, user_call_span):
    """Copy a macro body AST into XAST, replacing parameters."""

    def mb(span):
        return Prov("macro_body", span, call_span=user_call_span,
                    def_span=d.span, macro_name=d.name)

    if isinstance(node, Num):
        return XNum(node.text, mb(node.span))
    if isinstance(node, Var):
        if node.name in mapping:
            return XSub(mapping[node.name])
        return XVar(node.name, mb(node.span))
    if isinstance(node, Bin):
        return XBin(
            node.op,
            _substitute(node.left, mapping, d, user_call_span),
            _substitute(node.right, mapping, d, user_call_span),
            mb(node.op_span),
        )
    if isinstance(node, Call):
        return XCall(
            node.name,
            [_substitute(a, mapping, d, user_call_span) for a in node.args],
            node.span,
        )
    raise ExpandError(f"cannot substitute in {node!r}")


def _expand_x(x, macros, used, depth, user_call_span):
    """Expand nested macro calls inside an already-substituted XAST.

    ``user_call_span`` stays the outermost user-level call site, so a
    diagnostic for a deeply nested expansion still points at the fragment
    the user actually wrote.
    """

    if depth > MAX_DEPTH:
        raise ExpandError("macro expansion too deep (recursive macro?)")
    if isinstance(x, (XNum, XVar)):
        return x
    if isinstance(x, XSub):
        return XSub(_expand_x(x.inner, macros, used, depth, user_call_span))
    if isinstance(x, XBin):
        return XBin(
            x.op,
            _expand_x(x.left, macros, used, depth, user_call_span),
            _expand_x(x.right, macros, used, depth, user_call_span),
            x.op_prov,
        )
    if isinstance(x, XCall):
        args = [_expand_x(a, macros, used, depth, user_call_span) for a in x.args]
        return _expand_call(x.name, args, user_call_span, macros, used, depth)
    raise ExpandError(f"cannot expand {x!r}")


@dataclass(frozen=True)
class EToken:
    text: str
    kind: str
    prov: Prov


def emit(x) -> list[EToken]:
    """Flatten an XAST into expanded tokens (infix order)."""

    if isinstance(x, XNum):
        return [EToken(x.text, "num", x.prov)]
    if isinstance(x, XVar):
        return [EToken(x.name, "name", x.prov)]
    if isinstance(x, XBin):
        return emit(x.left) + [EToken(x.op, "punct", x.op_prov)] + emit(x.right)
    if isinstance(x, XSub):
        return (
            [EToken("(", "punct", _GENERATED)]
            + emit(x.inner)
            + [EToken(")", "punct", _GENERATED)]
        )
    raise ExpandError(f"internal: unexpanded node {x!r}")


def expand_stmt(stmt, macros: dict, used: set) -> list[EToken]:
    """Expand one non-macrodef statement into expanded tokens."""

    toks: list[EToken] = []
    if isinstance(stmt, Let):
        toks.append(EToken("let", "name", Prov("source", stmt.span)))
        toks.append(EToken(stmt.name, "name", Prov("source", stmt.name_span)))
        toks.append(EToken("=", "punct", Prov("source", stmt.span)))
        toks.extend(emit(expand_expr(stmt.expr, macros, used)))
        toks.append(EToken(";", "punct", Prov("source", stmt.span)))
    elif isinstance(stmt, Print):
        toks.append(EToken("print", "name", Prov("source", stmt.span)))
        toks.extend(emit(expand_expr(stmt.expr, macros, used)))
        toks.append(EToken(";", "punct", Prov("source", stmt.span)))
    elif isinstance(stmt, ExprStmt):
        toks.extend(emit(expand_expr(stmt.expr, macros, used)))
        toks.append(EToken(";", "punct", Prov("source", stmt.span)))
    else:
        raise ExpandError(f"cannot expand statement {stmt!r}")
    return toks


# ------------------------------------------------------------------ compiler

@dataclass(frozen=True)
class Instr:
    op: str
    arg: object = None
    span: Interval | None = None  # byte interval in the expanded buffer; None => generated


_BINOPS = {"+": "ADD", "-": "SUB", "*": "MUL", "/": "DIV"}


class CompileError(Exception):
    pass


def _compile_expr(node, out: list):
    if isinstance(node, Num):
        out.append(Instr("PUSH", node.value, node.span))
    elif isinstance(node, Var):
        out.append(Instr("LOAD", node.name, node.span))
    elif isinstance(node, Bin):
        _compile_expr(node.left, out)
        _compile_expr(node.right, out)
        out.append(Instr(_BINOPS[node.op], None, node.span))
    else:
        raise CompileError(f"cannot compile {node!r}")


def compile_stmt(stmt, out: list):
    if isinstance(stmt, Let):
        _compile_expr(stmt.expr, out)
        out.append(Instr("STORE", stmt.name, stmt.span))
    elif isinstance(stmt, Print):
        _compile_expr(stmt.expr, out)
        out.append(Instr("PRINT", None, stmt.span))
    elif isinstance(stmt, ExprStmt):
        _compile_expr(stmt.expr, out)
        out.append(Instr("POP"))  # generated by the compiler: no source span
    else:
        raise CompileError(f"cannot compile statement {stmt!r}")


# ------------------------------------------------------------------ VM

class VMError(Exception):
    def __init__(self, pc: int, message: str):
        super().__init__(f"pc={pc}: {message}")
        self.pc = pc
        self.message = message


def run_program(instrs) -> list:
    stack: list[int] = []
    env: dict[str, int] = {}
    output: list[int] = []
    pc = 0
    while True:
        ins = instrs[pc]
        op = ins.op
        if op == "HALT":
            return output
        if op == "PUSH":
            stack.append(ins.arg)
        elif op == "LOAD":
            if ins.arg not in env:
                raise VMError(pc, f"undefined variable '{ins.arg}'")
            stack.append(env[ins.arg])
        elif op == "STORE":
            env[ins.arg] = stack.pop()
        elif op in ("ADD", "SUB", "MUL", "DIV"):
            b = stack.pop()
            a = stack.pop()
            if op == "ADD":
                stack.append(a + b)
            elif op == "SUB":
                stack.append(a - b)
            elif op == "MUL":
                stack.append(a * b)
            elif b == 0:
                raise VMError(pc, "division by zero")
            else:
                stack.append(a // b)
        elif op == "PRINT":
            output.append(stack.pop())
        elif op == "POP":
            stack.pop()
        else:
            raise VMError(pc, f"bad opcode {op!r}")
        pc += 1
