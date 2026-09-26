import io
import random
import unittest

from radix_dict import PrefixTopKDict, main


class ReferenceDict:
    """对照实现：普通 dict + 全量排序。"""

    def __init__(self):
        self.words = {}

    def insert(self, word, weight):
        self.words[word] = weight

    def delete(self, word):
        return self.words.pop(word, None) is not None

    def topk(self, prefix, k):
        items = [item for item in self.words.items() if item[0].startswith(prefix)]
        items.sort(key=lambda item: (-item[1], item[0]))
        return items[:k]


class AcceptanceTest(unittest.TestCase):
    def test_acceptance_boundary(self):
        d = PrefixTopKDict()
        d.insert("ab", 2)
        d.insert("abc", 3)
        # 前缀 ab：权重降序 -> abc(3) 先于 ab(2)
        self.assertEqual(d.topk("ab", 10), [("abc", 3), ("ab", 2)])
        self.assertTrue(d.delete("abc"))
        # 删除 abc 后 ab 仍可精确查到
        self.assertEqual(d.get("ab"), 2)
        self.assertIn("ab", d)
        self.assertIsNone(d.get("abc"))
        # 空前缀表示全词典
        self.assertEqual(d.topk("", 10), [("ab", 2)])
        self.assertEqual(d.check_invariants(), [])

    def test_tie_break_by_codepoint(self):
        d = PrefixTopKDict()
        for word in ["b", "a", "c"]:
            d.insert(word, 5)
        self.assertEqual([w for w, _ in d.topk("", 10)], ["a", "b", "c"])

    def test_weight_update_without_full_resort(self):
        d = PrefixTopKDict()
        d.insert("app", 1)
        d.insert("apple", 2)
        d.insert("apex", 3)
        d.insert("app", 10)  # 更新权重
        self.assertEqual(d.topk("ap", 3), [("app", 10), ("apex", 3), ("apple", 2)])
        d.insert("apex", 10)  # 同权重更新后按码点序
        self.assertEqual(d.topk("ap", 2), [("apex", 10), ("app", 10)])
        self.assertEqual(d.check_invariants(), [])

    def test_delete_merges_single_child(self):
        d = PrefixTopKDict()
        d.insert("abcd", 1)
        d.insert("abce", 2)
        self.assertEqual(d.check_invariants(), [])
        d.delete("abcd")
        # 压缩后根应只有一条 "abce" 整边
        self.assertEqual(len(d._root.children), 1)
        only = next(iter(d._root.children.values()))
        self.assertEqual(only.label, "abce")
        self.assertEqual(d.topk("abc", 5), [("abce", 2)])
        self.assertEqual(d.check_invariants(), [])

    def test_delete_nonexistent_and_reinsert(self):
        d = PrefixTopKDict()
        d.insert("xy", 1)
        self.assertFalse(d.delete("xyz"))
        self.assertFalse(d.delete("x"))
        d.delete("xy")
        self.assertEqual(len(d), 0)
        self.assertEqual(d.topk("", 5), [])
        d.insert("xy", 9)
        self.assertEqual(d.topk("x", 5), [("xy", 9)])
        self.assertEqual(d.check_invariants(), [])

    def test_empty_word_rejected(self):
        d = PrefixTopKDict()
        with self.assertRaises(ValueError):
            d.insert("", 1)


class RandomizedDifferentialTest(unittest.TestCase):
    def test_against_reference(self):
        rng = random.Random(20260927)
        d = PrefixTopKDict()
        ref = ReferenceDict()
        alphabet = "abc"
        pool = []

        def rand_word():
            return "".join(rng.choice(alphabet) for _ in range(rng.randint(1, 6)))

        for step in range(4000):
            op = rng.random()
            if op < 0.45 or not pool:
                word = rand_word()
                weight = rng.randint(1, 50)
                d.insert(word, weight)
                ref.insert(word, weight)
                if word not in pool:
                    pool.append(word)
            elif op < 0.70:
                word = rng.choice(pool)
                got = d.delete(word)
                expected = ref.delete(word)
                self.assertEqual(got, expected, f"delete({word!r}) at step {step}")
                if expected:
                    pool.remove(word)
            else:
                prefix = "".join(rng.choice(alphabet) for _ in range(rng.randint(0, 3)))
                k = rng.randint(1, 8)
                self.assertEqual(
                    d.topk(prefix, k),
                    ref.topk(prefix, k),
                    f"topk({prefix!r}, {k}) at step {step}",
                )
            if step % 200 == 0:
                self.assertEqual(d.check_invariants(), [], f"invariants at step {step}")
                self.assertEqual(len(d), len(ref.words))
        self.assertEqual(d.check_invariants(), [])
        # 收尾：全部删除后树应缩回空根
        for word in list(pool):
            d.delete(word)
        self.assertEqual(len(d), 0)
        self.assertEqual(d._root.children, {})
        self.assertEqual(d.check_invariants(), [])


class CliTest(unittest.TestCase):
    def run_cli(self, text):
        out = io.StringIO()
        main(stdin=io.StringIO(text), stdout=out)
        return out.getvalue().splitlines()

    def test_cli_session(self):
        lines = self.run_cli(
            "INSERT ab 2\n"
            "INSERT abc 3\n"
            "TOPK 5 ab\n"
            "DELETE abc\n"
            "GET ab\n"
            "GET abc\n"
            "TOPK 5 \"\"\n"
            "SIZE\n"
            "CHECK\n"
            "DELETE abc\n"
            "QUIT\n"
        )
        self.assertEqual(
            lines,
            ["OK", "OK", "abc 3", "ab 2", "OK", "ab 2", "NOT_FOUND",
             "ab 2", "1", "OK", "NOT_FOUND"],
        )


if __name__ == "__main__":
    unittest.main()
