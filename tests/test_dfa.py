import unittest

from regexlite import (
    BudgetExceeded, Determinizer, Regex, build_nfa, parse, pike_search,
)


def make(pattern, alphabet="ab", **kw):
    nfa = build_nfa(parse(pattern), alphabet)
    return nfa, Determinizer(nfa, **kw)


def dfa_extent(det, text, start):
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
        state, pos = nxt, pos + 1


class TestLazyDeterminization(unittest.TestCase):
    def test_extent_matches_pike(self):
        nfa, det = make("(a|ab)*(b|)", state_budget=1000)
        for text in ["", "a", "ab", "aba", "abab", "baa", "abba"]:
            for start in range(len(text) + 1):
                hit = pike_search(nfa, text, start)
                want = None if hit is None or hit[0] != start else hit[1]
                self.assertEqual(dfa_extent(det, text, start), want,
                                 (text, start))

    def test_states_built_on_demand(self):
        nfa, det = make("a*b", state_budget=1000)
        self.assertEqual(det.distinct_states, 0)
        det.start()
        self.assertEqual(det.distinct_states, 1)
        dfa_extent(det, "aab", 0)
        self.assertGreater(det.distinct_states, 1)

    def test_unknown_symbol_is_dead(self):
        nfa, det = make("aa", state_budget=10)
        state = det.start()
        self.assertIsNone(det.step(state, "z"))  # not in alphabet: dead


class TestBudget(unittest.TestCase):
    def test_budget_exceeded_marks_incomplete(self):
        nfa, det = make("(a|b)*abb", state_budget=2)
        with self.assertRaises(BudgetExceeded):
            dfa_extent(det, "aabbb", 0)
        self.assertEqual(det.status, "incomplete")

    def test_no_wrong_automaton_on_budget(self):
        # With a tiny budget the Regex must not silently return DFA results;
        # it falls back to the Pike VM and reports incomplete status.
        rx = Regex.compile("(a|b)*abb", alphabet="ab", state_budget=2)
        m = rx.search("aabbb")
        self.assertEqual(m.span, (0, 4))
        self.assertEqual(m.engine, "pike")
        self.assertEqual(rx.dfa_status, "incomplete")

    def test_fallback_disabled_raises(self):
        rx = Regex.compile("(a|b)*abb", alphabet="ab", state_budget=2,
                           allow_fallback=False)
        with self.assertRaises(BudgetExceeded):
            rx.search("aabbb")

    def test_generous_budget_stays_complete(self):
        rx = Regex.compile("(a|b)*abb", alphabet="ab", state_budget=100)
        m = rx.search("aabbb")
        self.assertEqual(m.span, (0, 4))
        self.assertEqual(rx.dfa_status, "complete")


class TestEvictionKeepsSemantics(unittest.TestCase):
    PATTERN = "(a|b)(ab|ba)*(a|b)"
    TEXTS = ["aba", "abba", "baba", "aabbaab", "bbababa", "ababab"]

    def extents(self, cache_size):
        nfa, det = make(self.PATTERN, cache_size=cache_size, state_budget=1000)
        return [tuple(dfa_extent(det, t, 0) for t in self.TEXTS)], det

    def test_tiny_cache_same_results(self):
        big, det_big = self.extents(cache_size=1000)
        tiny, det_tiny = self.extents(cache_size=1)
        small, _ = self.extents(cache_size=3)
        self.assertEqual(big, tiny)
        self.assertEqual(big, small)
        # eviction actually happened and forced recomputation
        self.assertGreater(det_tiny.materializations, det_big.materializations)
        self.assertLessEqual(det_tiny.cached_states, 1)

    def test_cache_size_validated(self):
        nfa = build_nfa(parse("a"), "ab")
        with self.assertRaises(ValueError):
            Determinizer(nfa, cache_size=0)
        with self.assertRaises(ValueError):
            Determinizer(nfa, state_budget=0)


if __name__ == "__main__":
    unittest.main()
