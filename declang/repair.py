"""Minimal edit-cost repair search over token streams.

Edits (uniform cost 1):
  * insert a virtual token of any kind at any position
  * delete an original token
  * replace an original token's kind (a virtual token carrying `replaces`)

Search is BFS by edit count, so the first layer that yields a fully-parseable
declaration proves the minimum cost. Every fully-parseable state in that layer
is an equal-cost candidate (returned all of them, unless a cap truncates).
This is deliberately *not* a greedy sync: a result here is a true minimum
within the budget; an exhausted budget reports failure instead.
"""

from __future__ import annotations

from dataclasses import dataclass, field
from typing import List, Optional, Sequence, Tuple

from .lexer import ALL_KINDS, Token, make_virtual


def _parses_full(tokens: Sequence[Token]) -> bool:
    # Lazy import: parser imports this module at load time.
    from .parser import ParseError, parse_decl
    try:
        _, consumed = parse_decl(tokens, 0)
    except ParseError:
        return False
    return consumed == len(tokens)


def _describe_op(op: tuple) -> str:
    tag = op[0]
    if tag == "insert":
        _, pos, kind = op
        return f"insert {kind!r} at byte {pos}"
    if tag == "delete":
        _, tok = op
        return f"delete {tok.text!r} [{tok.start}:{tok.end}]"
    _, tok, kind = op
    return f"replace {tok.text!r} with {kind!r} [{tok.start}:{tok.end}]"


@dataclass(frozen=True)
class Candidate:
    tokens: Tuple[Token, ...]
    ops: Tuple[tuple, ...]

    def describe(self) -> str:
        if not self.ops:
            return "no edits"
        return "; ".join(_describe_op(op) for op in self.ops)


@dataclass
class RepairSearch:
    found: bool
    cost: Optional[int]
    candidates: List[Candidate] = field(default_factory=list)
    truncated: bool = False   # candidate list cut, or search state cap hit


def _neighbors(state: Tuple[Token, ...], ops: Tuple[tuple, ...]):
    """All one-edit neighbors of a token stream (deterministic order)."""
    n = len(state)
    for pos in range(n + 1):
        if pos < n:
            tok = state[pos]
            yield (
                state[:pos] + state[pos + 1:],
                ops + (("delete", tok),),
            )
            for kind in ALL_KINDS:
                if kind == tok.kind:
                    continue
                virtual = make_virtual(kind, 0, replaces=tok)
                yield (
                    state[:pos] + (virtual,) + state[pos + 1:],
                    ops + (("replace", tok, kind),),
                )

        byte = state[pos - 1].end if pos > 0 else (state[0].start if state else 0)
        for kind in ALL_KINDS:
            virtual = make_virtual(kind, byte)
            yield (
                state[:pos] + (virtual,) + state[pos:],
                ops + (("insert", byte, kind),),
            )


def find_minimal_repairs(
    tokens: Sequence[Token],
    max_cost: int = 2,
    max_candidates: int = 8,
    max_states: int = 60000,
) -> RepairSearch:
    """BFS by edit count. Returns all equal-cost candidates, or an explicit
    failure (never a falsely "minimal" greedy result)."""
    start = tuple(tokens)
    if _parses_full(start):
        return RepairSearch(True, 0, [Candidate(start, ())], False)

    visited = {tuple(t.kind for t in start)}
    frontier: List[Tuple[Tuple[Token, ...], Tuple[tuple, ...]]] = [(start, ())]
    truncated = False

    for cost in range(1, max_cost + 1):
        next_frontier = []
        candidates: List[Candidate] = []
        stop_layer = False

        for state, ops in frontier:
            if stop_layer:
                break
            for neighbor, new_ops in _neighbors(state, ops):
                key = tuple(t.kind for t in neighbor)
                if key in visited:
                    continue
                visited.add(key)

                if len(visited) > max_states:
                    truncated = True
                    stop_layer = True
                    break

                if _parses_full(neighbor):
                    candidates.append(Candidate(neighbor, new_ops))
                    if len(candidates) >= max_candidates:
                        # Keep scanning this layer only to learn whether more
                        # exist, capped at max_candidates + 1.
                        pass
                    if len(candidates) > max_candidates:
                        truncated = True
                        stop_layer = True
                        break
                else:
                    next_frontier.append((neighbor, new_ops))

        if candidates:
            return RepairSearch(True, cost, candidates[:max_candidates], truncated)

        if truncated or not next_frontier:
            return RepairSearch(False, None, [], truncated)

        frontier = next_frontier

    return RepairSearch(False, None, [], False)
