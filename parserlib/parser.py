"""Table-driven predictive parser with FOLLOW-set synchronised recovery.

Recovery invariants:
  * every loop iteration either consumes an input token or pops a stack
    symbol, so multiple errors can never cause unbounded popping;
  * skipped tokens are retained in error nodes attached to the tree;
  * synchronisation stops as soon as the lookahead can continue a legal
    structure, so following valid input is never swallowed;
  * diagnostics are de-duplicated by token position.
"""
from __future__ import annotations

from dataclasses import dataclass

from .errors import Diagnostic, DiagnosticSink
from .grammar import EPSILON, Grammar
from .table import ParseTable
from .tokens import EOF_KIND, Token, eof_token
from .tree import Node


@dataclass
class _Frame:
    symbol: str
    node: Node


@dataclass
class ParseResult:
    root: Node
    diagnostics: list[Diagnostic]
    tokens: list[Token]

    @property
    def ok(self) -> bool:
        return not self.diagnostics


class ParserInternalError(RuntimeError):
    pass


class PredictiveParser:
    def __init__(
        self,
        grammar: Grammar,
        table: ParseTable | None = None,
        recover: bool = True,
        max_steps: int | None = None,
    ) -> None:
        self.grammar = grammar
        self.table = table or ParseTable(grammar)
        self.recover = recover
        self.max_steps = max_steps
        self.nullable = grammar.nullable_set()

    def parse(self, tokens: list[Token]) -> ParseResult:
        stream = list(tokens)
        if not stream or stream[-1].kind != EOF_KIND:
            end = stream[-1].end if stream else 0
            stream = stream + [eof_token(end)]
        root = Node(self.grammar.start)
        stack = [_Frame(EOF_KIND, Node(EOF_KIND)), _Frame(self.grammar.start, root)]
        sink = DiagnosticSink()
        i = 0
        steps = 0
        step_bound = self.max_steps or max(256, 64 * (len(stream) + 8))
        aborted = False
        while stack and not aborted:
            steps += 1
            if steps > step_bound:
                raise ParserInternalError(
                    "recovery did not terminate within the step bound"
                )
            top = stack[-1]
            lookahead = stream[i]
            if top.symbol in self.grammar.terminals or top.symbol == EOF_KIND:
                if top.symbol == lookahead.kind:
                    top.node.token = lookahead
                    stack.pop()
                    i += 1
                elif top.symbol == EOF_KIND:
                    # Defensive: trailing garbage under the EOF frame.  Each
                    # iteration consumes a token, so this cannot loop.
                    sink.report(
                        self._diag(i, lookahead, (EOF_KIND,), "unexpected-token")
                    )
                    if not self.recover:
                        aborted = True
                    else:
                        root.children.append(Node.error([lookahead]))
                        i += 1
                else:
                    sink.report(
                        self._diag(i, lookahead, (top.symbol,), "missing-terminal")
                    )
                    if not self.recover:
                        aborted = True
                    else:
                        top.node.is_error = True
                        top.node.note = f"missing {top.symbol}"
                        stack.pop()
            else:
                rhs = self.table.get(top.symbol, lookahead.kind)
                if rhs is not None:
                    stack.pop()
                    if not rhs:
                        top.node.children.append(Node(EPSILON))
                    else:
                        children = [Node(sym) for sym in rhs]
                        top.node.children.extend(children)
                        for sym, child in zip(reversed(rhs), reversed(children)):
                            stack.append(_Frame(sym, child))
                    continue
                expected = self.table.expected(top.symbol)
                if not self.recover:
                    sink.report(self._diag(i, lookahead, expected, "unexpected-token"))
                    aborted = True
                    continue
                follow = self.table.follow[top.symbol]
                if lookahead.kind in follow or lookahead.kind == EOF_KIND:
                    # Synchronise on FOLLOW: abandon this phrase without
                    # consuming the lookahead.
                    sink.report(self._diag(i, lookahead, expected, "unexpected-token"))
                    top.node.is_error = True
                    top.node.note = "aborted: synchronised on FOLLOW"
                    stack.pop()
                elif top.symbol in self.nullable and self._viable(
                    lookahead.kind, stack[:-1]
                ):
                    # Nullable phrase: derive epsilon silently (consumes
                    # nothing) and let the enclosing context deal with the
                    # lookahead, so legal following structure is kept.  Only
                    # viable if the remaining stack can actually continue
                    # with the lookahead; otherwise skip the token here.
                    top.node.children.append(Node(EPSILON))
                    top.node.note = "recovered via epsilon"
                    stack.pop()
                else:
                    # Skip the offending token but retain it in an error node.
                    sink.report(self._diag(i, lookahead, expected, "unexpected-token"))
                    top.node.children.append(Node.error([lookahead]))
                    i += 1
        return ParseResult(root=root, diagnostics=sink.items, tokens=stream)

    def _viable(self, kind: str, stack: list[_Frame]) -> bool:
        """Can the frames below the top consume ``kind`` without skipping it?

        Terminal frames may be popped as "missing", nonterminals either
        accept the kind, synchronise on it, or (if nullable) defer further
        down.  Anything else means the token would be skipped anyway.
        """
        for frame in reversed(stack):
            sym = frame.symbol
            if sym in self.grammar.terminals or sym == EOF_KIND:
                if sym == kind:
                    return True
                continue
            if kind in self.table.entries[sym]:
                return True
            if sym in self.nullable:
                continue
            if kind in self.table.follow[sym]:
                return True
            return False
        return False

    @staticmethod
    def _diag(position: int, lookahead: Token, expected: tuple[str, ...], kind: str) -> Diagnostic:
        expected = tuple(sorted(expected))
        if kind == "missing-terminal":
            message = (
                f"missing {expected[0]!r} before {lookahead.kind!r} "
                f"at token {position}"
            )
        else:
            want = ", ".join(repr(e) for e in expected)
            message = (
                f"unexpected {lookahead.kind!r} at token {position}; "
                f"expected one of: {want}"
            )
        return Diagnostic(
            position=position,
            offset=lookahead.start,
            expected=expected,
            found=lookahead.kind,
            kind=kind,
            message=message,
        )
