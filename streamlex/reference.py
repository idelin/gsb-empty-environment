"""Slow whole-input reference lexer.

This is the deliberately naive, obviously-correct implementation used
to validate the streaming lexer: for every position it re-simulates
every active rule's DFA from scratch over every prefix.  It shares only
the match-arbitration helper (:func:`streamlex.lexer.better_match`) and
the action semantics with the streaming engine, so arbitrary-chunking
property tests compare two genuinely different scanning strategies.
"""
from __future__ import annotations

from .lexer import LexError, Token, apply_action, better_match


def lex_all(rules, *, initial_state, text):
    """Lex ``text`` in one shot; return the list of tokens.

    Raises :class:`streamlex.lexer.LexError` at the first position with
    no match and :class:`streamlex.lexer.AmbiguousRuleError` on an
    unresolved length+priority tie.
    """
    rules = list(rules)
    tokens = []
    stack = [initial_state]
    pos = 0
    while pos < len(text):
        best = None
        for rule in rules:
            if stack[-1] not in rule.states:
                continue
            state = rule.dfa.start
            for j in range(pos, len(text)):
                state = rule.dfa.step(state, text[j])
                if state is None:
                    break
                if rule.dfa.is_accept(state):
                    best = better_match(best, (j + 1, rule), pos)
        if best is None:
            raise LexError(pos, text[pos], stack[-1])
        end, rule = best
        if rule.emit:
            tokens.append(Token(rule.name, text[pos:end], pos, end, stack[-1]))
        apply_action(stack, rule.action, pos)
        pos = end
    return tokens
