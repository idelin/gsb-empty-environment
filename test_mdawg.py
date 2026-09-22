"""unittest suite for mdawg.  Run: python3 -m unittest -v"""

import random
import unittest

from mdawg import AlphabetError, Lexicon, MinimalAcyclicAutomaton


class TestAlphabet(unittest.TestCase):
    def test_explicit_alphabet_required(self):
        with self.assertRaises(AlphabetError):
            MinimalAcyclicAutomaton("")
        with self.assertRaises(AlphabetError):
            MinimalAcyclicAutomaton({"ab"})  # multi-char symbol rejected

    def test_out_of_alphabet_word_rejected(self):
        a = MinimalAcyclicAutomaton("abc")
        with self.assertRaises(AlphabetError):
            a.add("abd")
        with self.assertRaises(AlphabetError):
            a.remove("abd")
        lex = Lexicon("abc")
        with self.assertRaises(AlphabetError):
            lex.add("xyz")

    def test_empty_word(self):
        a = MinimalAcyclicAutomaton("ab")
        a.add("")
        self.assertIn("", a)
        self.assertTrue(a.remove(""))
        self.assertNotIn("", a)


class TestAddRemove(unittest.TestCase):
    def test_add_contains_remove(self):
        a = MinimalAcyclicAutomaton("abcdef")
        self.assertTrue(a.add("face"))
        self.assertFalse(a.add("face"))  # duplicate add: language unchanged
        self.assertIn("face", a)
        self.assertNotIn("fac", a)
        self.assertTrue(a.remove("face"))
        self.assertFalse(a.remove("face"))  # already gone
        self.assertNotIn("face", a)

    def test_delete_prefix_word_keeps_longer_words(self):
        a = MinimalAcyclicAutomaton("aelps")
        for w in ("app", "apple", "apples"):
            a.add(w)
        self.assertTrue(a.remove("app"))
        self.assertNotIn("app", a)
        self.assertIn("apple", a)
        self.assertIn("apples", a)
        self.assertTrue(a.remove("apple"))
        self.assertIn("apples", a)
        self.assertTrue(a.is_minimal())

    def test_delete_longer_word_keeps_prefix_word(self):
        a = MinimalAcyclicAutomaton("aelp")
        a.add("app")
        a.add("apple")
        a.remove("apple")
        self.assertIn("app", a)
        self.assertNotIn("apple", a)
        self.assertTrue(a.is_minimal())

    def test_copy_on_write_shared_suffix(self):
        # "dog" and "log" share the "og" suffix states in the minimal DFA.
        a = MinimalAcyclicAutomaton("dglo")
        a.add("dog")
        a.add("log")
        shared = a.reachable_states()
        self.assertEqual(shared, 4)  # root, shared d/l target, o-state, final leaf
        a.remove("dog")
        self.assertNotIn("dog", a)
        self.assertIn("log", a)  # shared suffix not polluted
        self.assertTrue(a.is_minimal())

    def test_copy_on_write_shared_prefix(self):
        a = MinimalAcyclicAutomaton("abcd")
        a.add("abc")
        a.add("abd")
        a.remove("abc")
        self.assertIn("abd", a)
        self.assertNotIn("abc", a)
        self.assertTrue(a.is_minimal())

    def test_remove_cascades_and_stops_at_final(self):
        a = MinimalAcyclicAutomaton("abc")
        a.add("a")
        a.add("abc")
        a.remove("abc")
        self.assertIn("a", a)
        self.assertEqual(a.reachable_states(), 2)  # root + final leaf
        self.assertTrue(a.is_minimal())

    def test_remove_everything_leaves_empty_root(self):
        a = MinimalAcyclicAutomaton("ab")
        a.add("a")
        a.add("b")
        a.remove("a")
        a.remove("b")
        self.assertEqual(list(a.iter_words()), [])
        self.assertEqual(a.reachable_states(), 1)
        self.assertTrue(a.is_minimal())


class TestPrefixQueries(unittest.TestCase):
    def test_has_prefix(self):
        a = MinimalAcyclicAutomaton("abcde")
        a.add("abc")
        a.add("abd")
        self.assertTrue(a.has_prefix("ab"))
        self.assertTrue(a.has_prefix("abc"))
        self.assertFalse(a.has_prefix("abe"))
        self.assertTrue(a.has_prefix(""))

    def test_iter_words_sorted(self):
        a = MinimalAcyclicAutomaton("abc")
        for w in ("cba", "ab", "abc", "ba"):
            a.add(w)
        self.assertEqual(list(a.iter_words()), ["ab", "abc", "ba", "cba"])
        self.assertEqual(list(a.iter_words("a")), ["ab", "abc"])
        self.assertEqual(list(a.iter_words("z")), [])


class TestFrequencyMetadata(unittest.TestCase):
    def test_repeated_add_counts(self):
        lex = Lexicon("abc")
        lex.add("ab")
        lex.add("ab")
        lex.add("ab", count=3)
        self.assertEqual(lex.frequency("ab"), 5)
        self.assertEqual(len(lex), 1)  # automaton still stores one word

    def test_remove_decrements_then_deletes(self):
        lex = Lexicon("abc")
        lex.add("ab", count=2)
        self.assertTrue(lex.remove("ab"))
        self.assertIn("ab", lex)  # count 1 left, word still in automaton
        self.assertEqual(lex.frequency("ab"), 1)
        self.assertTrue(lex.remove("ab"))
        self.assertNotIn("ab", lex)
        self.assertNotIn("ab", lex.automaton)
        self.assertFalse(lex.remove("ab"))

    def test_metadata_separated_from_structure(self):
        lex = Lexicon("abcd")
        lex.add("bad", count=7)
        lex.add("dad", count=2)  # shares the "ad" suffix with "bad"
        self.assertEqual(lex.words_with_prefix("b"), [("bad", 7)])
        self.assertEqual(lex.words_with_prefix("d"), [("dad", 2)])
        self.assertEqual(lex.frequency("bad"), 7)
        self.assertEqual(lex.frequency("dad"), 2)
        # Counts live outside the automaton: removing one word's count
        # cannot disturb the other's, despite the shared suffix states.
        while lex.remove("bad"):
            pass
        self.assertEqual(lex.frequency("dad"), 2)
        self.assertIn("dad", lex.automaton)

    def test_prefix_query_returns_counts(self):
        lex = Lexicon("aelprt")
        lex.add("app", count=3)
        lex.add("apple")
        lex.add("ape", count=2)
        self.assertEqual(
            lex.words_with_prefix("app"), [("app", 3), ("apple", 1)]
        )
        self.assertEqual(
            lex.words_with_prefix("ap"),
            [("ape", 2), ("app", 3), ("apple", 1)],
        )
        self.assertTrue(lex.has_prefix("appl"))
        self.assertFalse(lex.has_prefix("aq"))


class TestRebuildStrategy(unittest.TestCase):
    def test_rebuild_matches_online(self):
        words = ["dog", "log", "dot", "lot", "do", "lo"]
        online = MinimalAcyclicAutomaton("dglot")
        for w in words:
            online.add(w)
        rebuilt = MinimalAcyclicAutomaton.rebuild(words, "dglot")
        self.assertEqual(online.reachable_states(), rebuilt.reachable_states())
        self.assertEqual(sorted(online.iter_words()), sorted(rebuilt.iter_words()))
        self.assertTrue(rebuilt.is_minimal())

    def test_compact_collects_garbage(self):
        a = MinimalAcyclicAutomaton("abcdef")
        for w in ("abc", "abd", "abe", "abf"):
            a.add(w)
        for w in ("abc", "abd", "abe"):
            a.remove(w)
        self.assertGreater(a.total_states(), a.reachable_states())
        compact = a.compact()
        self.assertEqual(compact.total_states(), compact.reachable_states())
        self.assertEqual(list(compact.iter_words()), ["abf"])


class TestRandomisedModel(unittest.TestCase):
    def test_fuzz_against_set_model(self):
        rng = random.Random(20260922)
        alphabet = "abcd"
        words = [
            "".join(rng.choice(alphabet) for _ in range(rng.randint(0, 6)))
            for _ in range(60)
        ]
        a = MinimalAcyclicAutomaton(alphabet)
        model = set()
        for step in range(400):
            w = rng.choice(words)
            if rng.random() < 0.55:
                self.assertEqual(a.add(w), w not in model)
                model.add(w)
            else:
                self.assertEqual(a.remove(w), w in model)
                model.discard(w)
            self.assertEqual(sorted(a.iter_words()), sorted(model), f"step {step}")
            self.assertTrue(a.is_minimal(), f"step {step}")
            for probe in rng.sample(words, 3):
                self.assertEqual(probe in a, probe in model)
        # Cross-check minimality: online state count equals fresh rebuild.
        rebuilt = MinimalAcyclicAutomaton.rebuild(model, alphabet)
        self.assertEqual(a.reachable_states(), rebuilt.reachable_states())

    def test_fuzz_lexicon_counts(self):
        rng = random.Random(42)
        alphabet = "ab"
        words = [
            "".join(rng.choice(alphabet) for _ in range(rng.randint(1, 4)))
            for _ in range(10)
        ]
        lex = Lexicon(alphabet)
        model = {}
        for _ in range(300):
            w = rng.choice(words)
            if rng.random() < 0.6:
                lex.add(w)
                model[w] = model.get(w, 0) + 1
            else:
                if lex.remove(w):
                    model[w] -= 1
                    if model[w] == 0:
                        del model[w]
            for probe in rng.sample(words, 2):
                self.assertEqual(lex.frequency(probe), model.get(probe, 0))
        self.assertEqual(
            sorted(w for w, _ in lex.words_with_prefix()), sorted(model)
        )
        self.assertTrue(lex.automaton.is_minimal())


if __name__ == "__main__":
    unittest.main()
