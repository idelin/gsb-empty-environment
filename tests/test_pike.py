import unittest

from regexlite import CompileError, Regex, build_nfa, parse, pike_search

AB = "ab"


def search(pattern, text, alphabet=AB):
    nfa = build_nfa(parse(pattern), alphabet)
    return pike_search(nfa, text)


class TestBasicMatching(unittest.TestCase):
    def test_literal_search_leftmost(self):
        self.assertEqual(search("ab", "xxabby", "abxy")[:2], (2, 4))

    def test_no_match(self):
        self.assertIsNone(search("ba", "aaaa"))

    def test_longest_beats_branch_order(self):
        # 'a' is the higher-priority branch, but the longest extent wins
        self.assertEqual(search("a|ab", "ab")[:2], (0, 2))
        self.assertEqual(search("ab|a", "ab")[:2], (0, 2))

    def test_overlapping_branches_star(self):
        self.assertEqual(search("(a|ab)*", "aba")[:2], (0, 3))
        self.assertEqual(search("(ab|a)*", "aba")[:2], (0, 3))

    def test_char_class_and_negation(self):
        self.assertEqual(search("[^b]*b", "aaab")[:2], (0, 4))
        self.assertIsNone(search("[^a]", "a"))

    def test_dot_uses_explicit_alphabet(self):
        self.assertEqual(search(".*", "ab")[:2], (0, 2))

    def test_symbol_outside_alphabet_matches_nothing(self):
        # 'z' is not in the alphabet: no transition, only the empty match survives
        self.assertEqual(search("a*", "z", alphabet="ab")[:2], (0, 0))


class TestEmptyVsEmptyLanguage(unittest.TestCase):
    def test_empty_pattern_matches_empty_string(self):
        self.assertEqual(search("", "")[:2], (0, 0))

    def test_empty_group_matches_empty_string(self):
        self.assertEqual(search("()", "")[:2], (0, 0))

    def test_empty_language_matches_nothing(self):
        self.assertIsNone(search("[]", ""))
        self.assertIsNone(search("a[]", "a"))

    def test_star_of_empty_language_is_empty_string(self):
        self.assertEqual(search("([])*", "")[:2], (0, 0))

    def test_empty_language_kills_concat_branch(self):
        self.assertIsNone(search("a[]|b", "a"))
        self.assertEqual(search("a[]|b", "b")[:2], (0, 1))


class TestNestedStarsTerminate(unittest.TestCase):
    def test_star_of_star(self):
        self.assertEqual(search("(a*)*", "aa")[:2], (0, 2))

    def test_star_of_star_empty(self):
        self.assertEqual(search("(a*)*", "")[:2], (0, 0))

    def test_double_star_literal(self):
        self.assertEqual(search("a**", "aaa")[:2], (0, 3))

    def test_star_of_empty_group(self):
        self.assertEqual(search("()*", "")[:2], (0, 0))
        self.assertEqual(search("()*", "a")[:2], (0, 0))

    def test_deep_nesting(self):
        self.assertEqual(search("(((a*)*)*)*", "aa")[:2], (0, 2))


class TestCaptures(unittest.TestCase):
    def test_labelled_captures(self):
        rx = Regex.compile("(?<k>a*)(?<v>b*)", alphabet=AB)
        m = rx.search("aabb")
        self.assertEqual(m.captures, {"k": (0, 2), "v": (2, 4)})
        self.assertEqual(m.group("k"), "aa")
        self.assertEqual(m.group("v"), "bb")

    def test_optional_group_absent(self):
        rx = Regex.compile("(?<x>a)(?<y>b|)", alphabet=AB)
        m = rx.search("a")
        self.assertEqual(m.captures["y"], (1, 1))

    def test_priority_resolves_capture_boundaries(self):
        # Both g1='a',g2='bcd' and g1='ab',g2='c' reach end 4; the first
        # (higher-priority) path supplies the tags.
        rx = Regex.compile("(?<g1>a|ab)(?<g2>c|bcd)(?<g3>d*)", alphabet="abcd")
        m = rx.search("abcd")
        self.assertEqual(m.captures["g1"], (0, 1))
        self.assertEqual(m.captures["g2"], (1, 4))
        self.assertEqual(m.captures["g3"], (4, 4))

    def test_nested_group_boundaries(self):
        rx = Regex.compile("(?<outer>a(?<inner>b)*)", alphabet=AB)
        m = rx.search("abb")
        self.assertEqual(m.span, (0, 3))
        self.assertEqual(m.captures["outer"], (0, 3))
        self.assertEqual(m.captures["inner"], (2, 3))  # last iteration wins

    def test_star_capture_keeps_last_iteration(self):
        rx = Regex.compile("(?<x>a)*", alphabet=AB)
        m = rx.search("aaa")
        self.assertEqual(m.captures["x"], (2, 3))

    def test_capture_in_failed_longer_branch_not_reported(self):
        # (a|ab)b on "ab": g1='ab' then 'b' fails; g1='a','b' succeeds.
        rx = Regex.compile("(?<g>a|ab)b", alphabet=AB)
        m = rx.search("ab")
        self.assertEqual(m.span, (0, 2))
        self.assertEqual(m.captures["g"], (0, 1))


class TestAlphabetValidation(unittest.TestCase):
    def test_literal_outside_alphabet_rejected_with_span(self):
        with self.assertRaises(CompileError) as ctx:
            Regex.compile("abz", alphabet="ab")
        self.assertEqual(ctx.exception.span, (2, 3))

    def test_empty_alphabet_rejected(self):
        with self.assertRaises(CompileError):
            Regex.compile("a", alphabet="")


class TestSourcePositionsPreserved(unittest.TestCase):
    def test_nfa_states_carry_pattern_spans(self):
        nfa = build_nfa(parse("a(bc)*"), "abc")
        spans = {s for s in nfa.origins if s is not None}
        self.assertIn((0, 1), spans)   # literal a
        self.assertIn((1, 5), spans)   # group (bc)
        self.assertIn((1, 6), spans)   # star

    def test_every_state_has_origin(self):
        nfa = build_nfa(parse("(?<x>a|b)*c"), "abc")
        self.assertTrue(all(s is not None for s in nfa.origins))


if __name__ == "__main__":
    unittest.main()
