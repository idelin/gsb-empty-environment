"""Brute-force reference implementations for cross-checking."""

import itertools
import random

from automata import Automaton, ProductAutomaton


def brute_words(product, lo, hi):
    """All accepted words with length in [lo, hi], in lexicographic order."""
    out = []
    for n in range(lo, hi + 1):
        for tup in itertools.product(product.alphabet, repeat=n):
            word = "".join(tup)
            if product.accepts(word):
                out.append(word)
    return sorted(out)


def random_automaton(rng: random.Random, alphabet, n_states, p_trans=0.9):
    states = list(range(n_states))
    transitions = {}
    for s in states:
        for sym in alphabet:
            if rng.random() < p_trans:
                transitions[(s, sym)] = rng.choice(states)
    accepting = {s for s in states if rng.random() < 0.5}
    return Automaton(alphabet, states, initial=0, accepting=accepting,
                     transitions=transitions)


def random_product(seed, n_automata=3, alphabet="ab", max_states=4):
    rng = random.Random(seed)
    automata = [
        random_automaton(rng, alphabet, rng.randint(1, max_states))
        for _ in range(n_automata)
    ]
    return ProductAutomaton(automata)
