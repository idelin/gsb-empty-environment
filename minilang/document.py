"""A mutable document: local edits re-lex/re-parse and refresh diagnostics."""
from __future__ import annotations

from .interpreter import execute
from .lexer import lex
from .parser import parse_program


class Document:
    def __init__(self, text="", **recovery_kw):
        self._kw = recovery_kw
        self.update(text)

    def update(self, text):
        self.text = text
        self.tokens, lex_diagnostics = lex(text)
        self.program, self.diagnostics = parse_program(
            self.tokens, lex_diagnostics, **self._kw)
        self.env, runtime_diagnostics = execute(self.program)
        self.diagnostics = self.diagnostics + runtime_diagnostics

    def edit(self, start_byte, end_byte, replacement):
        """Apply a local edit given as UTF-8 byte offsets, then refresh the
        tree, diagnostics and environment."""
        data = self.text.encode("utf-8")
        data = data[:start_byte] + replacement.encode("utf-8") + data[end_byte:]
        self.update(data.decode("utf-8"))
