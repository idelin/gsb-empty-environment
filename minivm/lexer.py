"""Lexer for the mini language.

All source positions are UTF-8 *byte* offsets (half-open [start, end)
intervals), not character indices.
"""

_DELIMS = set('()";')
_NUMBER_CHARS = set("0123456789+-.")


class LexError(Exception):
    def __init__(self, message, start, end):
        super().__init__(f"{message} (bytes {start}..{end})")
        self.start = start
        self.end = end


class Token:
    __slots__ = ("kind", "text", "start", "end")

    def __init__(self, kind, text, start, end):
        self.kind = kind  # 'lparen' | 'rparen' | 'symbol' | 'number' | 'string'
        self.text = text
        self.start = start  # utf-8 byte offset, inclusive
        self.end = end      # utf-8 byte offset, exclusive

    def __repr__(self):
        return f"Token({self.kind}, {self.text!r}, {self.start}, {self.end})"


def _byte_offsets(source):
    """Map character index -> utf-8 byte offset."""
    offsets = [0]
    total = 0
    for ch in source:
        total += len(ch.encode("utf-8"))
        offsets.append(total)
    return offsets


def _is_number(text):
    if not text:
        return False
    if text[0] not in _NUMBER_CHARS:
        return False
    try:
        int(text)
        return True
    except ValueError:
        pass
    try:
        float(text)
        return any(c.isdigit() for c in text)
    except ValueError:
        return False


_ESCAPES = {"n": "\n", "t": "\t", "r": "\r", '"': '"', "\\": "\\"}


def tokenize(source):
    offsets = _byte_offsets(source)
    tokens = []
    i = 0
    n = len(source)
    while i < n:
        ch = source[i]
        if ch.isspace():
            i += 1
            continue
        if ch == ";":
            while i < n and source[i] != "\n":
                i += 1
            continue
        if ch == "(":
            tokens.append(Token("lparen", "(", offsets[i], offsets[i + 1]))
            i += 1
            continue
        if ch == ")":
            tokens.append(Token("rparen", ")", offsets[i], offsets[i + 1]))
            i += 1
            continue
        if ch == '"':
            j = i + 1
            buf = []
            while True:
                if j >= n:
                    raise LexError("unterminated string", offsets[i], offsets[n])
                c = source[j]
                if c == "\\":
                    if j + 1 >= n:
                        raise LexError("bad escape", offsets[j], offsets[n])
                    buf.append(_ESCAPES.get(source[j + 1], source[j + 1]))
                    j += 2
                elif c == '"':
                    j += 1
                    break
                else:
                    buf.append(c)
                    j += 1
            tokens.append(Token("string", "".join(buf), offsets[i], offsets[j]))
            i = j
            continue
        if ch in _DELIMS:
            raise LexError(f"unexpected character {ch!r}", offsets[i], offsets[i + 1])
        # symbol / number: run of non-delimiter, non-space characters
        j = i
        while j < n and not source[j].isspace() and source[j] not in _DELIMS:
            j += 1
        text = source[i:j]
        kind = "number" if _is_number(text) else "symbol"
        tokens.append(Token(kind, text, offsets[i], offsets[j]))
        i = j
    return tokens
