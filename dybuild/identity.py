"""Logical file identity, content hashing and directory fingerprints."""
from __future__ import annotations

import hashlib
import posixpath
from typing import Callable, Iterable, Sequence

CONTENT = "file"
DIRECTORY = "dir"
MISSING = "missing"

# ---------------------------------------------------------------------------
# Logical identity / path aliases
# ---------------------------------------------------------------------------

def canon(path: str) -> str:
    """Normalize a workspace-relative logical path (POSIX, no leading '/')."""
    p = posixpath.normpath(path.replace("\\", "/"))
    if p in (".", ""):
        return "."
    while p.startswith("./"):
        p = p[2:]
    if p.startswith("../") or p == "..":
        raise ValueError(f"path escapes workspace: {path!r}")
    return p


def make_alias_map(aliases: Iterable[tuple[str, str]] = ()) -> dict[str, str]:
    """Return {canonical alias -> canonical logical identity}."""
    out: dict[str, str] = {}
    for alias, logical in aliases:
        out[canon(alias)] = canon(logical)
    return out


def resolve(path: str, aliases: dict[str, str]) -> str:
    c = canon(path)
    return aliases.get(c, c)


# ---------------------------------------------------------------------------
# Content / fingerprints
# ---------------------------------------------------------------------------

def digest_bytes(data: bytes) -> str:
    return "sha256:" + hashlib.sha256(data).hexdigest()


def digest_text(text: str) -> str:
    return digest_bytes(text.encode("utf-8"))


def digest_missing() -> str:
    return "missing"


# --- Directory sort rules ---------------------------------------------------

SortRule = Callable[[list[tuple[str, str]]], list[tuple[str, str]]]


def sort_lex(entries: list[tuple[str, str]]) -> list[tuple[str, str]]:
    return sorted(entries, key=lambda nt: nt[0])


def sort_type_name(entries: list[tuple[str, str]]) -> list[tuple[str, str]]:
    order = {CONTENT: 0, DIRECTORY: 1}
    return sorted(entries, key=lambda nt: (order.get(nt[1], 2), nt[0]))


def sort_name_desc(entries: list[tuple[str, str]]) -> list[tuple[str, str]]:
    return sorted(entries, key=lambda nt: nt[0], reverse=True)


SORT_RULES: dict[str, SortRule] = {
    "lex": sort_lex,
    "type_name": sort_type_name,
    "name_desc": sort_name_desc,
}


def order_entries(entries: Sequence[tuple[str, str]], rule: str) -> list[tuple[str, str]]:
    try:
        fn = SORT_RULES[rule]
    except KeyError:
        raise ValueError(f"unknown sort rule: {rule!r}") from None
    return fn(list(entries))


def directory_fingerprint(entries: Sequence[tuple[str, str]], rule: str) -> str:
    ordered = order_entries(entries, rule)
    h = hashlib.sha256()
    h.update(rule.encode())
    h.update(b"\0")
    for name, kind in ordered:
        h.update(kind.encode())
        h.update(b":")
        h.update(name.encode())
        h.update(b"\0")
    return "dirfp:" + h.hexdigest()
