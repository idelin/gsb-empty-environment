"""Parser: token stream -> AST. Nodes carry UTF-8 byte spans."""

from .lexer import tokenize


class ParseError(Exception):
    def __init__(self, message, start=None, end=None):
        super().__init__(message)
        self.start = start
        self.end = end


class Node:
    __slots__ = ("start", "end")


class Sym(Node):
    __slots__ = ("name",)

    def __init__(self, name, start, end):
        self.name = name
        self.start = start
        self.end = end

    def __repr__(self):
        return f"Sym({self.name!r})"


class Num(Node):
    __slots__ = ("value",)

    def __init__(self, value, start, end):
        self.value = value
        self.start = start
        self.end = end

    def __repr__(self):
        return f"Num({self.value!r})"


class Str(Node):
    __slots__ = ("value",)

    def __init__(self, value, start, end):
        self.value = value
        self.start = start
        self.end = end

    def __repr__(self):
        return f"Str({self.value!r})"


class List(Node):
    __slots__ = ("items",)

    def __init__(self, items, start, end):
        self.items = items
        self.start = start
        self.end = end

    def __repr__(self):
        return f"List({self.items!r})"


def _number(text):
    try:
        return int(text)
    except ValueError:
        return float(text)


def parse(source):
    """Parse source into a list of top-level forms."""
    tokens = tokenize(source)
    pos = 0
    n = len(tokens)

    def parse_expr():
        nonlocal pos
        tok = tokens[pos]
        pos += 1
        if tok.kind == "lparen":
            items = []
            while True:
                if pos >= n:
                    raise ParseError("unterminated list", tok.start, tok.end)
                if tokens[pos].kind == "rparen":
                    end = tokens[pos].end
                    pos += 1
                    return List(items, tok.start, end)
                items.append(parse_expr())
        if tok.kind == "rparen":
            raise ParseError("unexpected ')'", tok.start, tok.end)
        if tok.kind == "number":
            return Num(_number(tok.text), tok.start, tok.end)
        if tok.kind == "string":
            return Str(tok.text, tok.start, tok.end)
        return Sym(tok.text, tok.start, tok.end)

    nodes = []
    while pos < n:
        nodes.append(parse_expr())
    return nodes
