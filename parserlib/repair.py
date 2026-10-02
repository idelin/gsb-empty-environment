"""Budgeted minimum-cost token-edit repair with a shared candidate forest.

The engine runs Dijkstra over (input position, parser stack) states where
edit transitions (insert / delete / substitute, cost 1 each) compete with
zero-cost match/expand transitions, pruned by the budget.  All minimum-cost
solutions are kept: the tight-edge subgraph reachable to the goal forms a
DAG (the shared forest) in which common prefixes of different repairs are
stored once, and every minimum-cost candidate is enumerated from it.  No
candidate is arbitrarily promoted to "the" unique repair.
"""
from __future__ import annotations

import heapq
import itertools
from dataclasses import dataclass, field

from .grammar import Grammar
from .table import ParseTable
from .tokens import EOF_KIND, Token, eof_token, inserted_token, substituted_token

INSERT = "insert"
DELETE = "delete"
SUBSTITUTE = "substitute"


@dataclass(frozen=True)
class Edit:
    op: str
    position: int  # index into the original token stream (EOF excluded)
    kind: str | None = None  # new kind for insert / substitute
    original: str | None = None  # original kind for delete / substitute

    def describe(self) -> str:
        if self.op == INSERT:
            return f"insert {self.kind!r} before token {self.position}"
        if self.op == DELETE:
            return f"delete token {self.position} ({self.original!r})"
        return (
            f"substitute token {self.position} "
            f"({self.original!r} -> {self.kind!r})"
        )


@dataclass
class RepairCandidate:
    cost: int
    edits: tuple[Edit, ...]
    repaired: tuple[Token, ...]  # includes the trailing EOF token
    path: tuple[int, ...]  # forest node ids, witnesses structural sharing

    def kind_sequence(self) -> tuple[str, ...]:
        return tuple(t.kind for t in self.repaired if t.kind != EOF_KIND)


@dataclass
class ForestNode:
    nid: int
    state: tuple
    cost: int
    edges: list[tuple[Edit | None, "ForestNode"]] = field(default_factory=list)


@dataclass
class RepairForest:
    budget: int
    min_cost: int | None
    root: ForestNode | None
    node_count: int
    edge_count: int
    candidates: list[RepairCandidate]
    truncated: bool

    def kind_sequences(self) -> set[tuple[str, ...]]:
        return {c.kind_sequence() for c in self.candidates}


def apply_edits(original: list[Token], edits: tuple[Edit, ...]) -> list[Token]:
    """Apply an edit script, preserving original token identities.

    Original tokens keep their uid/span; inserted tokens get fresh uids and a
    zero-width span at the insertion offset; substituted tokens get fresh
    uids but keep the original span.
    """
    inserts: dict[int, list[Edit]] = {}
    subs: dict[int, Edit] = {}
    deletes: set[int] = set()
    for edit in edits:
        if edit.op == INSERT:
            inserts.setdefault(edit.position, []).append(edit)
        elif edit.op == DELETE:
            deletes.add(edit.position)
        else:
            subs[edit.position] = edit
    out: list[Token] = []
    for i, tok in enumerate(original):
        for ins in inserts.get(i, ()):
            out.append(inserted_token(ins.kind, tok.start))
        if i in deletes:
            continue
        if i in subs:
            out.append(substituted_token(subs[i].kind, tok))
        else:
            out.append(tok)
    tail_at = original[-1].end if original else 0
    for ins in inserts.get(len(original), ()):
        out.append(inserted_token(ins.kind, tail_at))
    out.append(eof_token(tail_at))
    return out


class RepairEngine:
    def __init__(
        self,
        grammar: Grammar,
        table: ParseTable | None = None,
        budget: int = 2,
        max_candidates: int = 256,
        max_stack: int | None = None,
    ) -> None:
        self.grammar = grammar
        self.table = table or ParseTable(grammar)
        self.budget = budget
        self.max_candidates = max_candidates
        self.max_stack = max_stack

    def repair(self, tokens: list[Token]) -> RepairForest:
        original = list(tokens)
        if original and original[-1].kind == EOF_KIND:
            original = original[:-1]
        end = original[-1].end if original else 0
        stream = original + [eof_token(end)]
        n = len(stream)
        max_stack = self.max_stack or (n + 2 * self.budget + 8)
        start = (0, (EOF_KIND, self.grammar.start))
        goal = (n, ())

        transitions = lambda s: self._transitions(s, stream, max_stack)

        dist: dict[tuple, int] = {start: 0}
        counter = itertools.count()
        heap = [(0, next(counter), start)]
        while heap:
            d, _, state = heapq.heappop(heap)
            if d != dist[state]:
                continue
            for nxt, w, _edit in transitions(state):
                nd = d + w
                if nd > self.budget:
                    continue
                if nd < dist.get(nxt, self.budget + 1):
                    dist[nxt] = nd
                    heapq.heappush(heap, (nd, next(counter), nxt))

        if goal not in dist:
            return RepairForest(self.budget, None, None, 0, 0, [], False)
        min_cost = dist[goal]

        # Tight edges: u -w-> v with dist[u] + w == dist[v], both <= min_cost.
        tight: dict[tuple, list] = {}
        rev: dict[tuple, list] = {}
        for state, d in dist.items():
            if d > min_cost:
                continue
            for nxt, w, edit in transitions(state):
                if dist.get(nxt) == d + w:
                    tight.setdefault(state, []).append((edit, nxt))
                    rev.setdefault(nxt, []).append(state)
        reaches_goal = {goal}
        work = [goal]
        while work:
            state = work.pop()
            for prev in rev.get(state, ()):
                if prev not in reaches_goal:
                    reaches_goal.add(prev)
                    work.append(prev)

        nodes: dict[tuple, ForestNode] = {}
        order = []
        seen = {start}
        work = [start]
        while work:
            state = work.pop()
            order.append(state)
            for _edit, nxt in tight.get(state, ()):
                if nxt in reaches_goal and nxt not in seen:
                    seen.add(nxt)
                    work.append(nxt)
        for state in order:
            nodes[state] = ForestNode(nid=len(nodes), state=state, cost=dist[state])
        edge_count = 0
        for state in order:
            for edit, nxt in tight.get(state, ()):
                if nxt in nodes:
                    nodes[state].edges.append((edit, nodes[nxt]))
                    edge_count += 1
        root = nodes[start]

        candidates: list[RepairCandidate] = []
        seen_seqs: set[tuple[str, ...]] = set()
        truncated = False
        work = [(root, (), (root.nid,))]
        while work:
            if len(candidates) >= self.max_candidates:
                truncated = True
                break
            node, edits, path = work.pop()
            if node.state == goal:
                repaired = apply_edits(original, edits)
                seq = tuple(t.kind for t in repaired if t.kind != EOF_KIND)
                if seq not in seen_seqs:
                    seen_seqs.add(seq)
                    candidates.append(
                        RepairCandidate(
                            cost=min_cost,
                            edits=edits,
                            repaired=tuple(repaired),
                            path=path,
                        )
                    )
                continue
            for edit, nxt in node.edges:
                work.append(
                    (nxt, edits + ((edit,) if edit is not None else ()), path + (nxt.nid,))
                )
        candidates.sort(key=lambda c: tuple(e.describe() for e in c.edits))
        return RepairForest(
            budget=self.budget,
            min_cost=min_cost,
            root=root,
            node_count=len(nodes),
            edge_count=edge_count,
            candidates=candidates,
            truncated=truncated,
        )

    def _transitions(self, state: tuple, stream: list[Token], max_stack: int):
        """Zero-cost match/expansion plus unit-cost edit transitions.

        Edits are only ever performed against real tokens: an insertion pops
        an expected terminal (the inserted token is "matched" immediately),
        a deletion drops the current token, a substitution rewrites it.
        Nonterminal expansion itself is free, so no fake token is ever
        conjured up by a table entry.
        """
        i, stack = state
        out = []
        if not stack or i >= len(stream):
            return out
        top = stack[-1]
        lookahead = stream[i]
        real = lookahead.kind != EOF_KIND
        if top == EOF_KIND or top in self.grammar.terminals:
            if lookahead.kind == top:
                out.append(((i + 1, stack[:-1]), 0, None))
            if top != EOF_KIND:
                out.append(((i, stack[:-1]), 1, Edit(INSERT, i, top, None)))
                if real and lookahead.kind != top:
                    out.append(
                        (
                            (i + 1, stack[:-1]),
                            1,
                            Edit(SUBSTITUTE, i, top, lookahead.kind),
                        )
                    )
        else:
            seen_rhs = set()
            for rhs in self.table.entries[top].values():
                if rhs in seen_rhs:
                    continue
                seen_rhs.add(rhs)
                new_stack = stack[:-1] + tuple(reversed(rhs))
                if len(new_stack) <= max_stack:
                    out.append(((i, new_stack), 0, None))
        if real:
            out.append(((i + 1, stack), 1, Edit(DELETE, i, None, lookahead.kind)))
        return out
