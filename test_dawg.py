import unittest

from dawg import MinimalWordAutomaton
from dictionary import FrequencyDictionary
from metadata import FrequencyTable


class TestAlphabet(unittest.TestCase):
    def test_empty_alphabet_rejected(self):
        with self.assertRaises(ValueError):
            MinimalWordAutomaton("")

    def test_multi_char_symbol_rejected(self):
        with self.assertRaises(ValueError):
            MinimalWordAutomaton(["ab", "c"])

    def test_word_outside_alphabet_rejected(self):
        aut = MinimalWordAutomaton("ab")
        with self.assertRaises(ValueError):
            aut.add("abc")
        with self.assertRaises(ValueError):
            aut.remove("z")

    def test_non_string_rejected(self):
        aut = MinimalWordAutomaton("ab")
        with self.assertRaises(TypeError):
            aut.add(12)


class TestAutomaton(unittest.TestCase):
    def test_add_and_contains(self):
        aut = MinimalWordAutomaton("abc")
        self.assertTrue(aut.add("abc"))
        self.assertFalse(aut.add("abc"))
        self.assertIn("abc", aut)
        self.assertNotIn("ab", aut)
        self.assertEqual(len(aut), 1)

    def test_empty_word(self):
        aut = MinimalWordAutomaton("ab")
        aut.add("")
        self.assertIn("", aut)
        aut.remove("")
        self.assertNotIn("", aut)

    def test_remove_missing_returns_false(self):
        aut = MinimalWordAutomaton("ab")
        self.assertFalse(aut.remove("ab"))

    def test_delete_prefix_word_keeps_longer(self):
        aut = MinimalWordAutomaton("abcd")
        for w in ("ab", "abc", "abd"):
            aut.add(w)
        aut.remove("ab")
        self.assertNotIn("ab", aut)
        self.assertIn("abc", aut)
        self.assertIn("abd", aut)

    def test_delete_longer_keeps_prefix_word(self):
        aut = MinimalWordAutomaton("abc")
        aut.add("ab")
        aut.add("abc")
        aut.remove("abc")
        self.assertIn("ab", aut)
        self.assertNotIn("abc", aut)

    def test_prefix_queries(self):
        aut = MinimalWordAutomaton("abcd")
        for w in ("ab", "abc", "abd", "b", "bad"):
            aut.add(w)
        self.assertEqual(aut.words_with_prefix("ab"), ["ab", "abc", "abd"])
        self.assertEqual(aut.words_with_prefix("b"), ["b", "bad"])
        self.assertEqual(aut.words_with_prefix("z"), [])
        self.assertTrue(aut.has_prefix("a"))
        self.assertFalse(aut.has_prefix("c"))
        self.assertEqual(aut.words(), ["ab", "abc", "abd", "b", "bad"])

    def test_minimality_chain(self):
        aut = MinimalWordAutomaton("abc")
        for w in ("a", "ab", "abc"):
            aut.add(w)
        self.assertEqual(aut.state_count, 4)

    def test_minimality_shared_suffix(self):
        aut = MinimalWordAutomaton("abcx")
        aut.add("abc")
        aut.add("xbc")
        self.assertEqual(aut.state_count, 4)

    def test_minimality_dog_logs(self):
        aut = MinimalWordAutomaton("dgloss")
        for w in ("dog", "dogs", "log", "logs"):
            aut.add(w)
        self.assertEqual(aut.state_count, 5)

    def test_copy_on_write_shared_suffix(self):
        aut = MinimalWordAutomaton("abcx")
        aut.add("abc")
        aut.add("xbc")
        aut.remove("abc")
        self.assertIn("xbc", aut)
        self.assertNotIn("abc", aut)
        self.assertEqual(aut.words(), ["xbc"])

    def test_snapshot_persistent(self):
        aut = MinimalWordAutomaton("abcd")
        aut.add("abc")
        snap = aut.snapshot()
        aut.remove("abc")
        aut.add("abd")
        self.assertTrue(snap.contains("abc"))
        self.assertFalse(snap.contains("abd"))
        self.assertEqual(snap.words(), ["abc"])
        self.assertEqual(aut.words(), ["abd"])

    def test_language_matches_reference_set_randomized(self):
        import random

        rng = random.Random(20260922)
        alphabet = "abc"
        aut = MinimalWordAutomaton(alphabet)
        reference = set()
        words = [
            "".join(rng.choice(alphabet) for _ in range(rng.randint(0, 5)))
            for _ in range(200)
        ]
        for _ in range(2000):
            w = rng.choice(words)
            if rng.random() < 0.5:
                aut.add(w)
                reference.add(w)
            else:
                aut.remove(w)
                reference.discard(w)
            self.assertEqual(set(aut.words()), reference)
            self.assertEqual(len(aut), len(reference))


class TestFrequencyTable(unittest.TestCase):
    def test_counts(self):
        t = FrequencyTable()
        t.add("ab")
        t.add("ab")
        self.assertEqual(t.count("ab"), 2)
        self.assertEqual(t.total, 2)
        self.assertTrue(t.remove("ab"))
        self.assertEqual(t.count("ab"), 1)
        self.assertTrue(t.remove("ab"))
        self.assertFalse(t.remove("ab"))
        self.assertEqual(len(t), 0)

    def test_invalid_n(self):
        t = FrequencyTable()
        with self.assertRaises(ValueError):
            t.add("ab", 0)


class TestDictionary(unittest.TestCase):
    def test_duplicate_adds_counted(self):
        d = FrequencyDictionary("ab")
        d.add("ab")
        d.add("ab")
        self.assertEqual(d.count("ab"), 2)
        d.remove("ab")
        self.assertTrue(d.contains("ab"))
        self.assertEqual(d.count("ab"), 1)
        d.remove("ab")
        self.assertFalse(d.contains("ab"))

    def test_remove_absent_word(self):
        d = FrequencyDictionary("ab")
        self.assertFalse(d.remove("ab"))

    def test_prefix_query_separated_from_metadata(self):
        d = FrequencyDictionary("abc")
        d.add("ab")
        d.add("ab")
        d.add("abc")
        self.assertEqual(d.words_with_prefix("a"), ["ab", "abc"])
        self.assertEqual(d.frequencies_with_prefix("a"), [("ab", 2), ("abc", 1)])
        self.assertFalse(hasattr(d.automaton, "count"))

    def test_delete_prefix_word_keeps_longer_with_counts(self):
        d = FrequencyDictionary("abcd")
        d.add("ab")
        d.add("abc")
        d.remove("ab")
        self.assertFalse(d.contains("ab"))
        self.assertTrue(d.contains("abc"))
        self.assertEqual(d.count("abc"), 1)


if __name__ == "__main__":
    unittest.main()
