import io
import random
import unittest

from prefix_dict import InvariantError, PrefixDictionary
from prefix_dict.__main__ import run as run_cli


class ReferenceDictionary:
    """Trivial dict-backed oracle for differential testing."""

    def __init__(self) -> None:
        self.data: dict[str, int] = {}

    def insert(self, key: str, weight: int) -> None:
        self.data[key] = weight

    def delete(self, key: str) -> bool:
        if key in self.data:
            del self.data[key]
            return True
        return False

    def weight_of(self, key: str):
        return self.data.get(key)

    def prefix_topk(self, prefix: str, k: int):
        pairs = [
            (key, weight)
            for key, weight in self.data.items()
            if key.startswith(prefix)
        ]
        pairs.sort(key=lambda item: (-item[1], item[0]))
        return pairs[:k]


class AcceptanceTests(unittest.TestCase):
    def test_acceptance_boundary(self) -> None:
        dic = PrefixDictionary()
        dic.insert("ab", 2)
        dic.insert("abc", 3)
        dic.check_invariants()

        self.assertEqual(
            dic.prefix_topk("ab", 10), [("abc", 3), ("ab", 2)]
        )
        self.assertTrue(dic.delete("abc"))
        dic.check_invariants()

        self.assertIsNone(dic.weight_of("abc"))
        self.assertEqual(dic.weight_of("ab"), 2)
        self.assertIn("ab", dic)
        self.assertEqual(dic.prefix_topk("ab", 10), [("ab", 2)])

    def test_empty_prefix_is_whole_dictionary(self) -> None:
        dic = PrefixDictionary()
        dic.insert("ab", 2)
        dic.insert("abc", 3)
        dic.insert("z", 1)
        self.assertEqual(
            dic.prefix_topk("", 10),
            [("abc", 3), ("ab", 2), ("z", 1)],
        )

    def test_k_zero_and_limits(self) -> None:
        dic = PrefixDictionary()
        dic.insert("a", 1)
        self.assertEqual(dic.prefix_topk("", 0), [])
        self.assertEqual(dic.prefix_topk("", 1), [("a", 1)])
        self.assertEqual(dic.prefix_topk("zzz", 5), [])

    def test_weight_tie_breaks_by_unicode_codepoint(self) -> None:
        dic = PrefixDictionary()
        dic.insert("ab", 5)
        dic.insert("aa", 5)
        dic.insert("中", 5)
        dic.insert("a", 5)
        # Same weight -> ascending code point: 'a' < 'aa' < 'ab' < '中'
        self.assertEqual(
            [key for key, _ in dic.prefix_topk("", 10)],
            ["a", "aa", "ab", "中"],
        )

    def test_weight_update_without_rebuild_semantics(self) -> None:
        dic = PrefixDictionary()
        dic.insert("a", 1)
        dic.insert("b", 2)
        dic.insert("a", 10)  # overwrite; no second entry
        self.assertEqual(len(dic), 2)
        self.assertEqual(dic.weight_of("a"), 10)
        self.assertEqual(dic.prefix_topk("", 10), [("a", 10), ("b", 2)])
        dic.check_invariants()

    def test_empty_key_at_root(self) -> None:
        dic = PrefixDictionary()
        dic.insert("", 7)
        dic.insert("a", 3)
        self.assertEqual(dic.weight_of(""), 7)
        self.assertEqual(dic.prefix_topk("", 10), [("", 7), ("a", 3)])
        self.assertTrue(dic.delete(""))
        self.assertIsNone(dic.weight_of(""))
        self.assertEqual(dic.weight_of("a"), 3)
        dic.check_invariants()

    def test_delete_missing(self) -> None:
        dic = PrefixDictionary()
        self.assertFalse(dic.delete("nope"))
        dic.insert("abc", 1)
        self.assertFalse(dic.delete("ab"))   # prefix of a stored key
        self.assertFalse(dic.delete("abcd"))  # extension past a stored key
        self.assertTrue(dic.delete("abc"))
        dic.check_invariants()

    def test_compression_merge_after_delete(self) -> None:
        dic = PrefixDictionary()
        for key, weight in [
            ("team", 1), ("test", 2), ("toaster", 3), ("t", 4),
        ]:
            dic.insert(key, weight)
        dic.check_invariants()
        # Removing 't' forces a chain merge under 't'.
        self.assertTrue(dic.delete("t"))
        dic.check_invariants()
        self.assertEqual(
            sorted(dic.items()),
            [("team", 1), ("test", 2), ("toaster", 3)],
        )
        self.assertEqual(dic.weight_of("test"), 2)
        # Insert again to confirm merged edges still split correctly.
        dic.insert("t", 9)
        dic.check_invariants()
        self.assertEqual(dic.prefix_topk("t", 2), [("t", 9), ("toaster", 3)])

    def test_stale_size_counter(self) -> None:
        dic = PrefixDictionary()
        dic.insert("abc", 1)
        dic.insert("abd", 2)
        dic.insert("ab", 3)
        self.assertEqual(len(dic), 3)
        dic.delete("ab")
        self.assertEqual(len(dic), 2)
        dic.check_invariants()


class CliTests(unittest.TestCase):
    def test_cli_session(self) -> None:
        stdin = io.StringIO(
            "INSERT ab 2\n"
            "INSERT abc 3\n"
            "TOPK ab 10\n"
            "DELETE abc\n"
            "WEIGHT ab\n"
            "WEIGHT abc\n"
            "TOPK - 10\n"
            "CHECK\n"
            "BOGUS\n"
        )
        stdout = io.StringIO()
        code = run_cli(stdin, stdout)
        self.assertEqual(code, 0)
        lines = stdout.getvalue().splitlines()
        self.assertIn("3\tabc", lines)
        self.assertIn("2\tab", lines)
        self.assertEqual(lines[-4], "MISSING")
        self.assertIn("2\tab", lines)
        self.assertIn("OK", lines)
        self.assertTrue(any(line.startswith("ERROR") for line in lines))

    def test_cli_unicode_tie(self) -> None:
        stdin = io.StringIO(
            'INSERT "中" 5\nINSERT a 5\nTOPK all 5\n'
        )
        stdout = io.StringIO()
        run_cli(stdin, stdout)
        body = stdout.getvalue()
        self.assertLess(body.index("5\ta\n"), body.index("5\t中\n"))


class RandomDifferentialTests(unittest.TestCase):
    ALPHABET = list("ab") + ["中"]

    def _random_key(self, rng: random.Random) -> str:
        return "".join(rng.choice(self.ALPHABET) for _ in range(rng.randint(0, 4)))

    def test_random_operations_against_reference(self) -> None:
        rng = random.Random(20260927)
        dic = PrefixDictionary()
        ref = ReferenceDictionary()
        prefixes = ["", "a", "b", "ab", "中"]

        for step in range(4000):
            action = rng.random()
            key = self._random_key(rng)
            if action < 0.55:
                weight = rng.randint(0, 4)
                dic.insert(key, weight)
                ref.insert(key, weight)
            elif action < 0.80:
                self.assertEqual(dic.delete(key), ref.delete(key), step)
            else:
                prefix = rng.choice(prefixes)
                k = rng.randint(0, 6)
                self.assertEqual(
                    dic.prefix_topk(prefix, k),
                    ref.prefix_topk(prefix, k),
                    (step, prefix, k),
                )
            self.assertEqual(len(dic), len(ref.data), step)
            self.assertEqual(dic.weight_of(key), ref.weight_of(key), step)
            if step % 25 == 0:
                dic.check_invariants()
        dic.check_invariants()

    def test_stress_deep_merge_cycle(self) -> None:
        rng = random.Random(42)
        dic = PrefixDictionary()
        ref = ReferenceDictionary()
        keys = {
            "".join(rng.choice("abc") for _ in range(rng.randint(1, 6)))
            for _ in range(300)
        }
        for key in keys:
            w = rng.randint(0, 10)
            dic.insert(key, w)
            ref.insert(key, w)
        dic.check_invariants()
        for key in list(keys):
            if rng.random() < 0.7:
                self.assertEqual(dic.delete(key), ref.delete(key))
        dic.check_invariants()
        for prefix in ("", "a", "ab", "c"):
            self.assertEqual(
                dic.prefix_topk(prefix, 20), ref.prefix_topk(prefix, 20)
            )
        # Re-insert everything: split logic must rebuild correctly.
        for key in keys:
            dic.insert(key, 1)
            ref.insert(key, 1)
        dic.check_invariants()
        self.assertEqual(
            dic.prefix_topk("", 1000), ref.prefix_topk("", 1000)
        )


if __name__ == "__main__":
    unittest.main()
