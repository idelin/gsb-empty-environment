"""Document facade: keeps source, recovered tree, diagnostics and environment
in sync. A local byte-range edit re-runs lex -> recover-parse -> execute and
updates the diagnostics (incremental reuse is not needed at this scale)."""

from __future__ import annotations

from dataclasses import dataclass
from typing import List, Tuple

from .interp import execute
from .lexer import Diagnostic, Token, lex
from .parser import Decl, parse_program


_SEVERITY_ORDER = {"error": 0, "runtime": 1}


@dataclass
class Document:
    text: str = ""

    def __init__(
        self,
        text: str = "",
        max_cost: int = 2,
        max_candidates: int = 8,
        max_states: int = 60000,
    ):
        self.max_cost = max_cost
        self.max_candidates = max_candidates
        self.max_states = max_states
        self.tokens: List[Token] = []
        self.decls: List[Decl] = []
        self.env = {}
        self.diagnostics: List[Diagnostic] = []
        self.update(text)

    def update(self, text: str) -> "Document":
        self.text = text
        tokens, lex_diags = lex(text)
        self.tokens = tokens
        decls, parse_diags = parse_program(
            tokens,
            max_cost=self.max_cost,
            max_candidates=self.max_candidates,
            max_states=self.max_states,
        )
        self.decls = decls
        self.env, runtime_diags = execute(decls)
        diags = lex_diags + parse_diags + runtime_diags
        diags.sort(key=lambda d: (d.start, _SEVERITY_ORDER.get(d.severity, 9)))
        self.diagnostics = diags
        return self

    def edit(self, start: int, end: int, replacement: str) -> "Document":
        """Replace UTF-8 byte range [start, end) with replacement, then
        update the tree and diagnostics."""
        data = self.text.encode("utf-8")
        if not (0 <= start <= end <= len(data)):
            raise ValueError("edit range out of bounds")
        new_bytes = data[:start] + replacement.encode("utf-8") + data[end:]
        return self.update(new_bytes.decode("utf-8"))
