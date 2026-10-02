"""Incremental re-parsing: token edits invalidate only the affected blocks.

The stream is parsed as a sequence of blocks (e.g. statements).  Recovery
inside a block never skips tokens that can start a new block, so a damaged
block cannot swallow legal following structure.  On update, a token-level
diff maps unchanged regions to previously parsed blocks, which are reused
by identity; only blocks overlapping the changed region are re-parsed.
"""
from __future__ import annotations

import difflib
from dataclasses import dataclass, replace

from .grammar import compute_first
from .tokens import EOF_SYMBOL
from .tree import Node


@dataclass
class Block:
    tree: Node
    errors: list
    start: int
    end: int


@dataclass
class IncrementalResult:
    blocks: list
    reused: int
    reparsed: int

    @property
    def errors(self):
        return [e for b in self.blocks for e in b.errors]

    @property
    def ok(self):
        return not self.errors

    def tree(self):
        root = Node("program")
        root.children = [b.tree for b in self.blocks]
        return root


class IncrementalParser:
    def __init__(self, parser, block_symbol):
        self.parser = parser
        self.block_symbol = block_symbol
        first = compute_first(parser.grammar)
        self._never_skip = frozenset(first[block_symbol]) | {EOF_SYMBOL}
        self.tokens = []
        self.blocks = []

    def _parse_block(self, tokens, i):
        res = self.parser.parse(
            tokens[i:],
            start=self.block_symbol,
            never_skip=self._never_skip,
            expect_eof=False,
        )
        errors = [replace(e, pos=e.pos + i) for e in res.errors]
        consumed = res.consumed if res.consumed > 0 else 1
        return Block(res.tree, errors, i, i + consumed), consumed

    def _parse_all(self, tokens):
        blocks = []
        i = 0
        while tokens[i].type != EOF_SYMBOL:
            blk, consumed = self._parse_block(tokens, i)
            blocks.append(blk)
            i += consumed
        return blocks

    def parse(self, tokens):
        self.tokens = list(tokens)
        self.blocks = self._parse_all(self.tokens)
        return IncrementalResult(self.blocks, reused=0, reparsed=len(self.blocks))

    def update(self, new_tokens):
        new_tokens = list(new_tokens)
        old_keys = [(t.type, t.value) for t in self.tokens]
        new_keys = [(t.type, t.value) for t in new_tokens]
        matcher = difflib.SequenceMatcher(a=old_keys, b=new_keys, autojunk=False)
        reusable = []
        for tag, i1, i2, j1, j2 in matcher.get_opcodes():
            if tag != "equal":
                continue
            shift = j1 - i1
            for b in self.blocks:
                if i1 <= b.start and b.end <= i2:
                    reusable.append(Block(b.tree, b.errors, b.start + shift,
                                          b.end + shift))
        reusable.sort(key=lambda b: b.start)
        blocks = []
        reused = 0
        reparsed = 0
        i = 0
        ri = 0
        while new_tokens[i].type != EOF_SYMBOL:
            if ri < len(reusable) and reusable[ri].start == i:
                blocks.append(reusable[ri])
                i = reusable[ri].end
                ri += 1
                reused += 1
            else:
                blk, consumed = self._parse_block(new_tokens, i)
                while ri < len(reusable) and reusable[ri].start < blk.end:
                    ri += 1
                blocks.append(blk)
                reparsed += 1
                i += consumed
        self.tokens = new_tokens
        self.blocks = blocks
        return IncrementalResult(blocks, reused=reused, reparsed=reparsed)
