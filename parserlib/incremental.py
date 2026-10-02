"""Incremental re-parsing: a token edit invalidates only the affected blocks.

The token stream is split into blocks at top-level terminator tokens.  Each
block is parsed independently and cached by the identities (uids) of its
tokens, so editing one token re-parses exactly the blocks whose token
identity sequence changed; untouched blocks keep their existing trees, and
recovery inside one block can never swallow the legal structure of the
following blocks.
"""
from __future__ import annotations

from dataclasses import dataclass

from .errors import Diagnostic
from .grammar import Grammar
from .parser import PredictiveParser
from .tokens import Token
from .tree import Node


@dataclass
class Block:
    index: int
    start: int  # token index, inclusive
    end: int  # token index, exclusive
    tree: Node
    diagnostics: tuple[Diagnostic, ...]
    key: tuple[int, ...]


@dataclass
class EditReport:
    invalidated: list[int]
    reused: list[int]
    diagnostics: list[Diagnostic]


class IncrementalParser:
    def __init__(
        self,
        grammar: Grammar,
        terminator: str = ";",
        parser: PredictiveParser | None = None,
    ) -> None:
        self.parser = parser or PredictiveParser(grammar)
        self.terminator = terminator
        self.tokens: list[Token] = []
        self.blocks: list[Block] = []
        self._cache: dict[tuple[int, ...], tuple[Node, tuple[Diagnostic, ...]]] = {}

    def _split(self, tokens: list[Token]) -> list[tuple[int, int]]:
        spans = []
        start = 0
        for i, tok in enumerate(tokens):
            if tok.kind == self.terminator:
                spans.append((start, i + 1))
                start = i + 1
        if start < len(tokens):
            spans.append((start, len(tokens)))
        return spans

    def _parse_blocks(self, tokens: list[Token]) -> EditReport:
        blocks: list[Block] = []
        invalidated: list[int] = []
        reused: list[int] = []
        diagnostics: list[Diagnostic] = []
        for index, (start, end) in enumerate(self._split(tokens)):
            key = tuple(tok.uid for tok in tokens[start:end])
            cached = self._cache.get(key)
            if cached is None:
                result = self.parser.parse(tokens[start:end])
                diagnostics_local = tuple(
                    Diagnostic(
                        position=start + d.position,
                        offset=d.offset,
                        expected=d.expected,
                        found=d.found,
                        kind=d.kind,
                        message=d.message,
                    )
                    for d in result.diagnostics
                )
                cached = (result.root, diagnostics_local)
                self._cache[key] = cached
                invalidated.append(index)
            else:
                reused.append(index)
            tree, block_diags = cached
            diagnostics.extend(block_diags)
            blocks.append(
                Block(
                    index=index,
                    start=start,
                    end=end,
                    tree=tree,
                    diagnostics=block_diags,
                    key=key,
                )
            )
        self.tokens = list(tokens)
        self.blocks = blocks
        return EditReport(
            invalidated=invalidated, reused=reused, diagnostics=diagnostics
        )

    def load(self, tokens: list[Token]) -> EditReport:
        self._cache.clear()
        return self._parse_blocks(list(tokens))

    def replace(self, index: int, new_token: Token) -> EditReport:
        tokens = list(self.tokens)
        tokens[index] = new_token
        return self._parse_blocks(tokens)

    def insert(self, index: int, token: Token) -> EditReport:
        tokens = list(self.tokens)
        tokens.insert(index, token)
        return self._parse_blocks(tokens)

    def delete(self, index: int) -> EditReport:
        tokens = list(self.tokens)
        del tokens[index]
        return self._parse_blocks(tokens)
