"""AST nodes. Nodes reference the exact tokens they were built from, so
original token identity and virtual-token markers survive into the tree."""
from __future__ import annotations

from dataclasses import dataclass, field

from .tokens import Token


@dataclass
class Num:
    value: object
    token: Token


@dataclass
class Str:
    value: str
    token: Token


@dataclass
class Var:
    name: str
    token: Token


@dataclass
class BinOp:
    op: str
    left: object
    right: object
    token: Token


@dataclass
class Paren:
    open_token: Token
    expr: object
    close_token: Token


@dataclass
class LetDecl:
    let_token: Token
    name_token: Token
    expr: object
    semi_token: Token
    edits: list = field(default_factory=list)  # recovery edits applied

    @property
    def name(self):
        return self.name_token.text

    @property
    def start(self):
        return self.let_token.start

    @property
    def end(self):
        return self.semi_token.end


@dataclass
class Program:
    decls: list
