"""Iterative precedence-climbing parser (shunting-yard variant).

The parser uses explicit stacks only, so arbitrarily deep nesting cannot
overflow the host call stack; nesting beyond ``limits.max_depth`` is
rejected with a ParseError instead.
"""

from .ast_nodes import Binary, Call, If, ListLit, Literal, Name, Unary
from .errors import ParseError
from .lexer import tokenize

_BINARY_PREC = {
    "or": 1, "and": 2,
    "==": 4, "!=": 4, "<": 4, "<=": 4, ">": 4, ">=": 4,
    "+": 5, "-": 5,
    "*": 6, "/": 6, "//": 6, "%": 6,
}
_UNARY_PREC = {"not": 3, "neg": 7}

_LITERAL_KEYWORDS = {"true": True, "false": False, "null": None}


def parse(source, budget, limits):
    tokens = tokenize(source, budget, limits)
    out = []            # operand stack of AST nodes
    ops = []            # operator / marker stack
    depth = 0           # nesting depth of parens/calls/lists
    expect_operand = True
    pos = 0
    eof = tokens[-1]
    while pos < len(tokens):
        tok = tokens[pos]
        kind = tok.kind
        if kind == "eof":
            break
        if expect_operand:
            if kind == "number" or kind == "string":
                budget.consume(1, "parsing")
                out.append(Literal(tok.value, tok.start, tok.end))
                expect_operand = False
            elif kind == "keyword":
                if tok.value in _LITERAL_KEYWORDS:
                    budget.consume(1, "parsing")
                    out.append(Literal(_LITERAL_KEYWORDS[tok.value],
                                       tok.start, tok.end))
                    expect_operand = False
                elif tok.value == "not":
                    ops.append(["unary", "not", _UNARY_PREC["not"],
                                tok.start])
                elif tok.value == "if":
                    nxt = tokens[pos + 1]
                    if nxt.kind != "lparen":
                        raise ParseError("expected '(' after 'if'",
                                         tok.start, tok.end)
                    pos += 1
                    depth += 1
                    _check_depth(depth, limits, tok.start, tok.end)
                    ops.append(["if", 0, tok.start])
                else:
                    raise ParseError("expected an expression",
                                     tok.start, tok.end)
            elif kind == "ident":
                nxt = tokens[pos + 1]
                if nxt.kind == "lparen":
                    pos += 1
                    depth += 1
                    _check_depth(depth, limits, tok.start, tok.end)
                    ops.append(["call", tok.value, tok.start, tok.end, 0,
                                tok.start])
                else:
                    budget.consume(1, "parsing")
                    out.append(Name(tok.value, tok.start, tok.end))
                    expect_operand = False
            elif kind == "op" and tok.value == "-":
                ops.append(["unary", "neg", _UNARY_PREC["neg"], tok.start])
            elif kind == "lparen":
                depth += 1
                _check_depth(depth, limits, tok.start, tok.end)
                ops.append(["paren", tok.start])
            elif kind == "lbracket":
                depth += 1
                _check_depth(depth, limits, tok.start, tok.end)
                ops.append(["list", 0, tok.start])
            elif kind == "rparen" and ops and ops[-1][0] in ("call", "if"):
                _close_paren(ops, out, tok, True, budget)
                depth -= 1
                expect_operand = False
            elif kind == "rbracket" and ops and ops[-1][0] == "list":
                _close_bracket(ops, out, tok, True, budget, limits)
                depth -= 1
                expect_operand = False
            else:
                raise ParseError("expected an expression",
                                 tok.start, tok.end)
        else:
            if kind == "op" and tok.value in _BINARY_PREC:
                _pop_ops(ops, out, _BINARY_PREC[tok.value], budget)
                ops.append(["binary", tok.value, _BINARY_PREC[tok.value],
                            tok.start])
                expect_operand = True
            elif kind == "keyword" and tok.value in ("and", "or"):
                _pop_ops(ops, out, _BINARY_PREC[tok.value], budget)
                ops.append(["binary", tok.value, _BINARY_PREC[tok.value],
                            tok.start])
                expect_operand = True
            elif kind == "comma":
                while ops and ops[-1][0] in ("binary", "unary"):
                    _apply(ops.pop(), out, budget)
                if not ops or ops[-1][0] not in ("call", "list", "if"):
                    raise ParseError("unexpected ','", tok.start, tok.end)
                marker = ops[-1]
                if marker[0] == "call":
                    marker[4] += 1
                else:
                    marker[1] += 1
                expect_operand = True
            elif kind == "rparen":
                while ops and ops[-1][0] in ("binary", "unary"):
                    _apply(ops.pop(), out, budget)
                if not ops or ops[-1][0] not in ("paren", "call", "if"):
                    raise ParseError("unmatched ')'", tok.start, tok.end)
                _close_paren(ops, out, tok, False, budget)
                depth -= 1
                expect_operand = False
            elif kind == "rbracket":
                while ops and ops[-1][0] in ("binary", "unary"):
                    _apply(ops.pop(), out, budget)
                if not ops or ops[-1][0] != "list":
                    raise ParseError("unmatched ']'", tok.start, tok.end)
                _close_bracket(ops, out, tok, False, budget, limits)
                depth -= 1
                expect_operand = False
            else:
                raise ParseError("expected an operator",
                                 tok.start, tok.end)
        pos += 1
    if expect_operand:
        raise ParseError("expected an expression", eof.start, eof.end)
    while ops:
        entry = ops.pop()
        tag = entry[0]
        if tag in ("binary", "unary"):
            _apply(entry, out, budget)
        elif tag == "paren":
            raise ParseError("unclosed '('", entry[1], entry[1] + 1)
        elif tag == "call":
            raise ParseError("unclosed '(' in call to '%s'" % entry[1],
                             entry[5], entry[5] + 1)
        elif tag == "if":
            raise ParseError("unclosed '(' after 'if'",
                             entry[2], entry[2] + 1)
        else:
            raise ParseError("unclosed '['", entry[2], entry[2] + 1)
    if len(out) != 1:
        raise ParseError("could not parse expression", 0, eof.end)
    return out[0]


def _check_depth(depth, limits, start, end):
    if depth > limits.max_depth:
        raise ParseError("nesting too deep", start, end)


def _pop_ops(ops, out, prec, budget):
    while ops and ops[-1][0] in ("binary", "unary") and ops[-1][2] >= prec:
        _apply(ops.pop(), out, budget)


def _apply(entry, out, budget):
    budget.consume(1, "parsing")
    if entry[0] == "unary":
        operand = out.pop()
        out.append(Unary(entry[1], operand, entry[3], operand.end))
    else:
        right = out.pop()
        left = out.pop()
        out.append(Binary(entry[1], left, right, left.start, right.end))


def _close_paren(ops, out, tok, just_opened, budget):
    marker = ops.pop()
    if marker[0] == "paren":
        return
    if marker[0] == "call":
        argc = marker[4] + (0 if just_opened else 1)
        args = out[len(out) - argc:]
        del out[len(out) - argc:]
        budget.consume(1, "parsing")
        out.append(Call(marker[1], marker[2], marker[3], list(args),
                        marker[5], tok.end))
    else:  # "if"
        argc = marker[1] + (0 if just_opened else 1)
        if argc != 3:
            raise ParseError("'if' expects 3 arguments", marker[2], tok.end)
        args = out[len(out) - 3:]
        del out[len(out) - 3:]
        budget.consume(1, "parsing")
        out.append(If(args[0], args[1], args[2], marker[2], tok.end))


def _close_bracket(ops, out, tok, just_opened, budget, limits):
    marker = ops.pop()
    argc = marker[1] + (0 if just_opened else 1)
    if argc > limits.max_collection:
        raise ParseError("collection too large", marker[2], tok.end)
    items = out[len(out) - argc:]
    del out[len(out) - argc:]
    budget.consume(1, "parsing")
    out.append(ListLit(list(items), marker[2], tok.end))
