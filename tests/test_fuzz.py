import os
import random
import subprocess
import sys
import tempfile
import unittest

from regexlite import Regex, parse
from regexlite.fuzz import (
    Mismatch, all_strings, check_pattern, check_string, random_ast,
    to_pattern, write_reproducer,
)
from regexlite.reference import ref_search

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))

# Directed tricky patterns: empty-string loops, overlapping branches,
# capture boundaries, empty language vs empty string, recovery after match.
TRICKY = [
    "(a*)*", "(a*)*a", "((a*)*)*", "(a|)*", "()*", "a**", "(a*b*)*",
    "a|ab", "ab|a", "(a|ab)*", "(ab|a)*b", "a(a|ab)*",
    "(a)(b)", "((a)b)*", "(a|b)(b|a)", "(a*)(b*)", "(a|)(a|)",
    "([])*", "[]", "a[]|b", "([]|a)*", "[^]*",
    "(a|a)*", "(aa|a)*", "(a|aa)*b", "(a(b|))*",
]


class TestDirectedExhaustive(unittest.TestCase):
    def test_tricky_patterns_exhaustive(self):
        for pattern in TRICKY:
            with self.subTest(pattern=pattern):
                mm, skipped = check_pattern(pattern, "ab", max_len=5)
                if mm is not None:
                    self.fail(f"{pattern!r} on {mm.string!r}: "
                              f"expected {mm.expected}, got {mm.got}")


class TestRandomExhaustive(unittest.TestCase):
    def test_random_patterns_exhaustive(self):
        rng = random.Random(0xC0FFEE)
        checked = skipped_total = 0
        for _ in range(60):
            pattern = to_pattern(random_ast(rng, "ab", depth=3))
            mm, skipped = check_pattern(pattern, "ab", max_len=4)
            skipped_total += skipped
            if mm is not None:
                self.fail(f"{pattern!r} on {mm.string!r}: "
                          f"expected {mm.expected}, got {mm.got}")
            checked += 1
        self.assertEqual(checked, 60)


class TestSerializerRoundTrip(unittest.TestCase):
    def test_random_ast_roundtrip(self):
        rng = random.Random(7)
        for _ in range(200):
            pattern = to_pattern(random_ast(rng, "ab", depth=3))
            parsed = parse(pattern)  # must not raise
            self.assertEqual(parsed.pattern, pattern)


class TestStreamEqualsBatch(unittest.TestCase):
    def test_stream_matches_finditer_on_random_inputs(self):
        rng = random.Random(99)
        for _ in range(30):
            pattern = to_pattern(random_ast(rng, "ab", depth=2))
            rx = Regex.compile(pattern, alphabet="ab")
            text = "".join(rng.choice("ab") for _ in range(rng.randint(0, 12)))
            want = [(m.start, m.end, m.captures) for m in rx.finditer(text)]
            st = rx.stream()
            got = []
            for ch in text:  # feed one char at a time: maximal chunk stress
                got.extend(st.feed(ch))
            got.extend(st.finish())
            got = [(m.start, m.end, m.captures) for m in got]
            self.assertEqual(got, want, (pattern, text))


class TestReproducer(unittest.TestCase):
    def run_repro(self, mm):
        with tempfile.TemporaryDirectory() as tmp:
            path = write_reproducer(mm, tmp, ROOT)
            proc = subprocess.run(
                [sys.executable, path], capture_output=True, text=True)
            return proc

    def test_consistent_case_exits_zero(self):
        pattern, s = "(a|ab)*", "aab"
        parsed = parse(pattern)
        expected = ref_search(parsed, "ab", s)
        mm = Mismatch(pattern, "ab", s, expected, expected)  # agree
        proc = self.run_repro(mm)
        self.assertEqual(proc.returncode, 0, proc.stdout + proc.stderr)
        self.assertIn("engines agree", proc.stdout)

    def test_recorded_mismatch_exits_one(self):
        pattern, s = "a|ab", "ab"
        parsed = parse(pattern)
        expected = ref_search(parsed, "ab", s)
        bogus = (0, 1, expected[2])  # recorded pike result disagrees
        mm = Mismatch(pattern, "ab", s, expected, bogus)
        proc = self.run_repro(mm)
        self.assertEqual(proc.returncode, 1, proc.stdout + proc.stderr)
        self.assertIn("MISMATCH", proc.stdout)

    def test_reproducer_is_standalone_file(self):
        mm = Mismatch("a", "ab", "a", (0, 1, ()), (0, 1, ()))
        with tempfile.TemporaryDirectory() as tmp:
            path = write_reproducer(mm, tmp, ROOT, name="repro_x.py")
            self.assertTrue(os.path.isfile(path))
            with open(path) as fh:
                content = fh.read()
            self.assertIn("def main", content)
            self.assertIn("sys.exit", content)


class TestReferenceSanity(unittest.TestCase):
    def test_empty_string_vs_empty_language(self):
        self.assertIsNotNone(ref_search(parse(""), "ab", ""))
        self.assertIsNone(ref_search(parse("[]"), "ab", ""))

    def test_nested_star_terminates_quickly(self):
        # would hang forever without the empty-iteration rule
        result = ref_search(parse("(a*)*"), "ab", "aaaa")
        self.assertEqual(result[:2], (0, 4))

    def test_all_strings_enumeration(self):
        self.assertEqual(len(list(all_strings("ab", 3))), 1 + 2 + 4 + 8)


if __name__ == "__main__":
    unittest.main()
