import random
import unittest

from regexlite import Regex


def run_stream(rx, chunks):
    st = rx.stream()
    events = []
    for chunk in chunks:
        events.extend(st.feed(chunk))
    events.extend(st.finish())
    return events


def batch(rx, text):
    return [(m.start, m.end, m.captures) for m in rx.finditer(text)]


def streamed(rx, chunks):
    return [(m.start, m.end, m.captures) for m in run_stream(rx, chunks)]


class TestLongestMatchWaiting(unittest.TestCase):
    def test_match_waits_for_longer_extension(self):
        rx = Regex.compile("ab|abc", alphabet="abcx")
        st = rx.stream()
        self.assertEqual(st.feed("ab"), [])          # could still extend
        events = st.feed("c")
        self.assertEqual([(m.start, m.end) for m in events], [(0, 3)])
        self.assertEqual(st.finish(), [])

    def test_match_finalized_by_blocking_char(self):
        rx = Regex.compile("ab|abc", alphabet="abcx")
        st = rx.stream()
        self.assertEqual(st.feed("ab"), [])
        events = st.feed("x")                        # 'x' kills extension
        self.assertEqual([(m.start, m.end) for m in events], [(0, 2)])

    def test_match_finalized_at_eof(self):
        rx = Regex.compile("ab|abc", alphabet="abcx")
        st = rx.stream()
        self.assertEqual(st.feed("ab"), [])
        events = st.finish()                         # EOF: commit longest
        self.assertEqual([(m.start, m.end) for m in events], [(0, 2)])

    def test_immediate_emit_when_no_extension_possible(self):
        rx = Regex.compile("a", alphabet="ab")
        st = rx.stream()
        events = st.feed("aa")                       # each 'a' final at once
        self.assertEqual([(m.start, m.end) for m in events], [(0, 1), (1, 2)])


class TestChunkingEquivalence(unittest.TestCase):
    CASES = [
        ("(a|ab)*b", "ab", "aababbabab"),
        ("(?<w>a*)(?<x>b*)", "ab", "aabbbab"),
        ("a*", "ab", "bbaab"),
        ("(ab|ba)(a|)", "ab", "abbaaba"),
        ("(?<k>a*)(=(?<v>b*))*", "ab=", "aa=bb a=b".replace(" ", "")),
    ]

    def test_chunked_equals_batch(self):
        rng = random.Random(20241001)
        for pattern, alphabet, text in self.CASES:
            rx = Regex.compile(pattern, alphabet=alphabet)
            want = batch(rx, text)
            # one chunk, char-by-char, and random chunkings
            chunkings = [[text], list(text)]
            for _ in range(5):
                cuts = sorted(rng.sample(range(1, len(text)), 3))
                chunks, prev = [], 0
                for cut in cuts + [len(text)]:
                    chunks.append(text[prev:cut])
                    prev = cut
                chunkings.append(chunks)
            for chunks in chunkings:
                got = streamed(rx, chunks)
                self.assertEqual(got, want, (pattern, chunks))


class TestRecoveryAndEmptyMatches(unittest.TestCase):
    def test_recovery_after_match(self):
        rx = Regex.compile("aa*", alphabet="abx")
        events = run_stream(rx, ["xaaxaa"])
        self.assertEqual([(m.start, m.end) for m in events], [(1, 3), (4, 6)])

    def test_recovery_after_unmatchable_char(self):
        rx = Regex.compile("ab", alphabet="abx")
        events = run_stream(rx, ["xxabxab"])
        self.assertEqual([(m.start, m.end) for m in events], [(2, 4), (5, 7)])

    def test_empty_match_advances(self):
        rx = Regex.compile("a*", alphabet="ab")
        events = run_stream(rx, ["b"])
        self.assertEqual([(m.start, m.end) for m in events], [(0, 0), (1, 1)])

    def test_empty_match_at_eof(self):
        rx = Regex.compile("()*", alphabet="ab")
        events = run_stream(rx, [""])
        self.assertEqual([(m.start, m.end) for m in events], [(0, 0)])

    def test_captures_in_stream(self):
        rx = Regex.compile("(?<k>a*)(?<v>b*)", alphabet="ab")
        st = rx.stream()
        events = st.feed("aa") + st.feed("bb") + st.finish()
        self.assertEqual(events[0].captures, {"k": (0, 2), "v": (2, 4)})
        self.assertEqual(events[0].group("k"), "aa")
        self.assertEqual(events[0].group("v"), "bb")

    def test_feed_after_finish_raises(self):
        rx = Regex.compile("a", alphabet="ab")
        st = rx.stream()
        st.finish()
        with self.assertRaises(RuntimeError):
            st.feed("a")
        with self.assertRaises(RuntimeError):
            st.finish()


if __name__ == "__main__":
    unittest.main()
