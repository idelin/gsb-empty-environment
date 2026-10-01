"""randbelow 的无偏性：精确枚举证明 + 大整数支持。"""
import hashlib
import unittest

from unbiased_shuffle import CountingSource, randbelow


class ScriptedSource:
    """按脚本返回值的可复现随机源（用于精确分支枚举）。"""

    def __init__(self, values):
        self.values = list(values)
        self.draws = 0

    def getrandbits(self, k):
        self.draws += 1
        return self.values.pop(0)


class TestRandbelowExact(unittest.TestCase):
    """【精确结论】单轮接受：每个 r < n 在 k 比特空间中恰有一个原像。"""

    def test_each_value_accepted_exactly_once_per_round(self):
        for n in list(range(2, 33)) + [63, 64, 65, 100, 255, 256, 257, 1000]:
            k = (n - 1).bit_length()
            for r in range(2 ** k):
                if r < n:
                    src = ScriptedSource([r])
                    self.assertEqual(randbelow(src, n), r)
                    self.assertEqual(src.draws, 1)  # 恰一次抽取即接受
                else:
                    src = ScriptedSource([r, 0])
                    self.assertEqual(randbelow(src, n), 0)
                    self.assertEqual(src.draws, 2)  # r >= n 被拒绝并重抽

    def test_rejection_probability_below_half(self):
        # 精确：接受概率 n / 2**k > 1/2，故期望抽取次数 < 2
        for n in [2, 3, 5, 6, 7, 9, 17, 100, 10 ** 6 + 3]:
            k = (n - 1).bit_length()
            self.assertGreater(2 * n, 2 ** k)

    def test_boundary_values(self):
        self.assertEqual(randbelow(CountingSource(seed=1), 1), 0)
        with self.assertRaises(ValueError):
            randbelow(CountingSource(seed=1), 0)
        with self.assertRaises(ValueError):
            randbelow(CountingSource(seed=1), -3)


class TestRandbelowBigInt(unittest.TestCase):
    """大整数上界：n 远超 64 位。"""

    def test_huge_upper_bound(self):
        n = 10 ** 40 + 7
        src = CountingSource(seed=20261001)
        for _ in range(200):
            v = randbelow(src, n)
            self.assertTrue(0 <= v < n)
        self.assertGreater(src.bits, 0)

    def test_reproducible_with_same_seed(self):
        n = 2 ** 200 + 12345
        src1 = CountingSource(seed=7)
        seq1 = [randbelow(src1, n) for _ in range(50)]
        src2 = CountingSource(seed=7)
        seq2 = [randbelow(src2, n) for _ in range(50)]
        self.assertEqual(seq1, seq2)

    def test_rejection_with_huge_n(self):
        # 精确分支：n = 2**64 + 1，k = 65；2**65-1 被拒，5 被接受
        n = 2 ** 64 + 1
        src = ScriptedSource([2 ** 65 - 1, 5])
        self.assertEqual(randbelow(src, n), 5)
        self.assertEqual(src.draws, 2)


class TestBlockSizeIndependence(unittest.TestCase):
    """随机整数抽样不受底层比特流的块大小影响。"""

    class StreamBits:
        """从位置定义的确定性字节流（SHA-256 计数器模式）按块读取的随机源。

        字节流的第 p 个字节只取决于 (seed, p)，与读取块大小无关，
        因此同一 seed 下不同 block_size 产生完全相同的比特序列。
        """

        def __init__(self, seed, block_size):
            self.key = seed.to_bytes(8, "big")
            self.block_size = block_size
            self.pos = 0
            self.chunk = b""
            self.chunk_start = -1
            self.buf = 0
            self.nbits = 0

        def _byte_at(self, p):
            if not (self.chunk_start <= p < self.chunk_start + len(self.chunk)):
                self.chunk_start = (p // 32) * 32
                self.chunk = hashlib.sha256(
                    self.key + self.chunk_start.to_bytes(8, "big")).digest()
            return self.chunk[p - self.chunk_start]

        def getrandbits(self, k):
            while self.nbits < k:
                for _ in range(self.block_size):
                    self.buf = (self.buf << 8) | self._byte_at(self.pos)
                    self.pos += 1
                    self.nbits += 8
            shift = self.nbits - k
            out = self.buf >> shift
            self.buf &= (1 << shift) - 1
            self.nbits = shift
            return out

    def test_same_sequence_across_block_sizes(self):
        n = 997
        reference_src = self.StreamBits(42, 1)
        reference = [randbelow(reference_src, n) for _ in range(300)]
        for block_size in (2, 3, 7, 64):
            src = self.StreamBits(42, block_size)
            seq = [randbelow(src, n) for _ in range(300)]
            self.assertEqual(seq, reference)


if __name__ == "__main__":
    unittest.main()
