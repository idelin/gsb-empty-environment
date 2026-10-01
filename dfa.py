"""DFA minimization with partition certificates, distinguishing witnesses,
and incremental partition repair.

Pure Python 3.11 standard library. Alphabet and transition structure are
given explicitly; no regex or automata libraries are used.

Conventions
-----------
- States are arbitrary hashables; symbols are single-character strings.
- Transitions may be partial: missing edges are completed to a fresh dead
  (sink) state by ``normalize``.
- ``normalize`` = remove unreachable states, then complete with a dead state.
"""

from __future__ import annotations

from collections import deque
from dataclasses import dataclass


class _DeadState:
    """Unique sink-state sentinel used when completing partial transitions."""

    def __repr__(self) -> str:  # pragma: no cover - cosmetic
        return "<dead>"


DEAD = _DeadState()


@dataclass
class DFA:
    states: frozenset
    alphabet: frozenset
    transitions: dict  # (state, symbol) -> state; may be partial
    start: object
    accepting: frozenset


# ---------------------------------------------------------------------------
# Basic structural operations
# ---------------------------------------------------------------------------

def reachable_states(dfa: DFA) -> set:
    seen = {dfa.start}
    queue = deque([dfa.start])
    while queue:
        s = queue.popleft()
        for a in dfa.alphabet:
            t = dfa.transitions.get((s, a))
            if t is not None and t not in seen:
                seen.add(t)
                queue.append(t)
    return seen


def trim(dfa: DFA) -> DFA:
    """Remove states not reachable from the start state."""
    keep = reachable_states(dfa)
    return DFA(
        frozenset(keep),
        dfa.alphabet,
        {k: v for k, v in dfa.transitions.items() if k[0] in keep},
        dfa.start,
        frozenset(dfa.accepting & keep),
    )


def complete(dfa: DFA) -> DFA:
    """Fill missing transitions with a single shared dead state."""
    transitions = dict(dfa.transitions)
    missing = False
    for s in dfa.states:
        for a in dfa.alphabet:
            if (s, a) not in transitions:
                missing = True
                transitions[(s, a)] = DEAD
    if not missing:
        return dfa
    for a in dfa.alphabet:
        transitions[(DEAD, a)] = DEAD
    return DFA(dfa.states | {DEAD}, dfa.alphabet, transitions,
               dfa.start, dfa.accepting)


def normalize(dfa: DFA) -> DFA:
    """Trim unreachable states, then complete with a dead state."""
    return complete(trim(dfa))


def relabel(dfa: DFA, renaming) -> DFA:
    """Return an isomorphic DFA with states renamed by ``renaming``."""
    return DFA(
        frozenset(renaming(s) for s in dfa.states),
        dfa.alphabet,
        {(renaming(s), a): renaming(t) for (s, a), t in dfa.transitions.items()},
        renaming(dfa.start),
        frozenset(renaming(s) for s in dfa.accepting),
    )


def run(dfa: DFA, state, word: str):
    """Follow ``word`` (a string of single-char symbols) from ``state``."""
    for ch in word:
        state = dfa.transitions[(state, ch)]
    return state


# ---------------------------------------------------------------------------
# Partition refinement
# ---------------------------------------------------------------------------

def initial_partition(dfa: DFA) -> list:
    """Accepting states can never merge with non-accepting states."""
    acc = set(dfa.accepting)
    non = set(dfa.states) - acc
    return [b for b in (acc, non) if b]


def _refine(dfa: DFA, partition: list):
    """Refine ``partition`` to stability.

    Returns (blocks, pairs_touched) where pairs_touched counts, per refinement
    round, the state pairs separated by a split (sum of |A|*|B| over the parts
    each block breaks into).
    """
    blocks = [set(b) for b in partition if b]
    pairs = 0
    while True:
        block_of = {}
        for i, b in enumerate(blocks):
            for s in b:
                block_of[s] = i
        new_blocks = []
        changed = False
        for b in blocks:
            groups = {}
            for s in b:
                sig = (
                    s in dfa.accepting,
                    tuple(block_of[dfa.transitions[(s, a)]]
                          for a in sorted(dfa.alphabet, key=repr)),
                )
                groups.setdefault(sig, set()).add(s)
            if len(groups) > 1:
                changed = True
                sizes = [len(g) for g in groups.values()]
                total = len(b)
                pairs += (total * total - sum(x * x for x in sizes)) // 2
            new_blocks.extend(groups.values())
        blocks = new_blocks
        if not changed:
            return blocks, pairs


def quotient_of(dfa: DFA, blocks: list):
    """Build the quotient DFA whose states are block indices 0..k-1."""
    block_of = {}
    for i, b in enumerate(blocks):
        for s in b:
            block_of[s] = i
    transitions = {}
    for i, b in enumerate(blocks):
        rep = next(iter(b))
        for a in dfa.alphabet:
            transitions[(i, a)] = block_of[dfa.transitions[(rep, a)]]
    accepting = frozenset(i for i, b in enumerate(blocks) if b & dfa.accepting)
    quotient = DFA(frozenset(range(len(blocks))), dfa.alphabet, transitions,
                   block_of[dfa.start], accepting)
    return quotient, block_of


# ---------------------------------------------------------------------------
# Distinguishing witnesses (independent product-space BFS)
# ---------------------------------------------------------------------------

def distinguishing_string(dfa: DFA, p, q):
    """Shortest string accepted from exactly one of p, q (BFS over the
    product of the DFA with itself). Returns None iff p and q are
    language-equivalent."""
    if (p in dfa.accepting) != (q in dfa.accepting):
        return ""
    seen = {(p, q)}
    queue = deque([((p, q), "")])
    while queue:
        (x, y), w = queue.popleft()
        for a in sorted(dfa.alphabet, key=repr):
            nx, ny = dfa.transitions[(x, a)], dfa.transitions[(y, a)]
            nw = w + str(a)
            if (nx in dfa.accepting) != (ny in dfa.accepting):
                return nw
            if (nx, ny) not in seen:
                seen.add((nx, ny))
                queue.append(((nx, ny), nw))
    return None


# ---------------------------------------------------------------------------
# Canonical naming (input relabeling invariance)
# ---------------------------------------------------------------------------

def canonicalize(dfa: DFA):
    """Rename states q0, q1, ... in BFS order from the start state over the
    sorted alphabet. Isomorphic DFAs get identical canonical forms."""
    names = {}

    def name(s):
        if s not in names:
            names[s] = f"q{len(names)}"
        return names[s]

    name(dfa.start)
    queue = deque([dfa.start])
    while queue:
        s = queue.popleft()
        for a in sorted(dfa.alphabet, key=repr):
            t = dfa.transitions[(s, a)]
            if t not in names:
                name(t)
                queue.append(t)
    transitions = {(names[s], a): names[dfa.transitions[(s, a)]]
                   for s in names for a in dfa.alphabet}
    canon = DFA(
        frozenset(names.values()),
        dfa.alphabet,
        transitions,
        "q0",
        frozenset(names[s] for s in names if s in dfa.accepting),
    )
    return canon, names


def canonical_key(dfa: DFA):
    """Hashable canonical fingerprint; invariant under input relabeling."""
    canon, _ = canonicalize(normalize(dfa))
    return (
        canon.start,
        tuple(sorted(canon.accepting)),
        tuple(sorted((s, repr(a), t)
                     for (s, a), t in canon.transitions.items())),
    )


# ---------------------------------------------------------------------------
# Minimization with certificate
# ---------------------------------------------------------------------------

@dataclass
class MinimizationResult:
    normalized: DFA          # trimmed + completed input
    blocks: list             # list of frozenset of original states
    quotient: DFA            # minimal DFA with canonical state names
    mapping: dict            # original state -> canonical quotient state name
    certificate: dict        # (name_i, name_j) -> shortest distinguishing string
    pairs_touched: int       # state pairs separated during refinement


def minimize(dfa: DFA) -> MinimizationResult:
    ndfa = normalize(dfa)
    blocks, pairs = _refine(ndfa, initial_partition(ndfa))
    quotient, block_of = quotient_of(ndfa, blocks)
    canon, names = canonicalize(quotient)
    mapping = {s: names[block_of[s]] for s in ndfa.states}
    qnames = sorted(canon.states)
    certificate = {}
    for i, x in enumerate(qnames):
        for y in qnames[i + 1:]:
            certificate[(x, y)] = distinguishing_string(canon, x, y)
    return MinimizationResult(ndfa, [frozenset(b) for b in blocks], canon,
                              mapping, certificate, pairs)


# ---------------------------------------------------------------------------
# Incremental partition repair (split and merge)
# ---------------------------------------------------------------------------

@dataclass
class RepairResult:
    partition: list      # repaired list of frozenset blocks
    splits: int          # net blocks gained in the split phase
    merges: int          # blocks merged away in the merge phase
    pairs_touched: int   # state pairs examined across both phases


def repair_partition(dfa: DFA, partition: list) -> RepairResult:
    """Repair ``partition`` after local transition modifications.

    Split phase: refine the old partition to stability under the new
    transitions (handles states that stopped being equivalent).
    Merge phase: minimize the quotient of the refined partition. Any stable
    partition refines the coarsest stable one, so this recovers exactly the
    blocks that have become equivalent again (handles merges) at a cost
    proportional to the quotient size, not the original automaton.
    """
    ndfa = normalize(dfa)
    known = set().union(*partition) if partition else set()
    blocks = [set(b) & ndfa.states for b in partition]
    blocks = [b for b in blocks if b]
    for s in ndfa.states - known:  # states new to the partition (e.g. DEAD)
        blocks.append({s})

    refined, split_pairs = _refine(ndfa, blocks)
    splits = len(refined) - len(blocks)

    quotient, _ = quotient_of(ndfa, refined)
    qblocks, merge_pairs = _refine(quotient, initial_partition(quotient))
    merged = []
    for qb in qblocks:
        union = set()
        for i in qb:
            union |= refined[i]
        merged.append(union)
    merges = len(refined) - len(merged)

    return RepairResult([frozenset(b) for b in merged], splits, merges,
                        split_pairs + merge_pairs)


# ---------------------------------------------------------------------------
# Independent verification by product-space search
# ---------------------------------------------------------------------------

def verify_partition(dfa: DFA, partition: list):
    """Independently verify a partition by product-space BFS on every pair
    of states. Returns (ok, pairs_checked, counterexample_or_None)."""
    ndfa = normalize(dfa)
    block_of = {}
    for i, b in enumerate(partition):
        for s in b:
            block_of[s] = i
    states = sorted(ndfa.states, key=repr)
    checked = 0
    for i, s in enumerate(states):
        for t in states[i + 1:]:
            checked += 1
            witness = distinguishing_string(ndfa, s, t)
            equivalent = witness is None
            if equivalent != (block_of.get(s) == block_of.get(t)):
                return False, checked, (s, t, witness)
    return True, checked, None
