"""距离 DP 的正确性与下界安全性测试。"""
import heapq
import random
import unittest

from uniedit import CostModel, distance, length_lower_bound


def dijkstra_min_script(a, b, costs, alphabet, max_len):
    """参考实现：在字符串图上做 Dijkstra，求插入/删除/替换脚本的最小成本
    （不含换位）。用于验证无换位 DP == 脚本最小成本（定理 T1 的前提）。"""
    dist = {a: 0.0}
    pq = [(0.0, a)]
    while pq:
        d, s = heapq.heappop(pq)
        if d != dist[s]:
            continue
        if s == b:
            return d
        for i in range(len(s) + 1):  # 插入
            for ch in alphabet:
                t = s[:i] + ch + s[i:]
                nd = d + costs.insert(ch)
                if len(t) <= max_len and nd < dist.get(t, float("inf")):
                    dist[t] = nd
                    heapq.heappush(pq, (nd, t))
        for i in range(len(s)):  # 删除
            t = s[:i] + s[i + 1:]
            nd = d + costs.delete(s[i])
            if nd < dist.get(t, float("inf")):
                dist[t] = nd
                heapq.heappush(pq, (nd, t))
            for ch in alphabet:  # 替换
                if ch != s[i]:
                    t = s[:i] + ch + s[i + 1:]
                    nd = d + costs.substitute(s[i], ch)
                    if nd < dist.get(t, float("inf")):
                        dist[t] = nd
                        heapq.heappush(pq, (nd, t))
    raise AssertionError("unreachable within max_len")


def random_metric_costs(rng, alphabet):
    """随机对称符号图 + Floyd-Warshall 闭包 => 符号级三角不等式成立。"""
    nodes = [None] + list(alphabet)  # None 表示空字符 ε
    w = {}
    for i, x in enumerate(nodes):
        for y in nodes[i + 1:]:
            w[(x, y)] = w[(y, x)] = float(rng.randint(1, 4))
    for x in nodes:
        w[(x, x)] = 0.0
    for k in nodes:
        for i in nodes:
            for j in nodes:
                if w[(i, j)] > w[(i, k)] + w[(k, j)]:
                    w[(i, j)] = w[(i, k)] + w[(k, j)]
    return CostModel(
        insert=lambda c: w[(None, c)],
        delete=lambda c: w[(c, None)],
        substitute=lambda a, b: w[(a, b)],
        transpose=lambda a, b: float("inf"),
        min_insert=min(w[(None, c)] for c in alphabet),
        min_delete=min(w[(c, None)] for c in alphabet),
    )


class TestKnownValues(unittest.TestCase):
    def test_levenshtein_basics(self):
        c = CostModel.uniform()
        self.assertEqual(distance("kitten", "sitting", c), 3)
        self.assertEqual(distance("", "", c), 0)
        self.assertEqual(distance("", "abc", c), 3)
        self.assertEqual(distance("abc", "", c), 3)
        self.assertEqual(distance("flaw", "lawn", c), 2)

    def test_unicode_codepoints(self):
        c = CostModel.uniform()
        self.assertEqual(distance("café", "cafe", c), 1)
        self.assertEqual(distance("\U0001F600\U0001F603", "\U0001F600", c), 1)
        self.assertEqual(distance("中文", "中问", c), 1)
        # 预组合 é(U+00E9) vs 组合序列 e+U+0301：替换+插入 = 2；
        # 二者等价性由分词层 NFC 归一负责（见 test_tokenize）
        self.assertEqual(distance("caf\u00e9", "cafe\u0301", c), 2)

    def test_transpose(self):
        ct = CostModel.uniform(trans=1.0)
        self.assertEqual(distance("ca", "ac", ct), 1)
        self.assertEqual(distance("ac", "abc", ct), 1)
        # OSA 风格：换位后的子串不再重编，故为 3（三角不等式失效的反例）
        self.assertEqual(distance("ca", "abc", ct), 3)
        c0 = CostModel.uniform()  # 禁用换位
        self.assertEqual(distance("ca", "ac", c0), 2)

    def test_weighted(self):
        c = CostModel.uniform(ins=2.0, sub=3.0)
        self.assertEqual(distance("a", "ab", c), 2.0)
        self.assertEqual(distance("a", "b", c), 3.0)
        self.assertEqual(distance("a", "bc", c), 5.0)  # sub + ins


class TestDpVsBruteForce(unittest.TestCase):
    def test_no_trans_dp_equals_min_script(self):
        rng = random.Random(20260926)
        alphabet = ["a", "b", "c"]
        for _ in range(60):
            costs = random_metric_costs(rng, alphabet)
            a = "".join(rng.choice(alphabet) for _ in range(rng.randint(0, 4)))
            b = "".join(rng.choice(alphabet) for _ in range(rng.randint(0, 4)))
            got = distance(a, b, costs, use_transpose=False)
            want = dijkstra_min_script(a, b, costs, alphabet, len(a) + len(b))
            self.assertEqual(got, want, f"a={a!r} b={b!r}")

    def test_dominated_transpose_is_noop(self):
        """引理 L1 的经验验证：trans >= 无换位距离时，带换位 DP == 无换位 DP。"""
        rng = random.Random(7)
        alphabet = "abc"
        costs = CostModel.uniform(trans=2.0)  # d_notrans("ab","ba")=2，恰被支配
        for _ in range(80):
            a = "".join(rng.choice(alphabet) for _ in range(rng.randint(0, 5)))
            b = "".join(rng.choice(alphabet) for _ in range(rng.randint(0, 5)))
            self.assertEqual(
                distance(a, b, costs, use_transpose=True),
                distance(a, b, costs, use_transpose=False),
                f"a={a!r} b={b!r}",
            )


class TestLengthLowerBound(unittest.TestCase):
    def test_bound_never_exceeds_distance(self):
        rng = random.Random(11)
        alphabet = "abé中\U0001F600"
        costs = CostModel.uniform(ins=1.0, sub=2.0, trans=1.0)  # 非度量也安全
        for _ in range(200):
            a = "".join(rng.choice(alphabet) for _ in range(rng.randint(0, 6)))
            b = "".join(rng.choice(alphabet) for _ in range(rng.randint(0, 6)))
            lb = length_lower_bound(a, b, costs)
            d = distance(a, b, costs)
            self.assertLessEqual(lb, d, f"a={a!r} b={b!r}")


if __name__ == "__main__":
    unittest.main()
