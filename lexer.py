"""Streaming longest-match lexer with explicit DFAs.

Design notes
------------
- Rules are explicit DFAs: alphabet, transition table, start and accepting
  states are plain data.  No regex or automata library is used anywhere.
- Token selection is longest match first, then explicit rule priority
  (lower number wins), then rule list order as the final deterministic
  tie-break.  Several rules may accept the same lexeme and carry different
  actions (including different state transitions); the priority order picks
  exactly one action.
- Lexical states: rules are grouped into named states.  Actions may KEEP,
  PUSH, POP or SWITCH the state stack.
- Streaming: input arrives via feed().  A token is only committed when the
  longest match is *determined*, i.e. every live DFA died past the accepting
  position, or finish() was called.  A still-extendable match at the end of
  the buffer is never delivered early, so chunk boundaries cannot produce
  premature short tokens.
- Bounded buffering: while a match is undetermined the uncommitted window
  (len(buffer) - pos) may grow up to ``max_pending`` characters.  If it
  grows beyond that, feed() reports Status.INCOMPLETE together with the
  buffered size and the work spent, instead of silently cutting a shorter
  token.  Work per feed() call is bounded by
  (buffered + len(chunk)) * (#rules of the current state) transition lookups.
- Errors: if no rule can match at the current offset, a LexError with the
  precise absolute stream offset is reported; recover() skips one char and
  lexing can continue.
- checkpoint()/rollback() save and restore the full lexer position
  (scan offset, state stack, token log, pull cursor).  undo_input(n)
  retracts the last n fed characters (coarse mechanism; it does not restore
  the lexical-state history, use checkpoints for that).
"""

from __future__ import annotations

from collections import deque
from dataclasses import dataclass
from enum import Enum, auto
from typing import Hashable


class Status(Enum):
    OK = auto()          # chunk consumed; nothing undetermined over the bound
    INCOMPLETE = auto()  # undetermined window exceeded max_pending; no token cut
    ERROR = auto()       # no rule matches at the reported offset
    FINISHED = auto()    # finish() called and all input tokenized


class StateOp(Enum):
    KEEP = auto()
    PUSH = auto()
    POP = auto()
    SWITCH = auto()


@dataclass(frozen=True)
class DFA:
    alphabet: frozenset
    start: Hashable
    transitions: dict      # (state, char) -> state, fully explicit
    accepts: frozenset     # accepting states


@dataclass(frozen=True)
class Action:
    token_type: "str | None"       # None means "skip" (no token emitted)
    op: StateOp = StateOp.KEEP
    target: "str | None" = None    # state name for PUSH / SWITCH


@dataclass(frozen=True)
class Rule:
    name: str
    dfa: DFA
    priority: int                 # lower wins on equal match length
    action: Action


@dataclass(frozen=True)
class Token:
    type: str
    lexeme: str
    start: int
    end: int


@dataclass(frozen=True)
class LexError:
    offset: int                   # absolute stream offset of the failure
    char: str


@dataclass(frozen=True)
class FeedResult:
    status: Status
    buffered: int                 # uncommitted chars currently held back
    work: int                     # transition lookups spent by this call
    error: "LexError | None" = None


@dataclass(frozen=True)
class Checkpoint:
    pos: int
    stack: tuple
    log_len: int
    pulled: int
    finished: bool


def literal_dfa(word: str, alphabet) -> DFA:
    """DFA accepting exactly the literal string ``word``."""
    transitions = {}
    for i, ch in enumerate(word):
        transitions[(i, ch)] = i + 1
    return DFA(frozenset(alphabet), 0, transitions, frozenset({len(word)}))


def charclass_dfa(chars, alphabet, min_count: int = 1, repeat: bool = True) -> DFA:
    """DFA accepting ``[chars]{min_count,}`` (exactly ``min_count`` chars
    when ``repeat`` is False)."""
    transitions = {}
    for c in chars:
        for k in range(min_count):
            transitions[(k, c)] = k + 1
        if repeat:
            transitions[(min_count, c)] = min_count
    return DFA(frozenset(alphabet), 0, transitions, frozenset({min_count}))


class Lexer:
    def __init__(self, states: dict, start_state: str, max_pending: int = 4096):
        if start_state not in states:
            raise ValueError(f"unknown start state {start_state!r}")
        for name, rules in states.items():
            if not rules:
                raise ValueError(f"state {name!r} has no rules")
        self.states = states
        self.max_pending = max_pending
        self._extendable = {}
        for rules in states.values():
            for rule in rules:
                if id(rule) not in self._extendable:
                    self._extendable[id(rule)] = frozenset(
                        s for (s, _c) in rule.dfa.transitions)
        self.buffer = ""
        self.pos = 0                       # start of the uncommitted window
        self.state_stack = [start_state]
        self.finished = False
        self.error = None
        self._log = []                     # every committed token, in order
        self._pulled = 0                   # log entries already handed out
        self._out = deque()                # committed, not yet pulled

    # ------------------------------------------------------------- input

    def feed(self, chunk: str) -> FeedResult:
        if self.finished:
            raise RuntimeError("feed() after finish()")
        if self.error is not None:
            raise RuntimeError("unrecovered lex error; call recover() first")
        self.buffer += chunk
        return self._pump(final=False)

    def finish(self) -> FeedResult:
        if self.error is not None:
            raise RuntimeError("unrecovered lex error; call recover() first")
        self.finished = True
        return self._pump(final=True)

    def recover(self) -> FeedResult:
        """Skip the offending character and resume lexing."""
        if self.error is None:
            raise RuntimeError("no error to recover from")
        self.pos += 1
        self.error = None
        return self._pump(final=self.finished)

    # ------------------------------------------------------------- output

    def next_token(self):
        if not self._out:
            return None
        self._pulled += 1
        return self._out.popleft()

    def drain(self) -> list:
        out = []
        while True:
            tok = self.next_token()
            if tok is None:
                return out
            out.append(tok)

    # ------------------------------------------------- checkpoint / undo

    def checkpoint(self) -> Checkpoint:
        return Checkpoint(self.pos, tuple(self.state_stack),
                          len(self._log), self._pulled, self.finished)

    def rollback(self, cp: Checkpoint) -> FeedResult:
        self.pos = cp.pos
        self.state_stack = list(cp.stack)
        del self._log[cp.log_len:]
        self._pulled = min(cp.pulled, len(self._log))
        self._out = deque(self._log[self._pulled:])
        self.finished = cp.finished
        self.error = None
        return self._pump(final=self.finished)

    def undo_input(self, n: int) -> FeedResult:
        """Retract the last ``n`` fed characters so they can be re-fed.

        Tokens overlapping the retracted tail are un-committed and will be
        re-produced from the corrected input.  This rewinds the scan offset
        but not the lexical-state history; use checkpoints for exact state
        restoration.
        """
        if n < 0 or n > len(self.buffer):
            raise ValueError(
                f"cannot retract {n} chars from a buffer of {len(self.buffer)}")
        new_len = len(self.buffer) - n
        keep = 0
        for tok in self._log:
            if tok.end <= new_len:
                keep += 1
            else:
                break
        first_dropped_start = self._log[keep].start if keep < len(self._log) else new_len
        del self._log[keep:]
        self._pulled = min(self._pulled, keep)
        self._out = deque(self._log[self._pulled:])
        self.pos = min(self.pos, new_len, first_dropped_start)
        self.buffer = self.buffer[:new_len]
        self.finished = False
        self.error = None
        return self._pump(final=False)

    # ------------------------------------------------------------- engine

    def _commit(self, rule: Rule, start: int, end: int) -> None:
        act = rule.action
        if act.token_type is not None:
            tok = Token(act.token_type, self.buffer[start:end], start, end)
            self._log.append(tok)
            self._out.append(tok)
        if act.op is StateOp.PUSH:
            if act.target not in self.states:
                raise RuntimeError(f"push of unknown state {act.target!r}")
            self.state_stack.append(act.target)
        elif act.op is StateOp.POP:
            if len(self.state_stack) == 1:
                raise RuntimeError("lexical state stack underflow")
            self.state_stack.pop()
        elif act.op is StateOp.SWITCH:
            if act.target not in self.states:
                raise RuntimeError(f"switch to unknown state {act.target!r}")
            self.state_stack[-1] = act.target

    def _pump(self, final: bool) -> FeedResult:
        work = 0
        status = None
        while self.pos < len(self.buffer) and self.error is None:
            rules = self.states[self.state_stack[-1]]
            live = [(rule, rule.dfa.start) for rule in rules
                    if rule.dfa.start in self._extendable[id(rule)]]
            best = None                    # (end_offset, rule)
            i = self.pos
            while i < len(self.buffer) and live:
                ch = self.buffer[i]
                nxt = []
                for rule, state in live:
                    target = rule.dfa.transitions.get((state, ch))
                    work += 1
                    if target is not None:
                        nxt.append((rule, target))
                i += 1
                live = []
                for rule, state in nxt:
                    if state in rule.dfa.accepts:
                        if (best is None or i > best[0]
                                or (i == best[0] and rule.priority < best[1].priority)):
                            best = (i, rule)
                    # States without outgoing transitions can never extend
                    # the match; dropping them keeps "determined" reachable.
                    if state in self._extendable[id(rule)]:
                        live.append((rule, state))
            determined = not live or final
            if best is not None and determined:
                end, rule = best
                self._commit(rule, self.pos, end)
                self.pos = end
                continue
            if best is None and determined:
                self.error = LexError(self.pos, self.buffer[self.pos])
                status = Status.ERROR
                break
            # Undetermined: some DFA is still alive at the end of the buffer,
            # so the match may extend with future input.  Never cut early.
            window = len(self.buffer) - self.pos
            status = Status.INCOMPLETE if window > self.max_pending else Status.OK
            break
        if status is None:
            if self.error is not None:
                status = Status.ERROR
            elif final and self.pos == len(self.buffer):
                status = Status.FINISHED
            else:
                status = Status.OK
        return FeedResult(status, len(self.buffer) - self.pos, work, self.error)
