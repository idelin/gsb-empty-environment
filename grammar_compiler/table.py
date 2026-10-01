"""Predictive (LL(1)) table construction and rich conflict reporting.

For every conflicting cell the report contains the shortest reachable terminal
prefix (a real leftmost-derivation witness), the competing productions and the
reason each of them predicts the same terminal.
"""
from __future__ import annotations

from collections import defaultdict
from dataclasses import dataclass
from heapq import heappop, heappush

from .symbols import Sym


def first_of_sequence(seq, nullable, first):
    out = set()
    for sym in seq:
        if sym.is_terminal:
            out.add(sym)
            return out
        out |= first.get(sym, set())
        if sym not in nullable:
            return out
    return out


def sequence_nullable(seq, nullable):
    return all(sym.is_nonterminal and sym in nullable for sym in seq)


def min_terminal_strings(productions):
    """Shortest terminal string derivable from each nonterminal."""
    best = {}
    changed = True
    while changed:
        changed = False
        for prod in productions:
            cand = []
            ok = True
            for sym in prod.rhs:
                if sym.is_terminal:
                    cand.append(sym)
                elif sym in best:
                    cand.extend(best[sym])
                else:
                    ok = False
                    break
            if not ok:
                continue
            cand = tuple(cand)
            cur = best.get(prod.lhs)
            if cur is None or (len(cand), cand) < (len(cur), cur):
                best[prod.lhs] = cand
                changed = True
    return best


def shortest_prefixes(productions, start, minstr):
    """Shortest terminal prefix w such that start derives w A ... (leftmost).

    Dijkstra over nonterminals: expanding A -> rhs makes each nonterminal in
    rhs reachable after the terminal string produced by the symbols before it.
    """
    by_lhs = defaultdict(list)
    for prod in productions:
        by_lhs[prod.lhs].append(prod)
    dist = {}
    if start is not None:
        dist[start] = ()
    heap = [(0, (), start)] if start is not None else []
    while heap:
        _, prefix, lhs = heappop(heap)
        if dist.get(lhs) != prefix:
            continue
        for prod in by_lhs.get(lhs, ()):
            consumed = []
            for sym in prod.rhs:
                if sym.is_nonterminal:
                    cand = prefix + tuple(consumed)
                    cur = dist.get(sym)
                    if cur is None or (len(cand), cand) < (len(cur), cur):
                        dist[sym] = cand
                        heappush(heap, (len(cand), cand, sym))
                    tail = minstr.get(sym)
                    if tail is None:
                        break
                    consumed.extend(tail)
                else:
                    consumed.append(sym)
    return dist


@dataclass(frozen=True)
class ConflictReport:
    nonterminal: Sym
    terminal: Sym
    productions: tuple          # competing Production objects
    reasons: tuple              # human-readable reason per production
    prefix: tuple               # shortest reachable terminal prefix
    witness: tuple              # prefix + (conflicting terminal,)

    def __str__(self):
        lines = [
            f"CONFLICT at {self.nonterminal} on lookahead {self.terminal}",
            "  shortest reachable prefix: "
            + (" ".join(t.name for t in self.prefix) or "(empty)"),
            "  witness input: "
            + " ".join(t.name for t in self.witness),
        ]
        for prod, reason in zip(self.productions, self.reasons):
            lines.append(f"  competing {prod}   [{reason}]")
        return "\n".join(lines)


def build_table(nullable, first, follow, productions, start):
    """Return (table, conflicts).

    table maps (nonterminal, terminal) -> tuple of production ids.
    """
    table = defaultdict(list)
    for prod in productions:
        predict = first_of_sequence(prod.rhs, nullable, first)
        if sequence_nullable(prod.rhs, nullable):
            predict = predict | follow.get(prod.lhs, set())
        for tok in predict:
            table[(prod.lhs, tok)].append(prod.pid)

    by_pid = {p.pid: p for p in productions}
    conflicts = []
    prefixes = None
    for key in sorted(table):
        pids = table[key]
        if len(pids) < 2:
            continue
        lhs, tok = key
        if prefixes is None:
            minstr = min_terminal_strings(productions)
            prefixes = shortest_prefixes(productions, start, minstr)
        prefix = prefixes.get(lhs, ())
        prods = tuple(by_pid[pid] for pid in pids)
        reasons = []
        for prod in prods:
            why = []
            if tok in first_of_sequence(prod.rhs, nullable, first):
                why.append("predicted via FIRST of rhs")
            if sequence_nullable(prod.rhs, nullable) and tok in follow.get(lhs, ()):
                why.append("predicted via FOLLOW of lhs (nullable rhs)")
            reasons.append(" and ".join(why))
        conflicts.append(ConflictReport(
            nonterminal=lhs, terminal=tok, productions=prods,
            reasons=tuple(reasons), prefix=prefix, witness=prefix + (tok,)))
    final = {key: tuple(pids) for key, pids in table.items()}
    return final, tuple(conflicts)
