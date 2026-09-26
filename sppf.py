"""General CFG parser that builds a shared packed parse forest (SPPF).

- Handles left recursion, epsilon productions and ambiguous grammars.
- Builds a binarized SPPF (Scott-style) instead of enumerating parse trees,
  so ambiguous inputs stay polynomial in space.
- Reports acceptance plus the farthest input position reached on failure.
- The alphabet (terminals) and the transition structure (productions) are
  given explicitly; no regex or automata libraries are used.

Pure standard library, Python 3.11+.
"""

from __future__ import annotations

from dataclasses import dataclass
from itertools import product

EPSILON = "ε"


class Grammar:
    """Explicitly defined context-free grammar.

    terminals:    iterable of terminal symbols (the alphabet)
    nonterminals: iterable of nonterminal symbols
    productions:  iterable of (lhs, rhs) pairs; rhs is a tuple of symbols,
                  the empty tuple () denotes an epsilon production
    start:        the start symbol (must be a nonterminal)
    """

    def __init__(self, terminals, nonterminals, productions, start):
        self.terminals = frozenset(terminals)
        self.nonterminals = frozenset(nonterminals)
        self.start = start
        self.productions = [(lhs, tuple(rhs)) for lhs, rhs in productions]
        if start not in self.nonterminals:
            raise ValueError(f"start symbol {start!r} is not a nonterminal")
        overlap = self.terminals & self.nonterminals
        if overlap:
            raise ValueError(f"terminal/nonterminal overlap: {sorted(overlap)}")
        for lhs, rhs in self.productions:
            if lhs not in self.nonterminals:
                raise ValueError(f"production lhs {lhs!r} is not a nonterminal")
            for sym in rhs:
                if sym not in self.terminals and sym not in self.nonterminals:
                    raise ValueError(
                        f"unknown symbol {sym!r} in production {lhs} -> {rhs}"
                    )
        self._by_lhs = {}
        for idx, (lhs, _) in enumerate(self.productions):
            self._by_lhs.setdefault(lhs, []).append(idx)

    def production_indices(self, lhs):
        return self._by_lhs.get(lhs, ())


class Node:
    """SPPF node.

    A node is interned by (label, start, end) and shared across derivations.
    `families` is a set of packed nodes; each packed node is a tuple of child
    nodes representing ONE distinct derivation edge.  Identical edges are
    deduplicated, distinct edges are all preserved.
    """

    __slots__ = ("label", "start", "end", "families")

    def __init__(self, label, start, end):
        self.label = label
        self.start = start
        self.end = end
        self.families = set()

    def __repr__(self):
        return f"Node({self.label!r}, {self.start}, {self.end})"


@dataclass
class ParseResult:
    accepted: bool
    farthest: int          # farthest input position reached (error location on failure)
    expected: frozenset    # terminals that could have continued at `farthest`
    root: Node | None      # SPPF root when accepted, else None
    node_count: int        # number of interned SPPF nodes (forest size)
    tokens: tuple
    prods: list            # productions used (index 0..n-2 = grammar, last = augmented start)


def parse(grammar: Grammar, tokens) -> ParseResult:
    """Earley-style recognition with Scott-style SPPF construction."""
    tokens = tuple(tokens)
    bad = [t for t in tokens if t not in grammar.terminals]
    if bad:
        raise ValueError(f"tokens not in the alphabet: {bad!r}")

    aug = grammar.start
    while aug in grammar.nonterminals:
        aug += "'"
    prods = list(grammar.productions) + [(aug, (grammar.start,))]
    aug_idx = len(prods) - 1
    by_lhs = {}
    for idx, (lhs, _) in enumerate(prods):
        by_lhs.setdefault(lhs, []).append(idx)
    nonterminals = grammar.nonterminals | {aug}

    n = len(tokens)
    charts = [dict() for _ in range(n + 1)]       # (rule, dot, origin) -> Node | None
    worklists = [[] for _ in range(n + 1)]
    nullable_done = [dict() for _ in range(n + 1)]  # pos -> {lhs: node} for zero-width completions
    nodes = {}                                     # (label, start, end) -> Node (interning)

    def intern(label, i, j):
        key = (label, i, j)
        node = nodes.get(key)
        if node is None:
            node = nodes[key] = Node(label, i, j)
        return node

    def make_node(label, i, j, children):
        node = intern(label, i, j)
        node.families.add(tuple(children))
        return node

    def build_node(rule, dot, origin, end, left, right):
        lhs, rhs = prods[rule]
        children = (right,) if left is None else (left, right)
        label = lhs if dot == len(rhs) else (rule, dot)
        return make_node(label, origin, end, children)

    def add_item(pos, item, node):
        chart = charts[pos]
        if item in chart:
            return  # state dedup: the SPPF node is shared, no proof edge is lost
        chart[item] = node
        worklists[pos].append(item)

    def process(pos, item):
        rule, dot, origin = item
        v = charts[pos][item]
        lhs, rhs = prods[rule]
        if dot == len(rhs):
            # Complete item: lhs spans origin..pos.  Combine with every item in
            # charts[origin] waiting for lhs.  Items added to charts[origin]
            # later are only possible when origin == pos (epsilon), and those
            # are caught by the nullable_done lookup in the predictor below.
            if origin == pos:
                nullable_done[pos][lhs] = v
            for (r2, d2, o2), v2 in list(charts[origin].items()):
                _, rhs2 = prods[r2]
                if d2 < len(rhs2) and rhs2[d2] == lhs:
                    w = build_node(r2, d2 + 1, o2, pos, v2, v)
                    add_item(pos, (r2, d2 + 1, o2), w)
            return
        sym = rhs[dot]
        if sym in nonterminals:
            for p in by_lhs.get(sym, ()):
                _, prhs = prods[p]
                node = make_node(sym, pos, pos, ()) if not prhs else None
                add_item(pos, (p, 0, pos), node)
            # Epsilon: if sym already completed with zero width at pos, advance now.
            w = nullable_done[pos].get(sym)
            if w is not None:
                add_item(pos, (rule, dot + 1, origin),
                         build_node(rule, dot + 1, origin, pos, v, w))
        elif pos < n and tokens[pos] == sym:
            leaf = intern(sym, pos, pos + 1)
            add_item(pos + 1, (rule, dot + 1, origin),
                     build_node(rule, dot + 1, origin, pos + 1, v, leaf))

    add_item(0, (aug_idx, 0, 0), None)
    farthest = 0
    for pos in range(n + 1):
        if not charts[pos]:
            break
        farthest = pos
        wl = worklists[pos]
        cursor = 0
        while cursor < len(wl):
            item = wl[cursor]
            cursor += 1
            process(pos, item)

    goal = (aug_idx, 1, 0)
    accepted = goal in charts[n]
    expected = frozenset(
        prods[r][1][d]
        for (r, d, _) in charts[farthest]
        if d < len(prods[r][1]) and prods[r][1][d] in grammar.terminals
    )
    return ParseResult(
        accepted=accepted,
        farthest=farthest,
        expected=expected,
        root=charts[n][goal] if accepted else None,
        node_count=len(nodes),
        tokens=tokens,
        prods=prods,
    )


def count_trees(root: Node) -> int:
    """Count the parse trees packed into the forest (without enumerating them)."""
    memo = {}
    visiting = set()

    def rec(node):
        key = id(node)
        if key in memo:
            return memo[key]
        if key in visiting:
            raise ValueError("cyclic parse forest (grammar has a derivation cycle)")
        visiting.add(key)
        total = 0
        for family in node.families:
            ways = 1
            for child in family:
                ways *= rec(child)
            total += ways
        visiting.discard(key)
        memo[key] = total if node.families else 1
        return memo[key]

    return rec(root)


def sample_trees(root: Node, limit: int = 10):
    """Enumerate up to `limit` concrete trees (inspection aid only; the forest
    itself is built without any enumeration)."""
    def rec(node, cap):
        if not node.families:
            return [(node.label,)]
        out = []
        for family in node.families:
            pools = [rec(child, cap) for child in family]
            for combo in product(*pools):
                out.append((node.label, *combo))
                if len(out) >= cap:
                    return out
        return out

    return rec(root, limit)


def format_label(label, prods) -> str:
    if isinstance(label, tuple):
        rule, dot = label
        lhs, rhs = prods[rule]
        parts = [str(s) for s in rhs]
        parts.insert(dot, "·")
        return f"{lhs}→{' '.join(parts)}"
    return str(label)


def format_tree(tree, prods, indent=0):
    lines = ["  " * indent + format_label(tree[0], prods)]
    for child in tree[1:]:
        lines.extend(format_tree(child, prods, indent + 1))
    return lines


def _demo():
    print("=== 1) Ambiguous: S -> S S | 'a' on 'aaa' (ambiguity preserved) ===")
    g = Grammar({"a"}, {"S"}, [("S", ("S", "S")), ("S", ("a",))], "S")
    res = parse(g, "aaa")
    print(f"accepted={res.accepted} farthest={res.farthest} "
          f"forest_nodes={res.node_count} packed_trees={count_trees(res.root)}")
    for i, tree in enumerate(sample_trees(res.root, 4), 1):
        print(f"-- tree {i} --")
        print("\n".join(format_tree(tree, res.prods)))

    print()
    print("=== 2) Same grammar on 'a' * 10: exponential trees, polynomial forest ===")
    res = parse(g, "a" * 10)
    print(f"accepted={res.accepted} forest_nodes={res.node_count} "
          f"packed_trees={count_trees(res.root)}")

    print()
    print("=== 3) Left recursion fed by epsilon: S -> S 'a' | A, A -> 'b' | eps ===")
    g3 = Grammar({"a", "b"}, {"S", "A"},
                 [("S", ("S", "a")), ("S", ("A",)),
                  ("A", ("b",)), ("A", ())], "S")
    for text in ("a", "b", "ba", "aa"):
        res = parse(g3, text)
        print(f"{text!r}: accepted={res.accepted} farthest={res.farthest} "
              f"packed_trees={count_trees(res.root) if res.accepted else '-'}")

    print()
    print("=== 3b) Nullable symbol inside recursion -> cyclic forest detected ===")
    g3b = Grammar({"a", "b"}, {"S", "A"},
                  [("S", ("S", "A")), ("S", ("a",)),
                   ("A", ("b",)), ("A", ())], "S")
    res = parse(g3b, "a")
    try:
        trees = count_trees(res.root)
    except ValueError as exc:
        trees = f"infinite ({exc})"
    print(f"'a': accepted={res.accepted} packed_trees={trees}")

    print()
    print("=== 4) Empty input judged by the grammar ===")
    g_eps = Grammar({"a"}, {"S", "A"},
                    [("S", ("A",)), ("A", ("a",)), ("A", ())], "S")
    res = parse(g_eps, "")
    print(f"nullable grammar, '': accepted={res.accepted} farthest={res.farthest}")
    g_no = Grammar({"a"}, {"S"}, [("S", ("a",))], "S")
    res = parse(g_no, "")
    print(f"S->'a' only,     '': accepted={res.accepted} farthest={res.farthest}")

    print()
    print("=== 5) Counterexample: farthest failure position ===")
    g5 = Grammar({"a", "b", "c"}, {"S"}, [("S", ("a", "b", "c"))], "S")
    res = parse(g5, "aba")
    print(f"S->'abc' on 'aba': accepted={res.accepted} "
          f"farthest={res.farthest} expected={sorted(res.expected)}")


if __name__ == "__main__":
    _demo()
