"""Minimum-cost token-edit repair within a budget.

Uniform-cost (Dijkstra) search over parser configurations (input position,
stack).  Nonterminal expansions are zero-cost; edits happen only at terminal
level (insert / delete / substitute, cost 1 each).  Every repair of minimal
cost is kept in a shared packed forest: no candidate is arbitrarily
presented as the unique fix.
"""
from __future__ import annotations

import heapq
from dataclasses import dataclass

from .tokens import EOF_SYMBOL, eof_token, inserted_token, substituted_token


@dataclass(frozen=True)
class Edit:
    kind: str  # 'insert' | 'delete' | 'substitute'
    pos: int
    sym: str = None
    old: str = None

    def describe(self):
        if self.kind == "insert":
            return f"insert {self.sym} before position {self.pos}"
        if self.kind == "delete":
            return f"delete {self.old} at position {self.pos}"
        return f"substitute {self.old} -> {self.sym} at position {self.pos}"


class RepairForest:
    """Shared packed forest of every minimal-cost repair."""

    def __init__(self, start, accepts, edges, cost, tokens):
        self.start = start
        self.accepts = frozenset(accepts)
        self.edges = edges  # config -> list of (Edit|None, child config)
        self.cost = cost
        self.tokens = tokens

    def enumerate(self, limit=100):
        """List up to `limit` distinct minimal edit scripts.

        Several config paths may realise the same script; distinct scripts
        are what the forest promises to keep, so duplicates are collapsed.
        """
        out = []
        seen_scripts = set()
        stack = [(self.start, (), frozenset([self.start]))]
        while stack and len(out) < limit:
            cfg, edits, seen = stack.pop()
            if cfg in self.accepts:
                if edits not in seen_scripts:
                    seen_scripts.add(edits)
                    out.append(edits)
                continue
            for edit, child in self.edges.get(cfg, ()):
                if child not in seen:
                    stack.append((child, edits + ((edit,) if edit is not None else ()),
                                  seen | {child}))
        return out

    def count(self, cap=100000):
        """Number of distinct minimal edit scripts (capped at `cap`)."""
        scripts = set()
        stack = [(self.start, (), frozenset([self.start]))]
        while stack:
            cfg, edits, seen = stack.pop()
            if cfg in self.accepts:
                scripts.add(edits)
                if len(scripts) >= cap:
                    return cap
                continue
            for edit, child in self.edges.get(cfg, ()):
                if child not in seen:
                    stack.append((child, edits + ((edit,) if edit is not None else ()),
                                  seen | {child}))
        return len(scripts)

    def apply(self, edits):
        """Apply one edit script, preserving token identity / source mapping."""
        return apply_edits(self.tokens, edits)


def _out_edges(cfg, tokens, table, grammar):
    i, stack = cfg
    n = len(tokens)
    tok = tokens[i] if i < n else None
    out = []
    if not stack:
        if tok is not None and tok.type != EOF_SYMBOL:
            out.append((Edit("delete", i, old=tok.type), 1, (i + 1, stack)))
        return out
    top = stack[-1]
    rest = stack[:-1]
    if top == EOF_SYMBOL or grammar.is_terminal(top):
        if tok is not None and tok.type == top:
            out.append((None, 0, (i + 1, rest)))
        if tok is not None and tok.type != EOF_SYMBOL:
            out.append((Edit("delete", i, old=tok.type), 1, (i + 1, stack)))
            if top != EOF_SYMBOL and tok.type != top:
                out.append((Edit("substitute", i, sym=top, old=tok.type), 1, (i + 1, rest)))
        if top != EOF_SYMBOL:
            out.append((Edit("insert", i, sym=top), 1, (i, rest)))
    else:
        seen_targets = set()
        for rhs in set(table[top].values()):
            tgt = (i, rest + tuple(reversed(rhs)))
            if tgt not in seen_targets:
                seen_targets.add(tgt)
                out.append((None, 0, tgt))
        if tok is not None and tok.type != EOF_SYMBOL:
            out.append((Edit("delete", i, old=tok.type), 1, (i + 1, stack)))
    return out


def minimal_repairs(tokens, parser, budget=3):
    """All minimum-cost edit scripts (cost <= budget) as a shared forest.

    Returns None when no repair within the budget exists.
    """
    tokens = list(tokens)
    if not tokens or tokens[-1].type != EOF_SYMBOL:
        tokens.append(eof_token(tokens[-1].pos + 1 if tokens else 0))
    n = len(tokens)
    grammar = parser.grammar
    table = parser.table
    start = (0, (EOF_SYMBOL, grammar.start))
    goal = (n, ())

    dist = {start: 0}
    out_edges = {}
    pq = [(0, start)]
    while pq:
        d, u = heapq.heappop(pq)
        if d != dist.get(u):
            continue
        for edit, w, v in _out_edges(u, tokens, table, grammar):
            nd = d + w
            if nd > budget:
                continue
            out_edges.setdefault(u, []).append((edit, w, v))
            if nd < dist.get(v, float("inf")):
                dist[v] = nd
                heapq.heappush(pq, (nd, v))
    if goal not in dist:
        return None
    cost = dist[goal]

    radj = {}
    for u, edges in out_edges.items():
        for _, w, v in edges:
            radj.setdefault(v, []).append((u, w))
    rdist = {goal: 0}
    pq = [(0, goal)]
    while pq:
        d, u = heapq.heappop(pq)
        if d != rdist.get(u):
            continue
        for v, w in radj.get(u, ()):
            nd = d + w
            if nd < rdist.get(v, float("inf")):
                rdist[v] = nd
                heapq.heappush(pq, (nd, v))

    forest_edges = {}
    for u, edges in out_edges.items():
        du = dist.get(u)
        if du is None or du + rdist.get(u, float("inf")) != cost:
            continue
        kept = [(edit, v) for edit, w, v in edges
                if dist.get(v) == du + w
                and du + w + rdist.get(v, float("inf")) == cost]
        if kept:
            forest_edges[u] = kept
    if start != goal and start not in forest_edges:
        return None
    return RepairForest(start, [goal], forest_edges, cost, tokens)


def apply_edits(tokens, edits):
    """Apply an edit script to a token list.

    Source tokens keep their identity; inserted/substituted tokens get fresh
    identities with distinct origins, and every token keeps a source position
    so the repaired stream still maps back to the source text.
    """
    tokens = [t for t in tokens if t.type != EOF_SYMBOL]
    inserts = {}
    deletes = set()
    subs = {}
    for e in edits:
        if e.kind == "insert":
            inserts.setdefault(e.pos, []).append(e)
        elif e.kind == "delete":
            deletes.add(e.pos)
        else:
            subs[e.pos] = e
    out = []
    for idx, tok in enumerate(tokens):
        for e in inserts.get(idx, ()):
            out.append(inserted_token(e.sym, tok.pos))
        if idx in deletes:
            continue
        if idx in subs:
            out.append(substituted_token(subs[idx].sym, tok.pos))
        else:
            out.append(tok)
    end_pos = tokens[-1].pos + 1 if tokens else 0
    for e in inserts.get(len(tokens), ()):
        out.append(inserted_token(e.sym, end_pos))
    out.append(eof_token(end_pos))
    return out
