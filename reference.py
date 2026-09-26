"""Slow whole-input reference lexer.

Naive O(n^2) longest-match rescans from scratch at every position; used as
the ground truth that the streaming lexer is compared against under
arbitrary chunkings.  Shares the exact same selection semantics:
longest match, then explicit rule priority, then rule list order.
"""

from __future__ import annotations

from lexer import StateOp, Token


class ReferenceError(Exception):
    def __init__(self, offset: int, char: str):
        super().__init__(f"no rule matches at offset {offset}: {char!r}")
        self.offset = offset
        self.char = char


def reference_lex(states: dict, start_state: str, text: str) -> list:
    pos = 0
    stack = [start_state]
    tokens = []
    while pos < len(text):
        best = None  # (end_offset, rule)
        for rule in states[stack[-1]]:
            state = rule.dfa.start
            i = pos
            while i < len(text):
                target = rule.dfa.transitions.get((state, text[i]))
                if target is None:
                    break
                state = target
                i += 1
                if state in rule.dfa.accepts:
                    if (best is None or i > best[0]
                            or (i == best[0] and rule.priority < best[1].priority)):
                        best = (i, rule)
        if best is None:
            raise ReferenceError(pos, text[pos])
        end, rule = best
        act = rule.action
        if act.token_type is not None:
            tokens.append(Token(act.token_type, text[pos:end], pos, end))
        if act.op is StateOp.PUSH:
            stack.append(act.target)
        elif act.op is StateOp.POP:
            stack.pop()
        elif act.op is StateOp.SWITCH:
            stack[-1] = act.target
        pos = end
    return tokens
