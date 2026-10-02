"""Table-driven predictive parser with FOLLOW-synchronised error recovery.

Recovery invariants:
  * a missing terminal is inserted (with its own inserted identity) and the
    expected set is reported;
  * an unexpected token is either synchronised via FOLLOW (pop without
    consuming) or skipped into an error node that keeps the token, so legal
    following structure is never swallowed;
  * every error step either consumes input or shrinks the stack, so multiple
    errors cannot loop forever, and each input position is reported at most
    once.
"""
from __future__ import annotations

from dataclasses import dataclass

from .grammar import EPS, build_table, compute_first, compute_follow
from .tokens import EOF_SYMBOL, eof_token, inserted_token
from .tree import Node


@dataclass
class ParseError:
    pos: int
    kind: str  # 'missing' | 'unexpected'
    expected: tuple
    detail: str


class TooManyErrors(Exception):
    pass


@dataclass
class ParseResult:
    tree: Node
    errors: list
    consumed: int

    @property
    def ok(self):
        return not self.errors


class PredictiveParser:
    def __init__(self, grammar, table=None):
        self.grammar = grammar
        self.first = compute_first(grammar)
        self.follow = compute_follow(grammar, self.first)
        self.table = table if table is not None else build_table(grammar)

    def parse(self, tokens, start=None, max_errors=100, never_skip=frozenset(),
              expect_eof=True):
        tokens = list(tokens)
        if not tokens or tokens[-1].type != EOF_SYMBOL:
            tokens.append(eof_token(tokens[-1].pos + 1 if tokens else 0))
        start = start or self.grammar.start
        root = Node(start)
        # stack entries: (symbol, node, tree_parent); the symbol stack pops
        # nonterminals at expansion time, so the tree parent is tracked
        # explicitly for attaching error nodes.
        stack = [(start, root, None)]
        if expect_eof:
            stack.insert(0, (EOF_SYMBOL, Node(EOF_SYMBOL), None))
        errors = []
        reported = set()
        i = 0
        steps = 0
        hard_limit = 1000 + 200 * len(tokens)
        while stack:
            steps += 1
            if steps > hard_limit:
                raise RuntimeError("parser failed to converge")
            sym, node, tree_parent = stack[-1]
            tok = tokens[i] if i < len(tokens) else tokens[-1]
            if sym == EOF_SYMBOL or self.grammar.is_terminal(sym):
                if tok.type == sym:
                    node.kind = "token"
                    node.token = tok
                    stack.pop()
                    i += 1
                else:
                    # missing terminal: report the expected set, insert, pop
                    self._report(errors, reported, i, "missing", (sym,), tok)
                    node.kind = "token"
                    node.token = inserted_token(sym, tok.pos)
                    stack.pop()
            else:
                prod = self.table[sym].get(tok.type)
                if prod is not None:
                    stack.pop()
                    if prod:
                        children = [Node(s) for s in prod]
                        leading = [c for c in node.children if c.kind == "error"]
                        node.children = leading + children
                        for s, c in reversed(list(zip(prod, children))):
                            stack.append((s, c, node))
                    else:
                        node.children = [Node(EPS, kind="epsilon")]
                elif (tok.type == EOF_SYMBOL or tok.type in self.follow[sym]
                        or tok.type in never_skip):
                    # FOLLOW sync: abandon the phrase, keep the token
                    self._report(errors, reported, i, "unexpected",
                                 tuple(sorted(self.table[sym])), tok)
                    node.kind = "error"
                    stack.pop()
                else:
                    # skip the token into an error node attached to the parent
                    self._report(errors, reported, i, "unexpected",
                                 tuple(sorted(self.table[sym])), tok)
                    err = Node("<error>", kind="error")
                    leaf = Node(tok.type, kind="token")
                    leaf.token = tok
                    err.children = [leaf]
                    if tree_parent is not None:
                        idx = next((k for k, c in enumerate(tree_parent.children)
                                    if c is node), len(tree_parent.children))
                        tree_parent.children.insert(idx, err)
                    else:
                        # no enclosing tree node yet: keep as leading trivia
                        node.children.append(err)
                    i += 1
            if len(errors) > max_errors:
                raise TooManyErrors(f"aborted after {max_errors} errors")
        return ParseResult(root, errors, i)

    @staticmethod
    def _report(errors, reported, pos, kind, expected, tok):
        if pos in reported:
            return
        reported.add(pos)
        want = "/".join(expected) if expected else "<nothing>"
        errors.append(ParseError(pos, kind, tuple(expected),
                                 f"{kind}: expected {want}, found {tok.type!r}"))
