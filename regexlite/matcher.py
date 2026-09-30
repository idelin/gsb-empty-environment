"""High-level matching API and chunked (streaming) matcher.

Engine strategy for :meth:`Regex.search`:

1. the lazy DFA finds the leftmost-longest match *extent*;
2. if captures are requested, the Pike VM re-runs the winning span to
   recover tags (DFA states deliberately carry no tag information);
3. if determinization exceeds its state budget, the engine does NOT return
   results from a truncated automaton: the determinizer is marked
   ``incomplete`` and, when ``allow_fallback`` is set, matching transparently
   falls back to the Pike VM (always correct, no state blowup).  With
   ``allow_fallback=False`` the :class:`BudgetExceeded` error propagates.

Streaming: :class:`StreamMatcher` accepts input in chunks.  Because match
semantics are longest-match, a candidate match is only *emitted* once it is
final -- either the threads die (no extension is possible), the remaining
threads have no outgoing transitions at all, or :meth:`finish` signals end
of input.  Until then the matcher waits for more input.
"""
from __future__ import annotations

from dataclasses import dataclass

from .dfa import BudgetExceeded, Determinizer
from .nfa import NFA, build_nfa
from .parser import Parsed, parse
from .pike import accept_thread, closure, initial_closure, pike_match_at, step


@dataclass
class Match:
    start: int
    end: int
    captures: dict[str, tuple[int, int] | None]  # label -> absolute span
    texts: dict[str, str | None]                 # label -> captured text
    matched: str
    engine: str  # 'dfa' | 'dfa+pike' | 'pike' | 'stream'

    @property
    def span(self) -> tuple[int, int]:
        return (self.start, self.end)

    def group(self, label: str, default=None):
        return self.texts.get(label, default)

    def __repr__(self):  # keep test output readable
        return (f"Match({self.start}, {self.end}, matched={self.matched!r}, "
                f"captures={self.captures}, engine={self.engine!r})")


class Regex:
    def __init__(self, parsed: Parsed, nfa: NFA, determinizer: Determinizer,
                 allow_fallback: bool):
        self.parsed = parsed
        self.pattern = parsed.pattern
        self.nfa = nfa
        self.determinizer = determinizer
        self.allow_fallback = allow_fallback

    @classmethod
    def compile(cls, pattern: str, alphabet, *, cache_size: int = 256,
                state_budget: int = 1024, allow_fallback: bool = True) -> "Regex":
        parsed = parse(pattern)
        nfa = build_nfa(parsed, alphabet)
        det = Determinizer(nfa, cache_size=cache_size, state_budget=state_budget)
        return cls(parsed, nfa, det, allow_fallback)

    # -- introspection ---------------------------------------------------
    @property
    def dfa_status(self) -> str:
        """'complete' or 'incomplete' (state budget was exceeded)."""
        return self.determinizer.status

    # -- extent engines --------------------------------------------------
    def _extent_dfa(self, text: str, start: int):
        det = self.determinizer
        state = det.start()
        last = None
        pos = start
        while True:
            if state.accepting:
                last = pos
            if pos >= len(text):
                return last
            nxt = det.step(state, text[pos])
            if nxt is None:
                return last
            state = nxt
            pos += 1

    def _extent(self, text: str, start: int):
        """Return (end | None, engine) for the longest match at ``start``."""
        try:
            return self._extent_dfa(text, start), "dfa"
        except BudgetExceeded:
            if not self.allow_fallback:
                raise
            hit = pike_match_at(self.nfa, text, start)
            return (None if hit is None else hit[0]), "pike"

    # -- batch API -------------------------------------------------------
    def _make_match(self, text: str, start: int, end: int,
                    want_captures: bool, extent_engine: str) -> Match:
        captures: dict[str, tuple[int, int] | None] = {}
        texts: dict[str, str | None] = {}
        engine = extent_engine
        if want_captures and self.nfa.nslots:
            hit = pike_match_at(self.nfa, text, start)
            assert hit is not None and hit[0] == end, "DFA/Pike extent disagree"
            tags = hit[1]
            engine = "dfa+pike" if extent_engine == "dfa" else "pike"
            for label, idx in self.nfa.labels.items():
                s, e = tags[2 * idx], tags[2 * idx + 1]
                if s is None or e is None:
                    captures[label] = None
                    texts[label] = None
                else:
                    captures[label] = (s, e)
                    texts[label] = text[s:e]
        return Match(start, end, captures, texts, text[start:end], engine)

    def search(self, text: str, pos: int = 0, want_captures: bool = True):
        """Leftmost-longest match at or after ``pos``."""
        for start in range(pos, len(text) + 1):
            end, engine = self._extent(text, start)
            if end is not None:
                return self._make_match(text, start, end, want_captures, engine)
        return None

    def match(self, text: str, pos: int = 0, want_captures: bool = True):
        """Longest match anchored exactly at ``pos`` (None if no match)."""
        end, engine = self._extent(text, pos)
        if end is None:
            return None
        return self._make_match(text, pos, end, want_captures, engine)

    def fullmatch(self, text: str, want_captures: bool = True):
        m = self.match(text, 0, want_captures)
        return m if m is not None and m.end == len(text) else None

    def finditer(self, text: str, want_captures: bool = True):
        """Non-overlapping leftmost-longest matches, left to right."""
        pos = 0
        while pos <= len(text):
            m = self.search(text, pos, want_captures)
            if m is None:
                return
            yield m
            pos = m.end if m.end > m.start else m.end + 1

    def stream(self) -> "StreamMatcher":
        return StreamMatcher(self)


class StreamMatcher:
    """Incremental matcher fed with ``feed(chunk)`` / finalized by ``finish()``.

    Emits the same non-overlapping leftmost-longest matches as
    ``Regex.finditer`` over the concatenation of all chunks, but a match is
    only emitted once no future input could extend it (longest-match
    waiting).  After each emitted match the automaton restarts at the match
    end (or one past it for empty matches), so scanning recovers and
    continues within the same stream.
    """

    def __init__(self, regex: Regex):
        self.regex = regex
        self.nfa = regex.nfa
        self.buf = ""
        self.base = 0      # absolute position of buf[0]
        self.total = 0     # total chars fed
        self.start = 0     # absolute start of the current attempt
        self.pos = 0       # absolute position of the next char to consume
        self.clist = initial_closure(self.nfa, 0)
        self.last = accept_thread(self.nfa, self.clist, 0)
        self.done = False

    # -- public ----------------------------------------------------------
    def feed(self, chunk: str) -> list[Match]:
        if self.done:
            raise RuntimeError("stream already finished")
        self.buf += chunk
        self.total += len(chunk)
        return self._pump()

    def finish(self) -> list[Match]:
        """Signal end of input; flush all remaining (now final) matches."""
        if self.done:
            raise RuntimeError("stream already finished")
        self.done = True
        events: list[Match] = []
        while self.start <= self.total:
            events.extend(self._finalize())
        self.buf = ""
        self.base = self.total
        return events

    # -- internals ---------------------------------------------------------
    def _reset(self, new_start: int) -> None:
        self.start = new_start
        self.pos = new_start
        self.clist = initial_closure(self.nfa, new_start)
        self.last = accept_thread(self.nfa, self.clist, new_start)

    def _pump(self) -> list[Match]:
        events: list[Match] = []
        while self.pos < self.base + len(self.buf):
            char = self.buf[self.pos - self.base]
            self.clist = step(self.nfa, self.clist, char, self.pos)
            self.pos += 1
            hit = accept_thread(self.nfa, self.clist, self.pos)
            if hit is not None:
                self.last = hit
            dead = not self.clist
            stuck = not any(self.nfa.sym[state] for state, _ in self.clist)
            if dead or stuck:
                # No extension is possible regardless of future input:
                # the pending longest match (if any) is final.
                events.extend(self._finalize())
        return events

    def _finalize(self) -> list[Match]:
        events: list[Match] = []
        if self.last is not None:
            end, tags = self.last
            events.append(self._emit(self.start, end, tags))
            new_start = end if end > self.start else self.start + 1
        else:
            new_start = self.start + 1  # no match here: skip one char, recover
        if new_start > self.base:  # drop consumed prefix; keep the rest
            drop = new_start - self.base
            self.buf = self.buf[drop:]
            self.base = new_start
        self._reset(new_start)  # reprocess buf[new_start:pos] via _pump
        return events

    def _emit(self, start: int, end: int, tags) -> Match:
        buf, base = self.buf, self.base
        captures: dict[str, tuple[int, int] | None] = {}
        texts: dict[str, str | None] = {}
        for label, idx in self.nfa.labels.items():
            s, e = tags[2 * idx], tags[2 * idx + 1]
            if s is None or e is None:
                captures[label] = None
                texts[label] = None
            else:
                captures[label] = (s, e)
                texts[label] = buf[s - base:e - base]
        return Match(start, end, captures, texts,
                     buf[start - base:end - base], "stream")
