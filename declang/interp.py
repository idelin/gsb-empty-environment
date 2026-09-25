"""Tree-walking interpreter. No eval, no external compiler."""

from __future__ import annotations

from typing import Dict, List, Tuple

from .lexer import Diagnostic
from .parser import Bin, Decl, IntLit, NameRef, Paren, StrLit


class RuntimeFault(Exception):
    pass


def _eval(node, env: Dict[str, object]) -> object:
    if isinstance(node, IntLit):
        return int(node.tok.text)
    if isinstance(node, StrLit):
        text = node.tok.text
        if len(text) >= 2 and text.endswith('"'):
            return text[1:-1]
        return text[1:]  # unterminated string recovered by the lexer
    if isinstance(node, NameRef):
        if node.tok.text not in env:
            raise RuntimeFault(f"undefined name {node.tok.text!r}")
        return env[node.tok.text]
    if isinstance(node, Paren):
        return _eval(node.inner, env)
    if isinstance(node, Bin):
        left = _eval(node.left, env)
        right = _eval(node.right, env)
        if isinstance(left, int) and isinstance(right, int):
            return left + right
        if isinstance(left, str) and isinstance(right, str):
            return left + right
        raise RuntimeFault(
            f"cannot add {type(left).__name__} and {type(right).__name__}"
        )
    raise RuntimeFault("unrecognized expression node")


def execute(decls: List[Decl]) -> Tuple[Dict[str, object], List[Diagnostic]]:
    env: Dict[str, object] = {}
    diags: List[Diagnostic] = []
    for decl in decls:
        try:
            env[decl.name_tok.text] = _eval(decl.expr, env)
        except RuntimeFault as exc:
            # One runtime fault never blocks later declarations.
            diags.append(
                Diagnostic(
                    f"runtime error: {exc}",
                    decl.name_tok.start, decl.name_tok.end, "runtime",
                )
            )
    return env, diags
