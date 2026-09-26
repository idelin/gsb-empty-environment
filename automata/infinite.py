"""Infinitude detection with explicit cycle evidence.

The intersection language is infinite iff some reachable product state that
can still reach an accepting state lies on a cycle.  Cycles that cannot lead
to acceptance ("irrelevant loops") must not produce an infinite verdict, so
we restrict the search to states that are both reachable and co-reachable.

The returned evidence ``(prefix, loop, suffix)`` certifies infinitude:
``prefix + loop**i + suffix`` is accepted for every ``i >= 0``.  The evidence
is independently checkable with ``verify_evidence`` which only uses the
public automaton interface.
"""

from collections import deque
from typing import NamedTuple, Optional


class CycleEvidence(NamedTuple):
    prefix: str   # word taking the initial state to the cycle state
    loop: str     # non-empty word taking the cycle state to itself
    suffix: str   # word taking the cycle state to an accepting state

    def witness(self, repeats):
        """The accepted witness word using ``repeats`` copies of the loop."""
        return self.prefix + self.loop * repeats + self.suffix


def _reachable(product):
    """BFS from the initial product state; returns (parent, adj)."""
    start = product.initial()
    parent = {start: None}          # state -> (prev_state, symbol)
    adj = {}
    queue = deque([start])
    while queue:
        state = queue.popleft()
        row = {}
        for sym in product.alphabet:
            nxt = product.step(state, sym)
            if nxt is None:
                continue
            row[sym] = nxt
            if nxt not in parent:
                parent[nxt] = (state, sym)
                queue.append(nxt)
        adj[state] = row
    return parent, adj


def find_infinite_evidence(product) -> Optional[CycleEvidence]:
    """Return cycle evidence if the intersection language is infinite."""
    parent, adj = _reachable(product)

    # Reverse BFS from accepting states: which states can reach acceptance,
    # and a pointer (symbol, next) walking towards an accepting state.
    to_accept = {}
    queue = deque()
    for state, row in adj.items():
        if product.is_accept(state):
            to_accept[state] = None
            queue.append(state)
    while queue:
        state = queue.popleft()
        for prev, row in adj.items():
            for sym, nxt in row.items():
                if nxt == state and prev not in to_accept:
                    to_accept[prev] = (sym, state)
                    queue.append(prev)

    useful = set(to_accept)
    if product.initial() not in useful:
        return None  # empty language: certainly finite

    # DFS over the useful subgraph looking for a back edge (a cycle whose
    # every state can reach acceptance).
    WHITE, GRAY, BLACK = 0, 1, 2
    color = {s: WHITE for s in useful}
    cycle = None  # (state_at_cycle_entry, [symbols around the loop])

    def dfs(start):
        nonlocal cycle
        color[start] = GRAY
        stack = [(start, iter(sorted(adj[start].items())))]
        path = [start]          # states on the current DFS path
        edge_syms = []          # symbol taken to enter path[i+1]
        while stack and cycle is None:
            state, it = stack[-1]
            advanced = False
            for sym, nxt in it:
                if nxt not in useful:
                    continue
                if color[nxt] == GRAY:
                    # cycle: nxt -> ... -> state --sym--> nxt
                    i = path.index(nxt)
                    cycle = (nxt, edge_syms[i:] + [sym])
                    advanced = True
                    break
                if color[nxt] == WHITE:
                    color[nxt] = GRAY
                    stack.append((nxt, iter(sorted(adj[nxt].items()))))
                    path.append(nxt)
                    edge_syms.append(sym)
                    advanced = True
                    break
            if not advanced:
                color[state] = BLACK
                stack.pop()
                path.pop()
                if edge_syms:
                    edge_syms.pop()

    dfs(product.initial())
    if cycle is None:
        return None

    cycle_state, loop_syms = cycle

    # prefix: initial -> cycle_state (via BFS parents)
    rev = []
    state = cycle_state
    while parent[state] is not None:
        prev, sym = parent[state]
        rev.append(sym)
        state = prev
    prefix = "".join(reversed(rev))

    # suffix: cycle_state -> an accepting state (via to_accept pointers)
    suffix_syms = []
    state = cycle_state
    while to_accept[state] is not None:
        sym, state = to_accept[state]
        suffix_syms.append(sym)
    suffix = "".join(suffix_syms)

    return CycleEvidence(prefix, "".join(loop_syms), suffix)


def verify_evidence(product, evidence: CycleEvidence, repeats=range(0, 4)):
    """Independently check evidence using only the automaton interface."""
    if not evidence.loop:
        return False
    state = product.initial()
    for sym in evidence.prefix:
        state = product.step(state, sym)
        if state is None:
            return False
    cycle_state = state
    for sym in evidence.loop:  # the loop must return to the cycle state
        state = product.step(state, sym)
        if state is None:
            return False
    if state != cycle_state:
        return False
    for i in repeats:
        if not product.accepts(evidence.witness(i)):
            return False
    return True
