from dataclasses import dataclass, field

from .errors import ParseError, UnsupportedSQL


KEYWORDS = {
    "and", "as", "asc", "avg", "between", "by", "case", "count", "desc",
    "distinct", "else", "end", "exists", "false", "from", "group", "having",
    "in", "is", "join", "like", "limit", "max", "min", "not", "null", "on",
    "or", "order", "select", "sum", "then", "true", "union", "when", "where",
}


@dataclass(frozen=True)
class Token:
    kind: str
    value: object
    pos: int


def tokenize(sql):
    tokens = []
    i = 0
    while i < len(sql):
        ch = sql[i]
        if ch.isspace():
            i += 1
            continue
        if ch == "-" and i + 1 < len(sql) and sql[i + 1] == "-":
            i += 2
            while i < len(sql) and sql[i] != "\n":
                i += 1
            continue
        if ch == "/" and i + 1 < len(sql) and sql[i + 1] == "*":
            raise UnsupportedSQL("block comments are not supported")
        if ch.isalpha() or ch == "_":
            start = i
            i += 1
            while i < len(sql) and (sql[i].isalnum() or sql[i] == "_"):
                i += 1
            text = sql[start:i]
            word = text.lower()
            kind = word if word in KEYWORDS else "ident"
            tokens.append(Token(kind, word if kind in KEYWORDS else text, start))
            continue
        if ch.isdigit():
            start = i
            seen_dot = False
            while i < len(sql) and (sql[i].isdigit() or sql[i] == "."):
                if sql[i] == ".":
                    if seen_dot:
                        raise ParseError(f"malformed number at position {start}")
                    seen_dot = True
                i += 1
            text = sql[start:i]
            value = float(text) if seen_dot else int(text)
            tokens.append(Token("number", value, start))
            continue
        if ch == "'":
            start = i
            i += 1
            chars = []
            while i < len(sql):
                if sql[i] == "'":
                    if i + 1 < len(sql) and sql[i + 1] == "'":
                        chars.append("'")
                        i += 2
                        continue
                    i += 1
                    tokens.append(Token("string", "".join(chars), start))
                    break
                chars.append(sql[i])
                i += 1
            else:
                raise ParseError("unterminated string literal")
            continue
        if ch == '"':
            start = i
            i += 1
            chars = []
            while i < len(sql):
                if sql[i] == '"':
                    if i + 1 < len(sql) and sql[i + 1] == '"':
                        chars.append('"')
                        i += 2
                        continue
                    i += 1
                    tokens.append(Token("quoted_ident", "".join(chars), start))
                    break
                chars.append(sql[i])
                i += 1
            else:
                raise ParseError("unterminated quoted identifier")
            continue
        two = sql[i:i + 2]
        if two in {"<=", ">=", "<>", "!="}:
            tokens.append(Token("op", "!=" if two == "<>" else two, i))
            i += 2
            continue
        if ch in "=<>+-*/().,;":
            tokens.append(Token("op", ch, i))
            i += 1
            continue
        raise ParseError(f"unexpected character {ch!r} at position {i}")
    tokens.append(Token("eof", None, len(sql)))
    return tokens


@dataclass(frozen=True)
class Star:
    qualifier: str | None = None


@dataclass(frozen=True)
class Literal:
    value: object


@dataclass(frozen=True)
class ColumnRef:
    name: str
    qualifier: str | None = None


@dataclass(frozen=True)
class UnaryExpr:
    op: str
    expr: object


@dataclass(frozen=True)
class BinaryExpr:
    left: object
    op: str
    right: object


@dataclass(frozen=True)
class FunctionCall:
    name: str
    args: tuple[object, ...]
    star: bool = False


@dataclass(frozen=True)
class ExistsExpr:
    query: object
    negated: bool = False


@dataclass(frozen=True)
class ScalarSubquery:
    query: object


@dataclass(frozen=True)
class SelectItem:
    expr: object
    alias: str | None = None


@dataclass(frozen=True)
class TableRef:
    name: str
    alias: str | None = None


@dataclass(frozen=True)
class SelectQuery:
    items: tuple[SelectItem, ...]
    from_table: TableRef
    where: object | None
    has_star: bool = field(default=False)


class Parser:
    def __init__(self, sql):
        self.tokens = tokenize(sql)
        self.index = 0

    @property
    def current(self):
        return self.tokens[self.index]

    def peek(self, offset=1):
        return self.tokens[min(self.index + offset, len(self.tokens) - 1)]

    def advance(self):
        token = self.current
        self.index += 1
        return token

    def accept(self, kind, value=None):
        token = self.current
        if token.kind != kind:
            return False
        if value is not None and token.value != value:
            return False
        self.advance()
        return True

    def expect(self, kind, value=None):
        token = self.current
        if not self.accept(kind, value):
            wanted = value if value is not None else kind
            raise ParseError(f"expected {wanted!r} but found {token.value!r}")
        return token

    def reject_if_current(self, values, message):
        if self.current.kind in values:
            raise UnsupportedSQL(message)

    def parse_statement(self):
        query = self.parse_select_query()
        self.accept("op", ";")
        if self.current.kind != "eof":
            raise UnsupportedSQL("multiple statements are not supported")
        return query

    def parse_select_query(self, nested=False):
        self.expect("select")
        if self.accept("distinct"):
            raise UnsupportedSQL("DISTINCT is not supported")
        items = [self.parse_select_item()]
        while self.accept("op", ","):
            items.append(self.parse_select_item())
        has_star = any(isinstance(item.expr, Star) for item in items)
        if has_star and len(items) != 1:
            raise UnsupportedSQL("'*' cannot be mixed with other SELECT items")
        self.expect("from")
        table_name = self.parse_identifier()
        alias = None
        if self.accept("as"):
            alias = self.parse_identifier()
        elif self.current.kind == "ident":
            alias = self.parse_identifier()
        where = None
        if self.accept("where"):
            where = self.parse_expr()
        trailing = {
            "group": "GROUP BY is not supported",
            "having": "HAVING is not supported",
            "order": "ORDER BY is not supported",
            "limit": "LIMIT is not supported",
            "union": "set operations are not supported",
        }
        if self.current.kind in trailing:
            raise UnsupportedSQL(trailing[self.current.kind])
        if nested:
            self.expect("op", ")")
        return SelectQuery(tuple(items), TableRef(table_name, alias), where, has_star)

    def parse_select_item(self):
        if self.accept("op", "*"):
            return SelectItem(Star())
        expr = self.parse_expr()
        alias = None
        if self.accept("as"):
            alias = self.parse_identifier()
        return SelectItem(expr, alias)

    def parse_identifier(self):
        token = self.current
        if token.kind in {"ident", "quoted_ident"}:
            self.advance()
            return token.value.lower() if token.kind == "ident" else token.value
        raise ParseError(f"expected identifier but found {token.value!r}")

    def parse_expr(self):
        return self.parse_or()

    def parse_or(self):
        expr = self.parse_and()
        while self.accept("or"):
            right = self.parse_and()
            expr = BinaryExpr(expr, "OR", right)
        return expr

    def parse_and(self):
        expr = self.parse_not()
        while self.accept("and"):
            right = self.parse_not()
            expr = BinaryExpr(expr, "AND", right)
        return expr

    def parse_not(self):
        if self.accept("not"):
            if self.accept("exists"):
                return self.parse_exists_query(negated=True)
            return UnaryExpr("NOT", self.parse_not())
        return self.parse_comparison()

    def parse_comparison(self):
        expr = self.parse_additive()
        if self.current.kind == "is":
            self.advance()
            negated = self.accept("not")
            self.expect("null")
            return BinaryExpr(expr, "IS NOT NULL" if negated else "IS NULL", Literal(None))
        if self.current.kind in {"in", "between", "like"}:
            raise UnsupportedSQL("IN, BETWEEN and LIKE are not supported")
        if self.current.kind == "op" and self.current.value in {"=", "!=", "<", ">", "<=", ">="}:
            op = self.advance().value
            right = self.parse_additive()
            expr = BinaryExpr(expr, op, right)
        return expr

    def parse_additive(self):
        expr = self.parse_multiplicative()
        while self.current.kind == "op" and self.current.value in {"+", "-"}:
            op = self.advance().value
            right = self.parse_multiplicative()
            expr = BinaryExpr(expr, op, right)
        return expr

    def parse_multiplicative(self):
        expr = self.parse_unary()
        while self.current.kind == "op" and self.current.value in {"/", "*"}:
            op = self.advance().value
            right = self.parse_unary()
            expr = BinaryExpr(expr, op, right)
        return expr

    def parse_unary(self):
        if self.current.kind == "op" and self.current.value == "-":
            self.advance()
            return UnaryExpr("-", self.parse_unary())
        if self.current.kind == "op" and self.current.value == "+":
            self.advance()
            return self.parse_unary()
        return self.parse_primary()

    def parse_primary(self):
        token = self.current
        if token.kind == "op" and token.value == "(":
            if self.peek().kind == "select":
                return ScalarSubquery(self.parse_parenthesized_query())
            self.advance()
            expr = self.parse_expr()
            self.expect("op", ")")
            return expr
        if token.kind == "exists":
            self.advance()
            return self.parse_exists_query()
        if token.kind in {"count", "sum", "avg", "min", "max"}:
            return self.parse_function_call()
        if token.kind == "null":
            self.advance()
            return Literal(None)
        if token.kind == "true":
            self.advance()
            return Literal(True)
        if token.kind == "false":
            self.advance()
            return Literal(False)
        if token.kind == "number":
            self.advance()
            return Literal(token.value)
        if token.kind in {"string", "quoted_ident"}:
            self.advance()
            return Literal(token.value) if token.kind == "string" else ColumnRef(token.value)
        if token.kind == "ident":
            first = self.advance().value.lower()
            if self.accept("op", "."):
                if self.accept("op", "*"):
                    return Star(first)
                second = self.parse_identifier()
                return ColumnRef(second, first)
            return ColumnRef(first)
        if token.kind in {"case", "join", "on", "asc", "desc", "by", "when", "then", "else", "end"}:
            raise UnsupportedSQL(f"{token.value!r} syntax is not supported")
        raise ParseError(f"unexpected token {token.value!r}")

    def parse_function_call(self):
        name = self.advance().value
        self.expect("op", "(")
        if self.accept("op", "*"):
            self.expect("op", ")")
            return FunctionCall(name, (), star=True)
        if self.accept("distinct"):
            raise UnsupportedSQL("aggregate DISTINCT is not supported")
        args = [self.parse_expr()]
        while self.accept("op", ","):
            args.append(self.parse_expr())
        self.expect("op", ")")
        return FunctionCall(name, tuple(args))

    def parse_parenthesized_query(self):
        self.expect("op", "(")
        return self.parse_select_query(nested=True)

    def parse_exists_query(self, negated=False):
        self.expect("op", "(")
        query = self.parse_select_query(nested=True)
        return ExistsExpr(query, negated)


def parse_sql(sql):
    return Parser(sql).parse_statement()
