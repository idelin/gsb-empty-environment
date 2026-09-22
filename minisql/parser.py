"""AST and recursive-descent parser for the supported mini-SQL subset."""
from __future__ import annotations

from dataclasses import dataclass, field

from .errors import SQLSyntaxError, UnsupportedSQLError
from .lexer import Token, tokenize

_AGGS = frozenset({"count", "sum", "avg", "min", "max"})
_TRAILING_FORBIDDEN = frozenset({
    "order", "limit", "offset", "having", "union", "intersect", "except",
})
_JOIN_WORDS = frozenset({
    "join", "inner", "left", "right", "full", "outer", "cross", "on", "using",
})


# --------------------------------------------------------------------------- #
# AST nodes
# --------------------------------------------------------------------------- #
@dataclass(frozen=True)
class Literal:
    value: object  # int | float | str | bool | None


@dataclass(frozen=True)
class Column:
    table: str | None
    name: str


@dataclass(frozen=True)
class Unary:
    op: str
    operand: object


@dataclass(frozen=True)
class Binary:
    op: str
    left: object
    right: object


@dataclass(frozen=True)
class IsNull:
    operand: object
    negated: bool


@dataclass(frozen=True)
class Aggregate:
    func: str
    arg: object | None   # None means '*'
    star: bool = False


@dataclass(frozen=True)
class Exists:
    query: object
    negated: bool = False


@dataclass(frozen=True)
class ScalarSubquery:
    query: object


@dataclass(frozen=True)
class Star:
    pass


@dataclass(frozen=True)
class SelectItem:
    expr: object
    alias: str | None


@dataclass(frozen=True)
class TableRef:
    name: str
    alias: str | None


@dataclass(frozen=True)
class Select:
    items: list[object]
    froms: list[TableRef]
    where: object | None
    group_by: list[Column] = field(default_factory=list)


# --------------------------------------------------------------------------- #
# Parser
# --------------------------------------------------------------------------- #
class _Parser:
    def __init__(self, sql: str):
        self.tokens: list[Token] = tokenize(sql)
        self.i = 0

    # -- token helpers ----------------------------------------------------- #
    @property
    def cur(self) -> Token:
        return self.tokens[self.i]

    def advance(self) -> Token:
        tok = self.tokens[self.i]
        self.i += 1
        return tok

    def accept_punct(self, value: str) -> bool:
        if self.cur.kind == "PUNCT" and self.cur.value == value:
            self.advance()
            return True
        return False

    def accept_kw(self, word: str) -> bool:
        if self.cur.kind == "KEYWORD" and self.cur.value == word:
            self.advance()
            return True
        return False

    def expect_punct(self, value: str) -> Token:
        if self.cur.kind != "PUNCT" or self.cur.value != value:
            raise SQLSyntaxError(
                "expected %r but found %r" % (value, self.cur.value)
            )
        return self.advance()

    def expect_kw(self, word: str) -> Token:
        if self.cur.kind != "KEYWORD" or self.cur.value != word:
            raise SQLSyntaxError(
                "expected %s but found %r" % (word.upper(), self.cur.value)
            )
        return self.advance()

    def expect_ident(self) -> str:
        if self.cur.kind != "IDENT":
            raise SQLSyntaxError("expected identifier but found %r" % self.cur.value)
        return self.advance().value

    # -- entry point ------------------------------------------------------- #
    def parse_statement(self) -> Select:
        query = self.parse_select()
        if self.cur.kind != "EOF":
            if self.cur.value in _TRAILING_FORBIDDEN:
                raise UnsupportedSQLError(
                    "clause %s is not supported" % self.cur.value.upper()
                )
            raise SQLSyntaxError("unexpected trailing token %r" % self.cur.value)
        return query

    def parse_select(self) -> Select:
        self.expect_kw("select")
        items = self.parse_select_items()
        self.expect_kw("from")
        froms = self.parse_froms()
        where = None
        if self.accept_kw("where"):
            where = self.parse_expression()
        group_by: list[Column] = []
        if self.accept_kw("group"):
            self.expect_kw("by")
            group_by = self.parse_group_columns()
        if self.cur.value in _TRAILING_FORBIDDEN:
            raise UnsupportedSQLError(
                "clause %s is not supported" % self.cur.value.upper()
            )
        return Select(items, froms, where, group_by)

    def parse_select_items(self) -> list[object]:
        items: list[object] = []
        if self.accept_punct("*"):
            if self.accept_punct(","):
                raise SQLSyntaxError("'*' cannot be mixed with other select items")
            return [Star()]
        while True:
            # qualified star t.* is deliberately rejected
            if (self.cur.kind == "IDENT"
                    and self.tokens[self.i + 1].kind == "PUNCT"
                    and self.tokens[self.i + 1].value == "."
                    and self.tokens[self.i + 2].kind == "PUNCT"
                    and self.tokens[self.i + 2].value == "*"):
                raise UnsupportedSQLError("qualified '*' (t.*) is not supported")
            expr = self.parse_expression()
            alias = None
            if self.accept_kw("as"):
                alias = self.expect_ident()
            elif (self.cur.kind == "IDENT"
                  and self.tokens[self.i + 1].kind in ("PUNCT", "KEYWORD")):
                # bare alias: a b, a FROM ...
                nxt = self.tokens[self.i + 1]
                if (nxt.kind == "PUNCT" and nxt.value == ",") or \
                   nxt.kind == "EOF" or \
                   (nxt.kind == "KEYWORD" and nxt.value in
                        ("from", "where", "group")):
                    alias = self.advance().value
            items.append(SelectItem(expr, alias))
            if not self.accept_punct(","):
                break
        return items

    def parse_froms(self) -> list[TableRef]:
        refs: list[TableRef] = []
        while True:
            name = self.expect_ident()
            alias = None
            if self.accept_kw("as"):
                alias = self.expect_ident()
            elif self.cur.kind == "IDENT":
                alias = self.advance().value
            elif self.cur.kind == "KEYWORD" and self.cur.value in _JOIN_WORDS:
                raise UnsupportedSQLError("JOIN is not supported; list base "
                                          "tables with commas instead")
            refs.append(TableRef(name, alias))
            if self.cur.kind == "KEYWORD" and self.cur.value in _JOIN_WORDS:
                raise UnsupportedSQLError("JOIN is not supported; list base "
                                          "tables with commas instead")
            if not self.accept_punct(","):
                break
        return refs

    def parse_group_columns(self) -> list[Column]:
        cols: list[Column] = []
        while True:
            cols.append(self.parse_column())
            if not self.accept_punct(","):
                break
        return cols

    # -- expressions ------------------------------------------------------- #
    def parse_expression(self) -> object:
        return self.parse_or()

    def parse_or(self) -> object:
        node = self.parse_and()
        while self.accept_kw("or"):
            node = Binary("OR", node, self.parse_and())
        return node

    def parse_and(self) -> object:
        node = self.parse_not()
        while self.accept_kw("and"):
            node = Binary("AND", node, self.parse_not())
        return node

    def parse_not(self) -> object:
        if self.accept_kw("not"):
            return Unary("NOT", self.parse_not())
        return self.parse_comparison()

    def parse_comparison(self) -> object:
        node = self.parse_addition()
        ops = {"=", "<>", "!=", "<", "<=", ">", ">="}
        if self.cur.kind == "PUNCT" and self.cur.value in ops:
            op = self.advance().value
            right = self.parse_addition()
            node = Binary(op, node, right)
        return node

    def parse_addition(self) -> object:
        node = self.parse_multiplication()
        while self.cur.kind == "PUNCT" and self.cur.value in ("+", "-"):
            op = self.advance().value
            node = Binary(op, node, self.parse_multiplication())
        return node

    def parse_multiplication(self) -> object:
        node = self.parse_unary()
        while self.cur.kind == "PUNCT" and self.cur.value in ("*", "/"):
            op = self.advance().value
            node = Binary(op, node, self.parse_unary())
        return node

    def parse_unary(self) -> object:
        if self.cur.kind == "PUNCT" and self.cur.value in ("+", "-"):
            op = self.advance().value
            return Unary(op, self.parse_unary())
        return self.parse_is_predicate()

    def parse_is_predicate(self) -> object:
        node = self.parse_primary()
        if self.accept_kw("is"):
            negated = self.accept_kw("not")
            self.expect_kw("null")
            node = IsNull(node, negated)
        return node

    def parse_column(self) -> Column:
        name = self.expect_ident()
        table = None
        if self.accept_punct("."):
            table = name
            name = self.expect_ident()
        return Column(table, name)

    def parse_primary(self) -> object:
        tok = self.cur
        if tok.kind == "NUMBER":
            self.advance()
            text = tok.value
            return Literal(int(text) if "." not in text else float(text))
        if tok.kind == "STRING":
            self.advance()
            return Literal(tok.value)
        if tok.kind == "KEYWORD":
            if tok.value == "null":
                self.advance()
                return Literal(None)
            if tok.value == "true":
                self.advance()
                return Literal(True)
            if tok.value == "false":
                self.advance()
                return Literal(False)
            if tok.value == "exists":
                self.advance()
                self.expect_punct("(")
                query = self.parse_select()
                self.expect_punct(")")
                node = Exists(query)
                return node
            if tok.value in _AGGS:
                return self.parse_aggregate()
        if tok.kind == "IDENT":
            # unknown function call -> not part of the subset
            if self.tokens[self.i + 1].kind == "PUNCT" \
                    and self.tokens[self.i + 1].value == "(":
                fname = self.advance().value
                raise UnsupportedSQLError("function %s() is not supported"
                                          % fname.upper())
            col = self.parse_column()
            return col
        if tok.kind == "PUNCT" and tok.value == "(":
            self.advance()
            if self.cur.kind == "KEYWORD" and self.cur.value == "select":
                query = self.parse_select()
                self.expect_punct(")")
                return ScalarSubquery(query)
            node = self.parse_expression()
            self.expect_punct(")")
            return node
        raise SQLSyntaxError("unexpected token %r" % tok.value)

    def parse_aggregate(self) -> Aggregate:
        func = self.advance().value  # count/sum/...
        self.expect_punct("(")
        if self.accept_punct("*"):
            self.expect_punct(")")
            if func != "count":
                raise UnsupportedSQLError(
                    "%s(*) is not supported; only COUNT(*)" % func.upper()
                )
            return Aggregate(func, None, star=True)
        if self.accept_kw("distinct"):
            raise UnsupportedSQLError("DISTINCT in aggregates is not supported")
        self.accept_kw("all")
        arg = self.parse_expression()
        self.expect_punct(")")
        return Aggregate(func, arg)


def parse(sql: str) -> Select:
    return _Parser(sql).parse_statement()
