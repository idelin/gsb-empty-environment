from .errors import LangError

KEYWORDS = {
    "import", "export", "from", "const", "var", "fn", "return",
    "if", "else", "while", "print", "true", "false", "and", "or",
    "not", "nil",
}

SINGLE_PUNCT = set("(){}[],;=+-*/%<>")
DOUBLE_PUNCT = {"==", "!=", "<=", ">="}

ESCAPES = {"n": "\n", "t": "\t", '"': '"', "\\": "\\"}


class Token:
    __slots__ = ("kind", "value", "start", "end")

    def __init__(self, kind, value, start, end):
        self.kind = kind      # 'num' | 'str' | 'name' | 'kw' | 'punct' | 'eof'
        self.value = value
        self.start = start    # UTF-8 byte offset, inclusive
        self.end = end        # UTF-8 byte offset, exclusive

    def __repr__(self):
        return f"Token({self.kind}, {self.value!r}, {self.start}, {self.end})"


def byte_offsets(src):
    """Map character index -> UTF-8 byte offset."""
    offsets = [0] * (len(src) + 1)
    total = 0
    for i, ch in enumerate(src):
        offsets[i] = total
        total += len(ch.encode("utf-8"))
    offsets[len(src)] = total
    return offsets


def tokenize(src):
    offs = byte_offsets(src)
    toks = []
    i = 0
    n = len(src)
    while i < n:
        ch = src[i]
        if ch in " \t\r\n":
            i += 1
            continue
        if ch == "#":
            while i < n and src[i] != "\n":
                i += 1
            continue
        start = i
        if ch.isdigit():
            while i < n and src[i].isdigit():
                i += 1
            if i + 1 < n and src[i] == "." and src[i + 1].isdigit():
                i += 1
                while i < n and src[i].isdigit():
                    i += 1
            toks.append(Token("num", src[start:i], offs[start], offs[i]))
            continue
        if ch == '"':
            i += 1
            buf = []
            while True:
                if i >= n:
                    raise LangError("unterminated string", offs[start], offs[n])
                c = src[i]
                if c == '"':
                    i += 1
                    break
                if c == "\\":
                    i += 1
                    if i >= n or src[i] not in ESCAPES:
                        raise LangError("invalid escape sequence",
                                        offs[start], offs[min(i, n)])
                    buf.append(ESCAPES[src[i]])
                    i += 1
                else:
                    buf.append(c)
                    i += 1
            toks.append(Token("str", "".join(buf), offs[start], offs[i]))
            continue
        if ch.isalpha() or ch == "_":
            while i < n and (src[i].isalnum() or src[i] == "_"):
                i += 1
            word = src[start:i]
            kind = "kw" if word in KEYWORDS else "name"
            toks.append(Token(kind, word, offs[start], offs[i]))
            continue
        if src[i:i + 2] in DOUBLE_PUNCT:
            toks.append(Token("punct", src[i:i + 2], offs[i], offs[i + 2]))
            i += 2
            continue
        if ch in SINGLE_PUNCT:
            toks.append(Token("punct", ch, offs[i], offs[i + 1]))
            i += 1
            continue
        raise LangError(f"unexpected character {ch!r}", offs[i], offs[i + 1])
    toks.append(Token("eof", "", offs[n], offs[n]))
    return toks
