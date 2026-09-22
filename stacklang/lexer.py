from .errors import LexError, Span

KEYWORDS = {
    "let", "if", "else", "while", "break",
    "true", "false", "and", "or", "not",
}

SYMBOLS = (
    "==", "!=", "<=", ">=",
    "+", "-", "*", "/", "%", "<", ">", "!",
    "=", "(", ")", "{", "}", ";",
)

TOKEN_KIND = {
    "+": "PLUS", "-": "MINUS", "*": "STAR", "/": "SLASH", "%": "PERCENT",
    "<": "LT", ">": "GT", "<=": "LE", ">=": "GE", "==": "EQ", "!=": "NE",
    "!": "BANG", "=": "ASSIGN",
    "(": "LPAREN", ")": "RPAREN", "{": "LBRACE", "}": "RBRACE", ";": "SEMI",
}


class Token:
    __slots__ = ("kind", "value", "start", "end")

    def __init__(self, kind, value, start, end):
        self.kind = kind
        self.value = value
        self.start = start
        self.end = end

    def __repr__(self):
        return f"Token({self.kind}, {self.value!r}, {self.start}..{self.end})"


def _is_ident_start(ch):
    return ch == "_" or ("a" <= ch <= "z") or ("A" <= ch <= "Z")


def _is_ident_cont(ch):
    return _is_ident_start(ch) or ("0" <= ch <= "9")


def tokenize(source):
    """Tokenize a UTF-8 source string; offsets are UTF-8 byte offsets."""
    text = source
    pos = 0
    byte_pos = 0
    tokens = []
    n = len(text)

    def advance():
        nonlocal pos, byte_pos
        ch = text[pos]
        pos += 1
        byte_pos += len(ch.encode("utf-8"))
        return ch

    while pos < n:
        ch = text[pos]
        if ch in " \t\r\n":
            advance()
            continue

        start = byte_pos
        if ch == "#":
            while pos < n and text[pos] != "\n":
                advance()
            continue

        if "0" <= ch <= "9":
            digits = ""
            while pos < n and "0" <= text[pos] <= "9":
                digits += advance()
            tokens.append(Token("INT", int(digits), start, byte_pos))
            continue

        if _is_ident_start(ch):
            name = ""
            while pos < n and _is_ident_cont(text[pos]):
                name += advance()
            kind = name.upper() if name in KEYWORDS else "NAME"
            tokens.append(Token(kind, name, start, byte_pos))
            continue

        matched = None
        for sym in SYMBOLS:
            if text.startswith(sym, pos):
                matched = sym
                break
        if matched is not None:
            for _ in range(len(matched)):
                advance()
            tokens.append(Token(TOKEN_KIND[matched], matched, start, byte_pos))
            continue

        end = byte_pos + len(ch.encode("utf-8"))
        bad = ch
        advance()
        raise LexError(f"unexpected character {bad!r}", Span(start, end))

    tokens.append(Token("EOF", None, byte_pos, byte_pos))
    return tokens
