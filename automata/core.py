"""Explicitly constructed deterministic finite automata and their lazy product.

No regex / automata libraries are used.  The alphabet and the transition
structure are supplied explicitly by the caller.  Symbols are single
characters so that words are plain Python strings.
"""


class Automaton:
    """A deterministic finite automaton with an explicit transition table.

    ``transitions`` maps ``(state, symbol) -> state``.  Missing entries are
    treated as "no transition" (the word is rejected).  Every mutation bumps
    ``version`` so that downstream caches can be invalidated.
    """

    def __init__(self, alphabet, states, initial, accepting, transitions=None):
        alphabet = tuple(sorted(alphabet))
        if not alphabet:
            raise ValueError("alphabet must be non-empty")
        for sym in alphabet:
            if not (isinstance(sym, str) and len(sym) == 1):
                raise ValueError("alphabet symbols must be single characters")
        states = set(states)
        if initial not in states:
            raise ValueError("initial state must be a state")
        accepting = set(accepting)
        if not accepting <= states:
            raise ValueError("accepting states must be states")
        trans = {}
        for (src, sym), dst in (transitions or {}).items():
            self._check_transition(states, alphabet, src, sym, dst)
            trans[(src, sym)] = dst
        self.alphabet = alphabet
        self.states = states
        self.initial = initial
        self.accepting = accepting
        self.transitions = trans
        self._version = 0

    @staticmethod
    def _check_transition(states, alphabet, src, sym, dst):
        if src not in states:
            raise ValueError(f"unknown source state {src!r}")
        if sym not in alphabet:
            raise ValueError(f"symbol {sym!r} not in alphabet")
        if dst not in states:
            raise ValueError(f"unknown destination state {dst!r}")

    @property
    def version(self):
        return self._version

    # -- mutators (bump the version so caches invalidate) ------------------
    def add_transition(self, src, sym, dst):
        self._check_transition(self.states, self.alphabet, src, sym, dst)
        self.transitions[(src, sym)] = dst
        self._version += 1

    def set_accept(self, state, flag=True):
        if state not in self.states:
            raise ValueError(f"unknown state {state!r}")
        if flag:
            self.accepting.add(state)
        else:
            self.accepting.discard(state)
        self._version += 1

    # -- queries -----------------------------------------------------------
    def step(self, state, sym):
        return self.transitions.get((state, sym))

    def run(self, word):
        """Return the state reached by ``word`` or None if stuck."""
        state = self.initial
        for sym in word:
            state = self.step(state, sym)
            if state is None:
                return None
        return state

    def accepts(self, word):
        state = self.run(word)
        return state is not None and state in self.accepting


class ProductAutomaton:
    """On-demand (lazy) product of several automata over a shared alphabet.

    Product states are plain tuples of per-automaton states and are only
    materialised when explored, so unreachable combinations never exist.
    """

    def __init__(self, automata):
        automata = tuple(automata)
        if not automata:
            raise ValueError("need at least one automaton")
        alphabet = automata[0].alphabet
        for aut in automata:
            if aut.alphabet != alphabet:
                raise ValueError("all automata must share the same alphabet")
        self.automata = automata
        self.alphabet = alphabet

    @property
    def version(self):
        """Combined version: changes whenever any component changes."""
        return tuple(aut.version for aut in self.automata)

    def initial(self):
        return tuple(aut.initial for aut in self.automata)

    def step(self, pstate, sym):
        nxt = []
        for aut, state in zip(self.automata, pstate):
            state = aut.step(state, sym)
            if state is None:
                return None
            nxt.append(state)
        return tuple(nxt)

    def is_accept(self, pstate):
        return all(
            state in aut.accepting
            for aut, state in zip(self.automata, pstate)
        )

    def accepts(self, word):
        state = self.initial()
        for sym in word:
            state = self.step(state, sym)
            if state is None:
                return False
        return self.is_accept(state)
