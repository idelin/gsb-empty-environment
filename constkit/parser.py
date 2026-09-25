"""Hand-written recursive-descent parser and AST definitions.

Grammar (semicolon terminated):
    module  := "module" IDENT ";" item*
    item    := ("const"|"local") IDENT "=" expr ";"
             | "enum" IDENT "{" member ("," member)* ","? "}"
             | "when" cond "{" item* "}"
    expr    := unary (("+"|"-"|"%") unary)*  etc.
    primary := INT | IDENT ("." IDENT)* | "(" expr ")" | "[" expr ("," expr)* "]"
"""
from __future__ import annotations

from dataclasses import dataclass, field

from .diag import Diagnostic, Span
from .lexer import Token, lex

# ---------------- AST ----------------


@dataclass(frozen=True)
class IntLit:
    value: int
    span: Span


@dataclass(frozen=True)
class Ref:
    parts: tuple[str, ...]
    span: Span


@dataclass(frozen=True)
class Bin:
    op: str
    left: object
    right: object
    span: Span


@dataclass(frozen=True)
class Neg:
    operand: object
    span: Span


@dataclass(frozen=True)
class Index:
    target: object
    index: object
    span: Span


@dataclass(frozen=True)
class ArrayLit:
    items: list[object]
    span: Span


Expr = object


@dataclass
class ConstItem:
    name: str
    name_span: Span
    exported: bool
    expr: Expr
    span: Span


@dataclass(frozen=True)
class EnumMember:
    name: str
    name_span: Span
    expr: Expr | None


@dataclass
class EnumItem:
    name: str
    name_span: Span
    members: list[EnumMember]
    span: Span


@dataclass(frozen=True)
class CondVar:
    name: str
    span: Span

    def eval(self, flags: dict[str, bool]) -> bool:
        return bool(flags.get(self.name, False))

    def flags_used(self) -> frozenset[str]:
        return frozenset({self.name})


@dataclass(frozen=True)
class CondNot:
    inner: object

    def eval(self, flags) -> bool:
        return not self.inner.eval(flags)

    def flags_used(self):
        return self.inner.flags_used()


@dataclass(frozen=True)
class CondAnd:
    left: object
    right: object

    def eval(self, flags) -> bool:
        return self.left.eval(flags) and self.right.eval(flags)

    def flags_used(self):
        return self.left.flags_used() | self.right.flags_used()


@dataclass(frozen=True)
class CondOr:
    left: object
    right: object

    def eval(self, flags) -> bool:
        return self.left.eval(flags) or self.right.eval(flags)

    def flags_used(self):
        return self.left.flags_used() | self.right.flags_used()


@dataclass
class WhenItem:
    cond: object
    items: list[object]
    span: Span


@dataclass
class Module:
    name: str
    items: list[object]


# ---------------- Parser ----------------

_RESERVED = {"module", "const", "local", "enum", "when"}


class ParseError(Exception):
    pass


class _Parser:
    def __init__(self, tokens: list[Token], module: str, diags: list[Diagnostic]):
        self.toks = tokens
        self.i = 0
        self.module = module
        self.diags = diags

    def _peek(self, k: int = 0) -> Token:
        return self.toks[min(self.i + k, len(self.toks) - 1)]

    def _next(self) -> Token:
        t = self.toks[self.i]
        self.i = min(self.i + 1, len(self.toks) - 1)
        return t

    def _at(self, text: str) -> bool:
        t = self._peek()
        return t.kind in ("punct", "ident") and t.text == text

    def _error(self, msg: str, span: Span) -> None:
        self.diags.append(Diagnostic(self.module, span, "parse-error", msg))

    def _expect(self, text: str) -> Token:
        if self._at(text):
            return self._next()
        t = self._peek()
        self._error(f"expected {text!r}, got {t.text!r}", t.span)
        raise ParseError()

    def _expect_ident(self) -> Token:
        t = self._peek()
        if t.kind != "ident" or t.text in _RESERVED:
            self._error(f"expected identifier, got {t.text!r}", t.span)
            raise ParseError()
        return self._next()

    def _recover(self) -> None:
        """Skip tokens up to the next ';' at brace depth 0 (or end of block)."""
        depth = 0
        while self._peek().kind != "eof":
            t = self._peek()
            if t.text == ";" and depth == 0:
                self._next()
                return
            if t.text == "{":
                depth += 1
            elif t.text == "}":
                if depth == 0:
                    return
                depth -= 1
            self._next()

    # items

    def parse_module(self) -> Module:
        try:
            kw = self._peek()
            if not (kw.kind == "ident" and kw.text == "module"):
                self._error("module source must start with 'module <name>;'", kw.span)
                raise ParseError()
            self._next()
            name_tok = self._expect_ident()
            self._expect(";")
            if name_tok.text != self.module:
                self._error(
                    f"module name {name_tok.text!r} does not match source key {self.module!r}",
                    name_tok.span,
                )
        except ParseError:
            self._recover()
        items: list[object] = []
        while self._peek().kind != "eof":
            try:
                items.append(self.parse_item())
            except ParseError:
                self._recover()
        return Module(self.module, items)

    def parse_item(self) -> object:
        t = self._peek()
        if t.kind == "ident" and t.text in ("const", "local"):
            return self.parse_const()
        if t.kind == "ident" and t.text == "enum":
            return self.parse_enum()
        if t.kind == "ident" and t.text == "when":
            return self.parse_when()
        self._error(f"unexpected token {t.text!r}", t.span)
        raise ParseError()

    def parse_const(self) -> ConstItem:
        kw = self._next()
        exported = kw.text == "const"
        name = self._expect_ident()
        self._expect("=")
        expr = self.parse_expr()
        end = self._expect(";")
        return ConstItem(name.text, name.span, exported, expr, Span(kw.span.start, end.span.end))

    def parse_enum(self) -> EnumItem:
        kw = self._next()
        name = self._expect_ident()
        self._expect("{")
        members: list[EnumMember] = []
        while not self._at("}"):
            mtok = self._expect_ident()
            expr = None
            if self._at("="):
                self._next()
                expr = self.parse_expr()
            members.append(EnumMember(mtok.text, mtok.span, expr))
            if self._at(","):
                self._next()
            elif not self._at("}"):
                t = self._peek()
                self._error(f"expected ',' or '}}', got {t.text!r}", t.span)
                raise ParseError()
        end = self._expect("}")
        return EnumItem(name.text, name.span, members, Span(kw.span.start, end.span.end))

    def parse_when(self) -> WhenItem:
        kw = self._next()
        cond = self.parse_cond()
        self._expect("{")
        items: list[object] = []
        while not self._at("}") and self._peek().kind != "eof":
            try:
                items.append(self.parse_item())
            except ParseError:
                self._recover()
        end = self._expect("}")
        return WhenItem(cond, items, Span(kw.span.start, end.span.end))

    # condition expressions

    def parse_cond(self) -> object:
        return self.parse_cond_or()

    def parse_cond_or(self) -> object:
        node = self.parse_cond_and()
        while self._at("||"):
            self._next()
            right = self.parse_cond_and()
            node = CondOr(node, right)
        return node

    def parse_cond_and(self) -> object:
        node = self.parse_cond_unary()
        while self._at("&&"):
            self._next()
            right = self.parse_cond_unary()
            node = CondAnd(node, right)
        return node

    def parse_cond_unary(self) -> object:
        if self._at("!"):
            self._next()
            return CondNot(self.parse_cond_unary())
        if self._at("("):
            self._next()
            node = self.parse_cond()
            self._expect(")")
            return node
        t = self._expect_ident()
        return CondVar(t.text, t.span)

    # value expressions

    def parse_expr(self) -> Expr:
        return self.parse_add()

    def parse_add(self) -> Expr:
        node = self.parse_mul()
        while self._at("+") or self._at("-"):
            op = self._next()
            right = self.parse_mul()
            node = Bin(op.text, node, right, Span(node.span.start, right.span.end))
        return node

    def parse_mul(self) -> Expr:
        node = self.parse_unary()
        while self._at("*") or self._at("/") or self._at("%"):
            op = self._next()
            right = self.parse_unary()
            node = Bin(op.text, node, right, Span(node.span.start, right.span.end))
        return node

    def parse_unary(self) -> Expr:
        if self._at("-"):
            op = self._next()
            inner = self.parse_unary()
            return Neg(inner, Span(op.span.start, inner.span.end))
        return self.parse_postfix()

    def parse_postfix(self) -> Expr:
        node = self.parse_primary()
        while self._at("["):
            self._next()
            index = self.parse_expr()
            end = self._expect("]")
            node = Index(node, index, Span(node.span.start, end.span.end))
        return node

    def parse_primary(self) -> Expr:
        t = self._peek()
        if t.kind == "int":
            self._next()
            return IntLit(int(t.text), t.span)
        if t.kind == "ident":
            start = t.span.start
            first = self._next()
            last = first
            parts = [first.text]
            while self._at("."):
                self._next()
                last = self._expect_ident()
                parts.append(last.text)
            return Ref(tuple(parts), Span(start, last.span.end))
        if self._at("("):
            self._next()
            node = self.parse_expr()
            self._expect(")")
            return node
        if self._at("["):
            st = self._next()
            items: list[Expr] = []
            if not self._at("]"):
                items.append(self.parse_expr())
                while self._at(","):
                    self._next()
                    if self._at("]"):
                        break
                    items.append(self.parse_expr())
            end = self._expect("]")
            return ArrayLit(items, Span(st.span.start, end.span.end))
        self._error(f"unexpected token {t.text!r}", t.span)
        raise ParseError()


def parse_module(source: str, module: str) -> tuple[Module, list[Diagnostic]]:
    diags: list[Diagnostic] = []
    tokens = lex(source, module, diags)
    parser = _Parser(tokens, module, diags)
    return parser.parse_module(), diags


def sexpr(node: Expr) -> str:
    """Canonical structural form of an expression (used for fingerprints)."""
    if isinstance(node, IntLit):
        return f"i{node.value}"
    if isinstance(node, Ref):
        return "r" + ".".join(node.parts)
    if isinstance(node, Bin):
        return f"({node.op} {sexpr(node.left)} {sexpr(node.right)})"
    if isinstance(node, Neg):
        return f"(- {sexpr(node.operand)})"
    if isinstance(node, Index):
        return f"(@ {sexpr(node.target)} {sexpr(node.index)})"
    if isinstance(node, ArrayLit):
        return "[" + ",".join(sexpr(x) for x in node.items) + "]"
    raise TypeError(f"not an expression: {node!r}")
