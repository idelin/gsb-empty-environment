"""Streaming maximal-munch lexer over explicit DFAs.

Semantics
---------
* Several rules may be active in the current lexical state.  All of
  their DFAs are advanced in parallel from the token start.
* A token is chosen by *longest match*; ties on length are broken by
  *rule priority* (lower integer wins).  A length-and-priority tie
  between two different rules raises :class:`AmbiguousRuleError`.
* A token is *delivered* only once the longest match is determined,
  i.e. when every candidate DFA is dead (no productive continuation)
  or when :meth:`Lexer.finish` signals end of input.  Until then the
  scanner waits, so a short token is never emitted prematurely across
  a chunk boundary.
* Buffering bound: the buffer holds exactly the unconsumed tail of the
  input, which while scanning is the text since the current token
  start.  If it grows beyond ``max_buffer`` the lexer reports
  :data:`Status.INCOMPLETE` instead of cutting a token early.
* Work bound: each input character is stepped through each active
  rule's DFA at most twice (once by the scan that overshoots the token
  end, once by the re-scan of the overshoot), so total work is
  O(input length x number of active rules).
"""
from __future__ import annotations

from dataclasses import dataclass
from enum import Enum

from .rules import Action, Rule


class Status(Enum):
    """Outcome of a feed/finish call."""

    OK = "ok"
    INCOMPLETE = "incomplete"  # buffer limit hit; token stream is undecided


@dataclass(frozen=True)
class Token:
    type: str
    lexeme: str
    start: int  # absolute offset of the first character
    end: int  # absolute offset one past the last character
    state: str  # lexical state in which the token was matched


class LexError(Exception):
    """No active rule matches at ``offset``.

    ``tokens`` carries the tokens that were already produced by the
    call that raised, so they are not lost.  The lexer stays positioned
    at the offending character; call :meth:`Lexer.skip` to drop it and
    continue (recovery).
    """

    def __init__(self, offset, char, state):
        self.offset = offset
        self.char = char
        self.state = state
        self.tokens: list[Token] = []
        super().__init__(
            f"no rule matches at offset {offset} (state {state!r}, char {char!r})"
        )


class AmbiguousRuleError(Exception):
    """Two different rules accept the same longest lexeme with equal priority."""

    def __init__(self, offset, names):
        self.offset = offset
        self.names = tuple(sorted(names))
        super().__init__(
            f"ambiguous match at offset {offset}: rules {self.names!r} "
            "share lexeme length and priority; make priorities explicit"
        )


class StateStackError(Exception):
    """A ``pop`` action was taken on the initial lexical state."""


@dataclass(frozen=True)
class FeedResult:
    tokens: tuple[Token, ...]
    status: Status
    buffered: int  # number of unconsumed characters still held


@dataclass(frozen=True)
class Checkpoint:
    """Opaque snapshot of the lexer; restore with :meth:`Lexer.restore`."""

    base: int
    buffer: str
    stack: tuple[str, ...]
    finished: bool


def better_match(best, cand, start):
    """Choose between two ``(end, rule)`` candidates from ``start``.

    Longer wins; equal length is decided by priority; equal length and
    priority between different rules is a configuration error.
    """
    if best is None:
        return cand
    end, rule = cand
    bend, brule = best
    clen, blen = end - start, bend - start
    if clen != blen:
        return cand if clen > blen else best
    if rule.priority != brule.priority:
        return cand if rule.priority < brule.priority else best
    if rule.name != brule.name:
        raise AmbiguousRuleError(start, (brule.name, rule.name))
    return best


def apply_action(stack, action: Action, offset):
    """Apply a lexical-state action to ``stack`` (a mutable list)."""
    if action.kind == "none":
        return
    if action.kind == "push":
        stack.append(action.state)
    elif action.kind == "switch":
        stack[-1] = action.state
    elif action.kind == "pop":
        if len(stack) == 1:
            raise StateStackError(
                f"cannot pop the initial lexical state (offset {offset})"
            )
        stack.pop()
    else:  # pragma: no cover - Action validates its kind
        raise AssertionError(action.kind)


class _Scan:
    """Incremental state of one in-progress token scan."""

    __slots__ = ("token_start", "next_abs", "states", "best")

    def __init__(self, token_start, rules):
        self.token_start = token_start
        self.next_abs = token_start
        self.states = {rule: rule.dfa.start for rule in rules}
        self.best = None  # (end_offset, rule) of the best accept so far


class Lexer:
    """Streaming lexer.

    Parameters
    ----------
    rules:
        Iterable of :class:`~streamlex.rules.Rule`.
    alphabet:
        Explicit input alphabet (iterable of single characters).  Every
        rule DFA's alphabet must be a subset of it.
    initial_state:
        Name of the initial lexical state.
    max_buffer:
        Maximum number of unconsumed characters held while a token is
        undecided.  Exceeding it makes :meth:`feed` return
        ``Status.INCOMPLETE`` (the buffer is kept intact; no token is
        cut early).  Enlarge ``max_buffer`` and keep feeding, or call
        :meth:`finish` to force a decision at end of input.
    """

    def __init__(self, rules, *, alphabet, initial_state, max_buffer=1 << 16):
        alphabet = frozenset(alphabet)
        if not alphabet:
            raise ValueError("alphabet must be a non-empty set of characters")
        for ch in alphabet:
            if not isinstance(ch, str) or len(ch) != 1:
                raise ValueError(f"alphabet symbols must be single characters, got {ch!r}")
        rules = list(rules)
        if not rules:
            raise ValueError("at least one rule is required")
        names = [r.name for r in rules]
        if len(set(names)) != len(names):
            raise ValueError("duplicate rule names")
        for rule in rules:
            extra = rule.dfa.alphabet - alphabet
            if extra:
                raise ValueError(
                    f"rule {rule.name!r} uses symbols outside the lexer "
                    f"alphabet: {sorted(extra)!r}"
                )
        if max_buffer < 1:
            raise ValueError("max_buffer must be >= 1")
        self.alphabet = alphabet
        self.rules = rules
        self.initial_state = initial_state
        self.max_buffer = max_buffer
        self._by_state: dict[str, list[Rule]] = {}
        for rule in rules:
            for state in rule.states:
                self._by_state.setdefault(state, []).append(rule)
        self._buf = ""
        self._base = 0  # absolute offset of self._buf[0]
        self._stack = [initial_state]
        self._finished = False
        self._scan: _Scan | None = None

    # ------------------------------------------------------------------ #
    # introspection
    @property
    def position(self):
        """Absolute offset of the first unconsumed character."""
        return self._base

    @property
    def state(self):
        return self._stack[-1]

    @property
    def state_stack(self):
        return tuple(self._stack)

    @property
    def buffered(self):
        return len(self._buf)

    @property
    def finished(self):
        return self._finished

    # ------------------------------------------------------------------ #
    # input
    def feed(self, chunk: str = "") -> FeedResult:
        """Feed a chunk of input and return any tokens now decidable."""
        if self._finished:
            raise RuntimeError("feed() after finish()")
        self._buf += chunk
        return self._run(final=False)

    def finish(self) -> FeedResult:
        """Signal end of input; resolves the pending tail or raises LexError."""
        self._finished = True
        return self._run(final=True)

    def resume(self) -> FeedResult:
        """Continue scanning after recovery (see :meth:`skip`)."""
        return self._run(final=self._finished)

    # ------------------------------------------------------------------ #
    # recovery / undo
    def skip(self, n: int = 1) -> None:
        """Drop ``n`` unconsumed characters (error recovery)."""
        if not 1 <= n <= len(self._buf):
            raise ValueError(f"cannot skip {n} chars with {len(self._buf)} buffered")
        self._buf = self._buf[n:]
        self._base += n
        self._scan = None

    def pushback(self, text: str) -> None:
        """Push ``text`` back in front of the unconsumed input."""
        self._buf = text + self._buf
        self._base -= len(text)
        self._scan = None

    def retract(self, n: int) -> str:
        """Revoke up to ``n`` not-yet-consumed tail characters.

        Returns the revoked text.  Only buffered (undelivered) input can
        be revoked; characters already turned into tokens are gone.
        """
        if n < 0:
            raise ValueError("n must be >= 0")
        k = min(n, len(self._buf))
        removed = self._buf[len(self._buf) - k:]
        self._buf = self._buf[: len(self._buf) - k]
        if self._scan is not None and self._scan.next_abs - self._base > len(self._buf):
            self._scan = None
        return removed

    def checkpoint(self) -> Checkpoint:
        """Snapshot position, buffer and state stack."""
        return Checkpoint(self._base, self._buf, tuple(self._stack), self._finished)

    def restore(self, cp: Checkpoint) -> None:
        """Restore a snapshot taken with :meth:`checkpoint`."""
        self._base = cp.base
        self._buf = cp.buffer
        self._stack = list(cp.stack)
        self._finished = cp.finished
        self._scan = None

    # ------------------------------------------------------------------ #
    # scanning engine
    def _run(self, final: bool) -> FeedResult:
        out: list[Token] = []
        status = Status.OK
        while True:
            if self._scan is None:
                if not self._buf:
                    break
                self._scan = _Scan(self._base, self._by_state.get(self._stack[-1], ()))
            scan = self._scan
            determined = False
            while scan.next_abs - self._base < len(self._buf):
                ch = self._buf[scan.next_abs - self._base]
                scan.next_abs += 1
                nxt = {}
                for rule, state in scan.states.items():
                    ns = rule.dfa.step(state, ch)
                    if ns is not None and rule.dfa.is_productive(ns):
                        nxt[rule] = ns
                scan.states = nxt
                for rule, state in nxt.items():
                    if rule.dfa.is_accept(state):
                        scan.best = better_match(
                            scan.best, (scan.next_abs, rule), scan.token_start
                        )
                if not any(rule.dfa.is_extendable(st) for rule, st in nxt.items()):
                    determined = True  # no candidate can consume another char
                    break
            if not determined:
                if final:
                    determined = True  # end of input decides
                else:
                    if len(self._buf) > self.max_buffer:
                        status = Status.INCOMPLETE
                    break
            if scan.best is None:
                rel = scan.token_start - self._base
                ch = self._buf[rel] if rel < len(self._buf) else ""
                err = LexError(scan.token_start, ch, self._stack[-1])
                err.tokens = out
                raise err
            end, rule = scan.best
            if rule.emit:
                out.append(
                    Token(
                        rule.name,
                        self._buf[scan.token_start - self._base : end - self._base],
                        scan.token_start,
                        end,
                        self._stack[-1],
                    )
                )
            apply_action(self._stack, rule.action, scan.token_start)
            self._buf = self._buf[end - self._base :]
            self._base = end
            self._scan = None
        return FeedResult(tuple(out), status, len(self._buf))
